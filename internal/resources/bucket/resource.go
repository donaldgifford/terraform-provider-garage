// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package bucket implements the garage_bucket resource — a managed S3
// bucket on a Garage cluster, addressable by id and zero or more global
// aliases, with optional size and object-count quotas and a
// `force_destroy` opt-in for non-empty teardown.
//
// Phase 3 of IMPL-0002 lands the schema, model, and Configure plumbing.
// Lifecycle methods are stubbed and intentionally return errors —
// Phases 4-7 fill them in. The resource is not registered in
// provider.Resources() until Phase 4.
package bucket

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/donaldgifford/terraform-provider-garage/internal/client"
)

// Compile-time interface assertions. Phase 7 adds
// resource.ResourceWithImportState; assertion gets added with that
// phase's commit.
var (
	_ resource.Resource              = (*Resource)(nil)
	_ resource.ResourceWithConfigure = (*Resource)(nil)
)

// Resource is the framework implementation of garage_bucket.
type Resource struct {
	client *client.Client
}

// New is the constructor passed to provider.Resources() once Phase 4
// registers the resource.
func New() resource.Resource {
	return &Resource{}
}

// Metadata sets the resource type name (provider prefix + "_bucket").
func (*Resource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

// Schema declares the bucket attribute surface per DESIGN-0002 §Schema.
// Notable choices, recorded in DESIGN-0002 §Decisions:
//   - `global_aliases` is a Set, not a List — order is semantically
//     irrelevant to Garage; using Set makes reorder-only HCL edits a no-op
//   - `max_size` / `max_objects` are nullable; `null` clears the quota,
//     literal `0` enforces a zero quota
//   - `force_destroy` defaults to false, mirroring the AWS S3 provider
//   - `id` carries UseStateForUnknown so plans don't show
//     "(known after apply)" on previously-created buckets
func (*Resource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Garage S3 bucket — a top-level storage container identified by an " +
			"immutable, admin-issued id and addressable via zero or more global aliases. Phase 2 surface: " +
			"global aliases, size and object-count quotas, and `force_destroy` for non-empty teardown. CORS " +
			"rules, lifecycle rules, and website config land in later RFC-0001 phases.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Garage-assigned bucket identifier (64-character hex string). Stable " +
					"across alias changes; the canonical reference for `terraform import`.",
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"global_aliases": schema.SetAttribute{
				MarkdownDescription: "Set of global aliases under which this bucket is reachable in S3 " +
					"requests. Order-insensitive; reordering in HCL produces no plan diff. Empty set is " +
					"valid — Garage permits id-only buckets, though they cannot be addressed by name in S3 " +
					"clients. Garage refuses to remove a bucket's last alias; rename via add-new + " +
					"remove-old, or delete the bucket instead.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.UseStateForUnknown(),
				},
			},
			"max_size": schema.Int64Attribute{
				MarkdownDescription: "Maximum total bytes the bucket may hold. `null` clears the size quota; " +
					"literal `0` enforces a read-only bucket (zero-byte quota). Garage's quota model treats " +
					"`nil` as the absence sentinel and `0` as a legitimate value — this resource preserves " +
					"the distinction.",
				Optional: true,
			},
			"max_objects": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of objects the bucket may hold. `null` clears the " +
					"object-count quota; literal `0` blocks new uploads.",
				Optional: true,
			},
			"force_destroy": schema.BoolAttribute{
				MarkdownDescription: "When true, `terraform destroy` empties the bucket via the S3 data " +
					"plane before deleting it. Requires provider-level `s3_endpoint`, `s3_access_key`, and " +
					"`s3_secret_key` to be configured (or their `GARAGE_S3_*` env equivalents). Defaults to " +
					"false — Garage refuses to delete non-empty buckets without this flag.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"created": schema.StringAttribute{
				MarkdownDescription: "Bucket creation timestamp (RFC 3339, UTC).",
				Computed:            true,
			},
			"bytes": schema.Int64Attribute{
				MarkdownDescription: "Total bytes currently stored. Updates as the bucket is mutated via S3; " +
					"plan diffs on this attribute are informational and do not trigger any API call.",
				Computed: true,
			},
			"objects": schema.Int64Attribute{
				MarkdownDescription: "Current object count. Plan diffs on this attribute are informational.",
				Computed:            true,
			},
			"unfinished_multipart_uploads": schema.Int64Attribute{
				MarkdownDescription: "Number of in-flight multipart uploads. Plan diffs on this attribute " +
					"are informational.",
				Computed: true,
			},
		},
	}
}

// Configure asserts the provider-supplied client and stashes it on the
// resource instance. Mirrors the clusterinfo data source's pattern —
// the cast failure path emits an "unexpected provider data type" error
// pointing at the provider developers, since it can only fire if the
// provider's Configure starts handing out a different type.
func (r *Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.client = c
}

// Create implements the three-step flow from DESIGN-0002 §Create flow:
//
//  1. CreateBucket — empty body; capture the new bucket id
//  2. AddBucketAlias for each plan-declared global alias
//  3. UpdateBucket if either quota is set in plan
//
// On any failure in steps 2-3, attempt a best-effort DeleteBucket
// rollback. The bucket is empty at this point (no objects can have been
// PUT yet), so DeleteBucket will succeed in the rollback path.
//
//nolint:gocritic // CreateRequest is the framework interface signature.
func (r *Resource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.client.CreateBucket(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create bucket", err.Error())
		return
	}
	bucketID := created.Id

	rollback := func(reason string) {
		tflog.Warn(ctx, "rolling back partial bucket create", map[string]any{
			"id":     bucketID,
			"reason": reason,
		})
		if delErr := r.client.DeleteBucket(ctx, bucketID); delErr != nil {
			tflog.Error(ctx, "rollback DeleteBucket failed", map[string]any{
				"id":  bucketID,
				"err": delErr.Error(),
			})
		}
	}

	if !plan.GlobalAliases.IsNull() && !plan.GlobalAliases.IsUnknown() {
		var aliases []string
		resp.Diagnostics.Append(plan.GlobalAliases.ElementsAs(ctx, &aliases, false)...)
		if resp.Diagnostics.HasError() {
			rollback("alias decode failed")
			return
		}
		for _, alias := range aliases {
			if err := r.client.AddBucketAlias(ctx, bucketID, alias); err != nil {
				resp.Diagnostics.AddError(
					fmt.Sprintf("Failed to add global alias %q", alias),
					err.Error(),
				)
				rollback("AddBucketAlias failed")
				return
			}
		}
	}

	quotas := modelToQuotas(&plan)
	if quotas.MaxSize != nil || quotas.MaxObjects != nil {
		if _, err := r.client.UpdateBucket(ctx, bucketID, quotas); err != nil {
			resp.Diagnostics.AddError("Failed to set bucket quotas", err.Error())
			rollback("UpdateBucket failed")
			return
		}
	}

	info, err := r.client.GetBucket(ctx, bucketID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to refresh bucket state after create", err.Error())
		rollback("post-create GetBucket failed")
		return
	}

	state := plan
	resp.Diagnostics.Append(applyBucketInfoToModel(info, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Read fetches authoritative bucket state from Garage and overlays it
// onto the model. On ErrNotFound (the bucket was deleted out-of-band),
// removes the resource from state so the next plan recreates it.
// force_destroy is preserved from prior state — it's provider-local and
// not represented in the API response.
//
//nolint:gocritic // ReadRequest is the framework interface signature.
func (r *Resource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	info, err := r.client.GetBucket(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Failed to read bucket", err.Error())
		return
	}

	forceDestroy := state.ForceDestroy
	resp.Diagnostics.Append(applyBucketInfoToModel(info, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.ForceDestroy = forceDestroy
	if state.ForceDestroy.IsNull() || state.ForceDestroy.IsUnknown() {
		state.ForceDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update reconciles the bucket's aliases and quotas from state →
// plan. Per DESIGN-0002 §Update flow and the Phase 2 live-API finding
// (Garage refuses last-alias removal with HTTP 400), alias adds always
// precede alias removes so a rename keeps the bucket reachable
// throughout the diff. A pure-remove diff that empties the alias set
// will surface Garage's verbatim error — that's the intended behavior.
//
//nolint:gocritic // UpdateRequest is the framework interface signature.
func (r *Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state Model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucketID := state.ID.ValueString()

	toAdd, toRemove, diags := diffGlobalAliases(ctx, plan.GlobalAliases, state.GlobalAliases)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, alias := range toAdd {
		if err := r.client.AddBucketAlias(ctx, bucketID, alias); err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Failed to add global alias %q", alias),
				err.Error(),
			)
			return
		}
	}
	for _, alias := range toRemove {
		if err := r.client.RemoveBucketAlias(ctx, bucketID, alias); err != nil {
			resp.Diagnostics.AddError(
				fmt.Sprintf("Failed to remove global alias %q", alias),
				err.Error(),
			)
			return
		}
	}

	if !plan.MaxSize.Equal(state.MaxSize) || !plan.MaxObjects.Equal(state.MaxObjects) {
		if _, err := r.client.UpdateBucket(ctx, bucketID, modelToQuotas(&plan)); err != nil {
			resp.Diagnostics.AddError("Failed to update bucket quotas", err.Error())
			return
		}
	}

	info, err := r.client.GetBucket(ctx, bucketID)
	if err != nil {
		resp.Diagnostics.AddError("Failed to refresh bucket state after update", err.Error())
		return
	}

	newState := plan
	resp.Diagnostics.Append(applyBucketInfoToModel(info, &newState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
}

// Delete deletes the bucket. Phase 4 ships a minimal implementation
// that handles the empty-bucket case via the admin API only — needed
// for the Phase 4 acceptance tests to clean up after themselves.
// Phase 6 extends this with force_destroy + S3 data-plane emptying
// for non-empty buckets.
//
//nolint:gocritic // DeleteRequest is the framework interface signature.
func (r *Resource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state Model
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bucketID := state.ID.ValueString()
	if bucketID == "" {
		return
	}

	if err := r.client.DeleteBucket(ctx, bucketID); err != nil {
		resp.Diagnostics.AddError(
			"Failed to delete bucket",
			err.Error()+
				"\n\nNote: in IMPL-0002 Phase 4 this resource only handles empty-bucket "+
				"deletion. Phase 6 adds force_destroy + S3 data-plane emptying for "+
				"non-empty buckets.",
		)
	}
}

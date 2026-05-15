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
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

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

// Create is a stub — Phase 4 of IMPL-0002 implements the real flow.
//
//nolint:gocritic // CreateRequest is the framework interface signature.
func (*Resource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError(
		"garage_bucket Create not yet implemented",
		"This lifecycle method lands in IMPL-0002 Phase 4. If you are seeing this error, "+
			"the resource was registered prematurely in provider.Resources().",
	)
}

// Read is a stub — Phase 4 implements the real flow.
//
//nolint:gocritic // ReadRequest is the framework interface signature.
func (*Resource) Read(_ context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	resp.Diagnostics.AddError(
		"garage_bucket Read not yet implemented",
		"This lifecycle method lands in IMPL-0002 Phase 4.",
	)
}

// Update is a stub — Phase 5 implements the real flow.
//
//nolint:gocritic // UpdateRequest is the framework interface signature.
func (*Resource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"garage_bucket Update not yet implemented",
		"This lifecycle method lands in IMPL-0002 Phase 5.",
	)
}

// Delete is a stub — Phase 6 implements the real flow.
//
//nolint:gocritic // DeleteRequest is the framework interface signature.
func (*Resource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError(
		"garage_bucket Delete not yet implemented",
		"This lifecycle method lands in IMPL-0002 Phase 6.",
	)
}

// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/donaldgifford/terraform-provider-garage/internal/client/openapi"
)

// Model mirrors the garage_bucket schema for plan/state marshaling.
// Field tags match the schema attribute names; framework types preserve
// the null/known/unknown distinction Terraform's planner depends on.
type Model struct {
	ID                         types.String `tfsdk:"id"`
	GlobalAliases              types.Set    `tfsdk:"global_aliases"`
	MaxSize                    types.Int64  `tfsdk:"max_size"`
	MaxObjects                 types.Int64  `tfsdk:"max_objects"`
	ForceDestroy               types.Bool   `tfsdk:"force_destroy"`
	Created                    types.String `tfsdk:"created"`
	Bytes                      types.Int64  `tfsdk:"bytes"`
	Objects                    types.Int64  `tfsdk:"objects"`
	UnfinishedMultipartUploads types.Int64  `tfsdk:"unfinished_multipart_uploads"`
}

// modelToQuotas converts the Model's nullable quota fields into the
// admin API's pointer-shaped ApiBucketQuotas. Always returns non-nil so
// the caller can request "clear quotas" by passing the result to
// UpdateBucket even when the user has dropped both attrs from HCL —
// the inner pointer fields stay nil in that case, which Garage
// interprets per quota field.
//
// Unknown values (Terraform-internal sentinel during plan computation)
// are treated like null — they shouldn't appear in fully-resolved Plan
// or State values that this function consumes.
func modelToQuotas(m *Model) *openapi.ApiBucketQuotas {
	q := &openapi.ApiBucketQuotas{}
	if !m.MaxSize.IsNull() && !m.MaxSize.IsUnknown() {
		v := m.MaxSize.ValueInt64()
		q.MaxSize = &v
	}
	if !m.MaxObjects.IsNull() && !m.MaxObjects.IsUnknown() {
		v := m.MaxObjects.ValueInt64()
		q.MaxObjects = &v
	}
	return q
}

// applyBucketInfoToModel overlays the API response onto the model in
// place, populating computed attributes (id, created, bytes, objects,
// unfinished_multipart_uploads) and reflecting authoritative server
// state for managed attributes (global_aliases, max_size, max_objects).
// `force_destroy` is provider-local and untouched.
func applyBucketInfoToModel(info *openapi.GetBucketInfoResponse, m *Model) diag.Diagnostics {
	var diags diag.Diagnostics

	m.ID = types.StringValue(info.Id)
	m.Created = types.StringValue(info.Created.UTC().Format(time.RFC3339))
	m.Bytes = types.Int64Value(info.Bytes)
	m.Objects = types.Int64Value(info.Objects)
	m.UnfinishedMultipartUploads = types.Int64Value(info.UnfinishedMultipartUploads)

	aliasValues := make([]attr.Value, 0, len(info.GlobalAliases))
	for _, a := range info.GlobalAliases {
		aliasValues = append(aliasValues, types.StringValue(a))
	}
	aliasSet, setDiags := types.SetValue(types.StringType, aliasValues)
	diags.Append(setDiags...)
	if diags.HasError() {
		return diags
	}
	m.GlobalAliases = aliasSet

	if info.Quotas.MaxSize != nil {
		m.MaxSize = types.Int64Value(*info.Quotas.MaxSize)
	} else {
		m.MaxSize = types.Int64Null()
	}
	if info.Quotas.MaxObjects != nil {
		m.MaxObjects = types.Int64Value(*info.Quotas.MaxObjects)
	} else {
		m.MaxObjects = types.Int64Null()
	}

	return diags
}

// diffGlobalAliases decodes plan and state alias Sets and returns the
// adds (plan ∖ state) and removes (state ∖ plan). Null or unknown
// values on either side are treated as empty.
//
// Used by Update to drive AddBucketAlias-then-RemoveBucketAlias calls
// in that order — see DESIGN-0002 §Update flow for why adds-before-
// removes is essential (Garage refuses last-alias removal with HTTP 400).
func diffGlobalAliases(ctx context.Context, plan, state types.Set) (add, remove []string, diags diag.Diagnostics) {
	var planAliases, stateAliases []string
	if !plan.IsNull() && !plan.IsUnknown() {
		diags.Append(plan.ElementsAs(ctx, &planAliases, false)...)
	}
	if !state.IsNull() && !state.IsUnknown() {
		diags.Append(state.ElementsAs(ctx, &stateAliases, false)...)
	}
	if diags.HasError() {
		return nil, nil, diags
	}

	stateSet := make(map[string]struct{}, len(stateAliases))
	for _, a := range stateAliases {
		stateSet[a] = struct{}{}
	}
	planSet := make(map[string]struct{}, len(planAliases))
	for _, a := range planAliases {
		planSet[a] = struct{}{}
	}

	for _, a := range planAliases {
		if _, ok := stateSet[a]; !ok {
			add = append(add, a)
		}
	}
	for _, a := range stateAliases {
		if _, ok := planSet[a]; !ok {
			remove = append(remove, a)
		}
	}
	return add, remove, diags
}

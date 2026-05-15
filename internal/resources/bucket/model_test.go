// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket

import (
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/donaldgifford/terraform-provider-garage/internal/client/openapi"
)

// TestModelToQuotas covers the three quota-translation states the Update
// path depends on, per DESIGN-0002 §Quota semantics.
func TestModelToQuotas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		maxSize           types.Int64
		maxObjects        types.Int64
		wantMaxSizeIsNil  bool
		wantMaxSize       int64
		wantMaxObjectsNil bool
		wantMaxObjects    int64
	}{
		{
			name:              "both null clear quota",
			maxSize:           types.Int64Null(),
			maxObjects:        types.Int64Null(),
			wantMaxSizeIsNil:  true,
			wantMaxObjectsNil: true,
		},
		{
			name:              "size set, objects null",
			maxSize:           types.Int64Value(1024),
			maxObjects:        types.Int64Null(),
			wantMaxSize:       1024,
			wantMaxObjectsNil: true,
		},
		{
			name:              "literal zero size preserved as zero, not nil",
			maxSize:           types.Int64Value(0),
			maxObjects:        types.Int64Null(),
			wantMaxSize:       0,
			wantMaxObjectsNil: true,
		},
		{
			name:           "both set",
			maxSize:        types.Int64Value(2048),
			maxObjects:     types.Int64Value(99),
			wantMaxSize:    2048,
			wantMaxObjects: 99,
		},
		{
			name:              "unknown treated as null",
			maxSize:           types.Int64Unknown(),
			maxObjects:        types.Int64Null(),
			wantMaxSizeIsNil:  true,
			wantMaxObjectsNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := Model{MaxSize: tt.maxSize, MaxObjects: tt.maxObjects}
			got := modelToQuotas(&m)
			if got == nil {
				t.Fatal("modelToQuotas: got nil, want non-nil")
			}
			checkInt64Ptr(t, "MaxSize", got.MaxSize, tt.wantMaxSizeIsNil, tt.wantMaxSize)
			checkInt64Ptr(t, "MaxObjects", got.MaxObjects, tt.wantMaxObjectsNil, tt.wantMaxObjects)
		})
	}
}

// checkInt64Ptr asserts a *int64's null-ness and (when non-null) its
// dereferenced value, flattening the otherwise-nested control flow in
// the quota-translation table.
func checkInt64Ptr(t *testing.T, name string, got *int64, wantNil bool, want int64) {
	t.Helper()
	if wantNil {
		if got != nil {
			t.Fatalf("%s = %d, want nil", name, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}

// TestApplyBucketInfoToModel verifies the response → model overlay path
// populates every Read-time field correctly: id, timestamps, computed
// metrics, server-authoritative aliases, and nullable quota fields.
func TestApplyBucketInfoToModel(t *testing.T) {
	t.Parallel()

	created, err := time.Parse(time.RFC3339, "2026-05-15T12:00:00Z")
	if err != nil {
		t.Fatalf("parse created: %v", err)
	}

	maxSize := int64(1024)

	info := &openapi.GetBucketInfoResponse{
		Id:                         "bucket-abc",
		Created:                    created,
		Bytes:                      9876,
		Objects:                    42,
		UnfinishedMultipartUploads: 1,
		GlobalAliases:              []string{"alpha", "beta"},
		Quotas:                     openapi.ApiBucketQuotas{MaxSize: &maxSize, MaxObjects: nil},
	}

	var m Model
	diags := applyBucketInfoToModel(info, &m)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}

	if m.ID.ValueString() != "bucket-abc" {
		t.Fatalf("ID = %q", m.ID.ValueString())
	}
	if m.Created.ValueString() != "2026-05-15T12:00:00Z" {
		t.Fatalf("Created = %q", m.Created.ValueString())
	}
	if m.Bytes.ValueInt64() != 9876 {
		t.Fatalf("Bytes = %d", m.Bytes.ValueInt64())
	}
	if m.Objects.ValueInt64() != 42 {
		t.Fatalf("Objects = %d", m.Objects.ValueInt64())
	}
	if m.UnfinishedMultipartUploads.ValueInt64() != 1 {
		t.Fatalf("UnfinishedMultipartUploads = %d", m.UnfinishedMultipartUploads.ValueInt64())
	}

	aliases := m.GlobalAliases.Elements()
	if len(aliases) != 2 {
		t.Fatalf("GlobalAliases len = %d, want 2", len(aliases))
	}

	if m.MaxSize.IsNull() || m.MaxSize.ValueInt64() != 1024 {
		t.Fatalf("MaxSize = %v, want 1024", m.MaxSize)
	}
	if !m.MaxObjects.IsNull() {
		t.Fatalf("MaxObjects = %v, want null", m.MaxObjects)
	}
}

// TestApplyBucketInfoToModel_emptyAliases verifies an empty
// globalAliases array on the API side produces an empty (but non-null)
// Set on the model side. Tests the "zero-alias bucket" Phase 3 task
// list flagged as valid.
func TestApplyBucketInfoToModel_emptyAliases(t *testing.T) {
	t.Parallel()

	info := &openapi.GetBucketInfoResponse{
		Id:            "id",
		Created:       time.Now(),
		GlobalAliases: []string{},
		Quotas:        openapi.ApiBucketQuotas{},
	}
	var m Model
	diags := applyBucketInfoToModel(info, &m)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if m.GlobalAliases.IsNull() {
		t.Fatal("GlobalAliases is null; want empty-but-known Set")
	}
	if n := len(m.GlobalAliases.Elements()); n != 0 {
		t.Fatalf("GlobalAliases len = %d, want 0", n)
	}
}

// TestApplyBucketInfoToModel_zeroQuotaPreservedDistinctFromNull confirms
// the DESIGN-0002 §Quota semantics distinction: literal zero MaxSize
// flows through as Int64Value(0), not Int64Null().
func TestApplyBucketInfoToModel_zeroQuotaPreservedDistinctFromNull(t *testing.T) {
	t.Parallel()

	zero := int64(0)
	info := &openapi.GetBucketInfoResponse{
		Id:      "id",
		Created: time.Now(),
		Quotas:  openapi.ApiBucketQuotas{MaxSize: &zero, MaxObjects: nil},
	}
	var m Model
	diags := applyBucketInfoToModel(info, &m)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}

	if m.MaxSize.IsNull() {
		t.Fatal("MaxSize is null; want literal zero (Int64Value(0))")
	}
	if got := m.MaxSize.ValueInt64(); got != 0 {
		t.Fatalf("MaxSize = %d, want 0", got)
	}
	if !m.MaxObjects.IsNull() {
		t.Fatalf("MaxObjects = %v, want null", m.MaxObjects)
	}
}

// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package clusterinfo_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/donaldgifford/terraform-provider-garage/internal/acctest"
)

// TestAccDataSourceClusterInfo is the Phase 1 acceptance gate: spin up
// a real Garage container, configure the provider against it, read the
// garage_cluster_info data source, and assert the resulting state has
// the expected shape.
//
// Per IMPL-0001 §Decisions #12, t.Parallel() is called from the start —
// this single test makes parallelism a no-op but the pattern is in
// place for Phase 2+ acceptance tests on the same runner.
func TestAccDataSourceClusterInfo(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.TestAccProviderConfig(g) + `
data "garage_cluster_info" "this" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The single-node fixture has exactly one node.
					resource.TestCheckResourceAttr("data.garage_cluster_info.this", "nodes.#", "1"),
					// Every node should report a non-empty Garage version.
					resource.TestCheckResourceAttrSet("data.garage_cluster_info.this", "nodes.0.garage_version"),
					// is_up should be true for the live single node.
					resource.TestCheckResourceAttr("data.garage_cluster_info.this", "nodes.0.is_up", "true"),
					// layout_version is set by --single-node; either 0 or 1 depending
					// on Garage's bootstrap timing — just check it's set.
					resource.TestCheckResourceAttrSet("data.garage_cluster_info.this", "layout_version"),
					// id is computed from layout_version.
					resource.TestCheckResourceAttrSet("data.garage_cluster_info.this", "id"),
				),
			},
		},
	})
}

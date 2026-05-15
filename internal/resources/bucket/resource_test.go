// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/donaldgifford/terraform-provider-garage/internal/acctest"
)

// TestAccGarageBucket_minimal is the smallest possible garage_bucket
// resource — no aliases, no quotas, default force_destroy=false.
// Confirms the Phase 4 CreateBucket → GetBucket round-trip works and
// that the resource ends up with the expected computed values
// (id present, bytes/objects/unfinished zeros).
func TestAccGarageBucket_minimal(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.TestAccProviderConfig(g) + `
resource "garage_bucket" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("garage_bucket.test", "id"),
					resource.TestCheckResourceAttrSet("garage_bucket.test", "created"),
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "0"),
					resource.TestCheckResourceAttr("garage_bucket.test", "force_destroy", "false"),
					resource.TestCheckResourceAttr("garage_bucket.test", "bytes", "0"),
					resource.TestCheckResourceAttr("garage_bucket.test", "objects", "0"),
					resource.TestCheckResourceAttr("garage_bucket.test", "unfinished_multipart_uploads", "0"),
					resource.TestCheckNoResourceAttr("garage_bucket.test", "max_size"),
					resource.TestCheckNoResourceAttr("garage_bucket.test", "max_objects"),
				),
			},
		},
	})
}

// TestAccGarageBucket_createWithAliasesAndQuotas exercises the full
// Phase 4 Create flow: empty CreateBucket → two AddBucketAlias calls →
// UpdateBucket with both quotas → GetBucket refresh. State should
// reflect every attribute the user declared.
func TestAccGarageBucket_createWithAliasesAndQuotas(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.TestAccProviderConfig(g) + `
resource "garage_bucket" "test" {
  global_aliases = ["alpha-bucket", "beta-bucket"]
  max_size       = 1048576
  max_objects    = 100
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("garage_bucket.test", "id"),
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "2"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "alpha-bucket"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "beta-bucket"),
					resource.TestCheckResourceAttr("garage_bucket.test", "max_size", "1048576"),
					resource.TestCheckResourceAttr("garage_bucket.test", "max_objects", "100"),
					resource.TestCheckResourceAttr("garage_bucket.test", "force_destroy", "false"),
					resource.TestCheckResourceAttr("garage_bucket.test", "bytes", "0"),
					resource.TestCheckResourceAttr("garage_bucket.test", "objects", "0"),
				),
			},
		},
	})
}

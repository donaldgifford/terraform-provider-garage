// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/donaldgifford/terraform-provider-garage/internal/acctest"
	"github.com/donaldgifford/terraform-provider-garage/internal/client"
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

// bucketConfigWithAliasesAndQuotas renders a garage_bucket resource
// pointing at the fixture's endpoint, with caller-supplied alias and
// quota blocks. The aliases parameter is rendered as an HCL list
// literal; pass nil for an empty list.
func bucketConfigWithAliasesAndQuotas(g *acctest.Garage, aliasesHCL, quotasHCL string) string {
	return acctest.TestAccProviderConfig(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = %s
  %s
}
`, aliasesHCL, quotasHCL)
}

// captureBucketID stores the resource's id attribute into the named
// variable on the first invocation — useful for follow-up steps that
// mutate Garage state outside Terraform's view.
func captureBucketID(target *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["garage_bucket.test"]
		if !ok {
			return errors.New("garage_bucket.test not in state")
		}
		*target = rs.Primary.Attributes["id"]
		if *target == "" {
			return errors.New("garage_bucket.test has empty id")
		}
		return nil
	}
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

// TestAccGarageBucket_updateAliases exercises the alias-diff Update path
// across the patterns DESIGN-0002 §Update flow calls out: add (1→2),
// rename (add+remove same plan), and shrink-but-keep-one (2→1).
// Adds-before-removes is critical for the rename case — without it,
// Garage's "last alias on the bucket" guard refuses the diff. The
// reorder no-op assertion lives in Phase 8's acceptance polish.
func TestAccGarageBucket_updateAliases(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: bucketConfigWithAliasesAndQuotas(g, `["one"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "1"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "one"),
				),
			},
			{
				// Add a second alias: 1 → 2.
				Config: bucketConfigWithAliasesAndQuotas(g, `["one", "two"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "2"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "one"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "two"),
				),
			},
			{
				// Rename "one" → "three": add-before-remove avoids the
				// transient zero-alias state that Garage would refuse.
				Config: bucketConfigWithAliasesAndQuotas(g, `["two", "three"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "2"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "two"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "three"),
				),
			},
			{
				// Shrink back to 1 alias.
				Config: bucketConfigWithAliasesAndQuotas(g, `["three"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "1"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "three"),
				),
			},
		},
	})
}

// TestAccGarageBucket_updateQuotas covers the three quota states DESIGN-0002
// §Quota semantics distinguishes: set, clear, and literal-zero. The
// "clear" step writes null on the model side and verifies state reads
// back null; "literal zero" writes 0 and verifies it sticks.
func TestAccGarageBucket_updateQuotas(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Start with both quotas set.
				Config: bucketConfigWithAliasesAndQuotas(g, `["bucket-one"]`, "max_size = 1024\n  max_objects = 50"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "max_size", "1024"),
					resource.TestCheckResourceAttr("garage_bucket.test", "max_objects", "50"),
				),
			},
			{
				// Clear max_size; keep max_objects.
				Config: bucketConfigWithAliasesAndQuotas(g, `["bucket-one"]`, "max_objects = 50"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("garage_bucket.test", "max_size"),
					resource.TestCheckResourceAttr("garage_bucket.test", "max_objects", "50"),
				),
			},
			{
				// Literal zero on max_size — distinct from clear.
				Config: bucketConfigWithAliasesAndQuotas(g, `["bucket-one"]`, "max_size = 0\n  max_objects = 50"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "max_size", "0"),
					resource.TestCheckResourceAttr("garage_bucket.test", "max_objects", "50"),
				),
			},
			{
				// Clear both.
				Config: bucketConfigWithAliasesAndQuotas(g, `["bucket-one"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("garage_bucket.test", "max_size"),
					resource.TestCheckNoResourceAttr("garage_bucket.test", "max_objects"),
				),
			},
		},
	})
}

// TestAccGarageBucket_driftDetection verifies the Read drift-cleanup
// path: an alias removed out-of-band (via a direct admin API call)
// surfaces in the next plan as a diff that re-adds the alias.
func TestAccGarageBucket_driftDetection(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	var bucketID string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: bucketConfigWithAliasesAndQuotas(g, `["drift-test-a", "drift-test-b"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "2"),
					captureBucketID(&bucketID),
				),
			},
			{
				// External mutation: remove one alias via the admin API
				// directly, bypassing Terraform.
				PreConfig: func() {
					c, err := client.New(g.Endpoint, g.AdminToken)
					if err != nil {
						t.Fatalf("client.New: %v", err)
					}
					if err := c.RemoveBucketAlias(context.Background(), bucketID, "drift-test-b"); err != nil {
						t.Fatalf("external RemoveBucketAlias: %v", err)
					}
				},
				// Same config — plan should detect drift and re-add
				// "drift-test-b". After apply, state matches config again.
				Config: bucketConfigWithAliasesAndQuotas(g, `["drift-test-a", "drift-test-b"]`, ``),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "global_aliases.#", "2"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "drift-test-a"),
					resource.TestCheckTypeSetElemAttr("garage_bucket.test", "global_aliases.*", "drift-test-b"),
				),
			},
		},
	})
}

// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/donaldgifford/terraform-provider-garage/internal/acctest"
	"github.com/donaldgifford/terraform-provider-garage/internal/client"
	"github.com/donaldgifford/terraform-provider-garage/internal/client/openapi"
)

// grantBucketAccess uses the admin API to grant the fixture's default
// S3 access key full data-plane access on the freshly-created bucket.
// Garage's permission model treats Owner / Read / Write as independent
// flags — Owner grants administrative ownership but not data-plane
// operations; Read/Write gate the actual S3 GET/PUT. We set all three
// so the PutObject calls below succeed.
func grantBucketAccess(t *testing.T, g *acctest.Garage, bucketID string) {
	t.Helper()
	c, err := client.New(g.Endpoint, g.AdminToken)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	yes := true
	perms := openapi.ApiBucketKeyPerm{Owner: &yes, Read: &yes, Write: &yes}
	if err := c.AllowBucketKey(context.Background(), bucketID, g.S3AccessKey, perms); err != nil {
		t.Fatalf("AllowBucketKey: %v", err)
	}
}

// putObject PUTs a small object into the named bucket alias via the
// S3 data plane using the fixture's default credentials.
func putObject(t *testing.T, g *acctest.Garage, bucketAlias, key, content string) {
	t.Helper()
	awsCfg := aws.Config{
		Region:      "garage",
		Credentials: credentials.NewStaticCredentialsProvider(g.S3AccessKey, g.S3SecretKey, ""),
	}
	cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(g.S3Endpoint)
		o.UsePathStyle = true
	})
	if _, err := cli.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(bucketAlias),
		Key:    aws.String(key),
		Body:   strings.NewReader(content),
	}); err != nil {
		t.Fatalf("PutObject(%s/%s): %v", bucketAlias, key, err)
	}
}

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

// TestAccGarageBucket_rejectNonEmptyWithoutForce — IMPL-0002 Phase 6.
// Verifies that Delete refuses to act on a non-empty bucket when
// force_destroy is false, surfacing the documented diagnostic. The
// final step flips force_destroy to true so the test framework's
// teardown destroy succeeds (otherwise Garage's bucket would leak into
// subsequent test runs of the same container — which doesn't apply
// here since containers are per-test, but the pattern is correct).
func TestAccGarageBucket_rejectNonEmptyWithoutForce(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	var bucketID string
	const alias = "force-test-reject"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureBucketID(&bucketID),
				),
			},
			{
				PreConfig: func() {
					grantBucketAccess(t, g, bucketID)
					putObject(t, g, alias, "obj-1", "hello")
				},
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "objects", "1"),
				),
			},
			{
				Config:      acctest.TestAccProviderConfigWithS3(g),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`is not empty`),
			},
			{
				// Flip force_destroy=true so the framework's final
				// teardown destroy succeeds (otherwise the bucket would
				// stay leaked but that doesn't matter — containers are
				// per-test).
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
			},
		},
	})
}

// TestAccGarageBucket_forceDestroyNonEmpty — IMPL-0002 Phase 6.
// Bucket created with force_destroy=true; an object is PUT externally;
// the test framework's teardown destroy hits the force-empty path:
// emptyBucket via S3 → admin DeleteBucket → bucket gone.
func TestAccGarageBucket_forceDestroyNonEmpty(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	var bucketID string
	const alias = "force-test-empty"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureBucketID(&bucketID),
				),
			},
			{
				PreConfig: func() {
					grantBucketAccess(t, g, bucketID)
					putObject(t, g, alias, "obj-1", "data-1")
					putObject(t, g, alias, "obj-2", "data-2")
				},
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "objects", "2"),
					resource.TestCheckResourceAttr("garage_bucket.test", "force_destroy", "true"),
				),
			},
		},
	})
}

// TestAccGarageBucket_forceDestroyMissingS3Creds — IMPL-0002 Phase 6.
// force_destroy=true on a non-empty bucket, but the provider has no
// s3_* attributes configured. emptyBucket's validate() should refuse
// before any S3 call, surfacing a diagnostic naming the missing
// fields. The final step adds the S3 attrs back so the framework's
// teardown destroy succeeds.
func TestAccGarageBucket_forceDestroyMissingS3Creds(t *testing.T) {
	t.Parallel()

	acctest.PreCheck(t)
	g := acctest.Start(t)

	var bucketID string
	const alias = "force-test-nocreds"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Provider WITHOUT s3_* attrs.
				Config: acctest.TestAccProviderConfig(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureBucketID(&bucketID),
				),
			},
			{
				PreConfig: func() {
					grantBucketAccess(t, g, bucketID)
					putObject(t, g, alias, "obj-1", "hello")
				},
				Config: acctest.TestAccProviderConfig(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("garage_bucket.test", "objects", "1"),
				),
			},
			{
				Config:      acctest.TestAccProviderConfig(g),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`force_destroy requires provider-level S3 credentials`),
			},
			{
				// Add s3_* attrs so the framework's final teardown destroy works.
				Config: acctest.TestAccProviderConfigWithS3(g) + fmt.Sprintf(`
resource "garage_bucket" "test" {
  global_aliases = [%q]
  force_destroy  = true
}
`, alias),
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

// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package acctest

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/testcontainers/testcontainers-go"

	"github.com/donaldgifford/terraform-provider-garage/internal/provider"
)

// TestAccProtoV6ProviderFactories is the protocol-v6 factory map that
// every Test* function passes to `resource.Test`. Mirrors HashiCorp's
// convention; the framework's `providerserver.NewProtocol6WithError` does
// all the heavy lifting.
//
// The "garage" key matches the provider's `Metadata().TypeName`, which
// is also the prefix users write in their .tf files
// (`provider "garage" {}`).
var TestAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"garage": providerserver.NewProtocol6WithError(provider.New("acctest")()),
}

// PreCheck verifies the test environment is usable. Skips when TF_ACC is
// unset (matches terraform-plugin-testing's own gate, but applied before
// the expensive container start so `just test` doesn't churn Docker on
// acceptance tests that will be skipped anyway) and when Docker is
// unreachable.
func PreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acctest: TF_ACC not set, skipping acceptance test")
	}
	if _, err := testcontainers.NewDockerClientWithOpts(context.Background()); err != nil {
		t.Skipf("acctest: Docker not available, skipping (%v)", err)
	}
}

// TestAccProviderConfig renders a `provider "garage" {}` block pointing
// at the running fixture's endpoint with its admin token. Compose with
// resource/data-source blocks inline:
//
//	Config: acctest.TestAccProviderConfig(g) + `
//	    data "garage_cluster_info" "this" {}
//	`
func TestAccProviderConfig(g *Garage) string {
	return fmt.Sprintf(`
provider "garage" {
  endpoint = %q
  token    = %q
}
`, g.Endpoint, g.AdminToken)
}

// TestAccProviderConfigWithS3 is TestAccProviderConfig plus the three
// S3 attributes needed by `force_destroy`. Used by IMPL-0002 Phase 6
// tests that destroy non-empty buckets.
func TestAccProviderConfigWithS3(g *Garage) string {
	return fmt.Sprintf(`
provider "garage" {
  endpoint      = %q
  token         = %q
  s3_endpoint   = %q
  s3_access_key = %q
  s3_secret_key = %q
}
`, g.Endpoint, g.AdminToken, g.S3Endpoint, g.S3AccessKey, g.S3SecretKey)
}

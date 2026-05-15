// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package acctest

import (
	"context"
	"fmt"
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

// PreCheck verifies the test environment is usable. The only requirement
// today is a reachable Docker daemon; `t.Skip`s with a clear message when
// the developer doesn't have one running so `just testacc` is still
// runnable on machines without Docker (the tests skip rather than fail).
func PreCheck(t *testing.T) {
	t.Helper()
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

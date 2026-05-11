// Build-only module — tools used by `make generate`.
// Kept separate from the provider module so tfplugindocs et al. don't
// pollute the provider's dependency graph.
//
// Run `go mod tidy` here after editing tools.go.
module tools

go 1.25.8

require github.com/hashicorp/terraform-plugin-docs v0.25.0

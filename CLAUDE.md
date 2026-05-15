# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project context

Terraform/OpenTofu provider for the Garage S3 admin API v2, built on the **Terraform Plugin Framework** (`hashicorp/terraform-plugin-framework`), not the legacy Plugin SDK v2. `.golangci.yml` carries a `depguard` rule that **denies imports from `terraform-plugin-sdk/v2`** — use the Framework equivalents (`terraform-plugin-testing` for acceptance test helpers, etc.).

The v0.1 surface, design rationale, and implementation phases are specified in **`docs/rfc/0001-garage-terraformopentofu-provider.md`** (RFC-0001) — that document is the source of truth, not the code (no provider code exists yet).

Repo was bootstrapped from the user's forge `go-ext` blueprint, then layered with Terraform-provider-specific bits ported out of the HashiCorp `terraform-provider-scaffolding-framework` template. Earlier hybrid baggage (Dockerfile, docker-bake.hcl, ct.yaml, kubebuilder justfile, docker jobs in CI) has been removed — those are forge defaults that don't apply to providers.

## Common commands

Task runner is `just` (see `justfile`). Run `just` with no args for the recipe list.

```
just build      # builds the provider binary into build/bin/
just install    # go install ./cmd/terraform-provider-garage (for ~/.terraformrc dev_overrides)
just fmt        # gofmt + goimports (local prefix github.com/donaldgifford)
just lint       # golangci-lint run ./...
just test       # go test -v -race ./...  (unit only — TF_ACC unset skips acceptance tests)
just testacc    # TF_ACC=1 go test -v -cover -timeout 120m ./internal/provider/
just generate   # cd tools && go generate ./...  → regenerates docs/{resources,data-sources}/ via tfplugindocs
just ci         # composite gate: lint + test + build + license-check
```

Tool versions pinned in `mise.toml` (Go 1.26.2, golangci-lint 2.11.4, just, etc.).

Once provider code exists (Phase 1+), single-test run: `go test -v -run TestAccGarageBucket ./internal/provider/`. Acceptance tests skip without `TF_ACC=1`.

## Architecture

Per RFC-0001 §Design/Architecture, the target layout is:

```
main.go             # providerserver.Serve entry point at repo root
                    # (HashiCorp convention; tfplugindocs requires main at provider-dir root)
internal/
  client/
    openapi/        # vendored garage-admin-v2.json + downgrade-shim output
    generated.go    # oapi-codegen output (regenerated via just generate)
    client.go       # thin wrapper: typed errors, retries
  provider/
    provider.go     # GarageProvider — Phase 2 stub, schema/Configure fill in Phase 5
  resources/{bucket,key,bucket_key,bucket_alias}/
  datasources/cluster_info/  # garage_cluster_info — Phase 6
tools/              # separate go.mod for tfplugindocs + oapi-codegen + spec-downgrade
examples/           # consumed by tfplugindocs to render docs/
```

**Current state (RFC-0001 Phase 1 shipped; Phase 2 underway):** Phase 1 merged via [PR #6](https://github.com/donaldgifford/terraform-provider-garage/pull/6). `main.go` at repo root wired to `providerserver.Serve` (with `-debug` flag). `internal/provider/provider.go` carries the `GarageProvider` skeleton — schema empty, `Configure` no-op for now, no resources or data sources registered yet (RFC-0001 Phases 2-6 add them). `internal/client/openapi/generated.go` is the `oapi-codegen` output of the Garage admin v2 OpenAPI spec, generated via `just generate`. The upstream spec is OpenAPI 3.1.0; `tools/cmd/spec-downgrade/` rewrites it to 3.0.3-compatible JSON because `kin-openapi` (oapi-codegen's parser) is 3.0-only. Both `garage-admin-v2.json` (raw) and `garage-admin-v2.openapi30.json` (downgraded) are committed. `internal/client/client.go` wraps the generated client with bearer auth, typed sentinel errors, idempotent-verb retries, and — as of IMPL-0002 Phase 1 — the six bucket CRUD methods (`CreateBucket`, `GetBucket`, `UpdateBucket`, `DeleteBucket`, `AddBucketAlias`, `RemoveBucketAlias`). RFC-0001 Phase 2 (the `garage_bucket` resource) is tracked in [IMPL-0002](docs/impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md); design rationale lives in [DESIGN-0002](docs/design/0002-phase-2-implementation-garagebucket-resource.md).

The provider registers only resources and data sources — RFC-0001 deliberately excludes functions, actions, and ephemeral resources from v0.1 (unlike the HashiCorp scaffolding template, which exercises all five primitive categories).

The registry address declared in `main.go` is `registry.terraform.io/donaldgifford/garage`. When publishing, this must match the registry owner; treat it as a single point of edit when forking.

### Docs generation

`docs/{resources,data-sources}/` is **generated** by `tfplugindocs` from `examples/` and schema descriptions — don't hand-edit. The `tools/` directory is a separate Go module (`tools/go.mod`) holding generator deps under a `//go:build generate` tag, which is why `just generate` cd's into it. CI (`.github/workflows/ci.yml` → `generate` job) fails if `just generate` produces a diff.

`docs/{adr,rfc,design,impl,investigation,plan}/` is project documentation managed by the `docz` CLI (`.docz.yaml`) — unrelated to the Terraform-generated docs above. The two coexist cleanly under `docs/` because `tfplugindocs` only touches its own known subdirectories.

### Release

GoReleaser (`.goreleaser.yml`) builds the registry-shaped artifact set: multi-OS/arch binaries named `terraform-provider-garage_v{Version}`, zip archives, SHA256SUMS with `terraform-registry-manifest.json` packaged in, GPG-signed checksums. Release flow is driven by `.github/workflows/release.yml` — the `bump-version` job uses `jefflinse/pr-semver-bump` to derive the tag from PR labels (`major`/`minor`/`patch`/`dont-release`), then GoReleaser publishes.

`GPG_FINGERPRINT` + `GPG_PRIVATE_KEY` repo secrets must be set before the first release — see ADR-0004 (deferred).

## Licensing and file headers

**License: MPL-2.0** per [ADR-0007](docs/adr/0007-provider-license-mpl-20-over-apache-20.md) — matches OpenTofu, pre-BSL Terraform, and the broader provider ecosystem.

Every Go source file (including build-only files under `tools/`) carries this two-line header:

```go
// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0
```

- Copyright year is fixed at the year of original authorship (2026); authorship attribution lives in git history, not the header.
- The `LICENSE` file at the repo root holds the full MPL-2.0 text. Non-Go files (Markdown, YAML, JSON, HCL) do not carry inline SPDX headers; the repo-level `LICENSE` applies project-wide.
- Files derived from MPL-2.0 third-party sources (e.g. HashiCorp scaffolding) retain their upstream copyright header alongside ours.
- The vendored `internal/client/openapi/garage-admin-v2.json` is unmodified from Garage HQ's published version; `oapi-codegen`-generated Go code (`generated.go`) carries our standard MPL-2.0 header — interface specs are not copyrightable software, so AGPL terms on the upstream Garage source don't propagate to generated client code.

## Open template carryovers worth knowing

- `.forge-lock.yaml` was deleted, so `forge sync` will no longer overwrite files here. Re-adding it ties the repo back to the `go-ext` blueprint.

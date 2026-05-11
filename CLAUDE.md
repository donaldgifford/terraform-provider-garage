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
cmd/terraform-provider-garage/main.go     # providerserver.Serve entry point (currently a stub)
internal/
  client/
    openapi/        # vendored garage-admin-v2.json
    generated.go    # oapi-codegen output (regenerated via go generate)
    client.go       # thin wrapper: typed errors, retries
  provider/
    provider.go     # GarageProvider — registers Resources() + DataSources() only
    config.go       # provider-level config schema (endpoint, token)
  resources/{bucket,key,bucket_key,bucket_alias}/
  datasources/{bucket,key,cluster_info}/
tools/              # separate go.mod for tfplugindocs (build-only dep)
examples/           # consumed by tfplugindocs to render docs/
```

**None of `internal/` exists yet** — Phase 1 work is initializing `go.mod`, scaffolding `cmd/terraform-provider-garage/main.go`, generating the client via `oapi-codegen`, and adding a `garage_cluster_info` smoke-test data source. The directory tree is the *target*, not the current state.

The provider will register only resources and data sources — RFC-0001 deliberately excludes functions, actions, and ephemeral resources from v0.1 (unlike the HashiCorp scaffolding template, which exercises all five primitive categories).

### Docs generation

`docs/{resources,data-sources}/` is **generated** by `tfplugindocs` from `examples/` and schema descriptions — don't hand-edit. The `tools/` directory is a separate Go module (`tools/go.mod`) holding generator deps under a `//go:build generate` tag, which is why `just generate` cd's into it. CI (`.github/workflows/ci.yml` → `generate` job) fails if `just generate` produces a diff.

`docs/{adr,rfc,design,impl,investigation,plan}/` is project documentation managed by the `docz` CLI (`.docz.yaml`) — unrelated to the Terraform-generated docs above. The two coexist cleanly under `docs/` because `tfplugindocs` only touches its own known subdirectories.

### Release

GoReleaser (`.goreleaser.yml`) builds the registry-shaped artifact set: multi-OS/arch binaries named `terraform-provider-garage_v{Version}`, zip archives, SHA256SUMS with `terraform-registry-manifest.json` packaged in, GPG-signed checksums. Release flow is driven by `.github/workflows/release.yml` — the `bump-version` job uses `jefflinse/pr-semver-bump` to derive the tag from PR labels (`major`/`minor`/`patch`/`dont-release`), then GoReleaser publishes.

`GPG_FINGERPRINT` + `GPG_PRIVATE_KEY` repo secrets must be set before the first release — see ADR-0004 (deferred).

## Open template carryovers worth knowing

- `.forge-lock.yaml` was deleted, so `forge sync` will no longer overwrite files here. Re-adding it ties the repo back to the `go-ext` blueprint.
- Forge's `LICENSE`/copyright story is **Apache-2.0**, but RFC `docs/repo-init.md` §6 argues for **MPL-2.0** (matches OpenTofu, pre-BSL Terraform, the broader provider ecosystem). License hasn't been chosen yet — confirm with the user before adding copyright headers.

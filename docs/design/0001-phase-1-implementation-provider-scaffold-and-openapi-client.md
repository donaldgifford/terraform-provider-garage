---
id: DESIGN-0001
title: "Phase 1 implementation: provider scaffold and OpenAPI client"
status: Draft
author: Donald Gifford
created: 2026-05-11
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0001: Phase 1 implementation: provider scaffold and OpenAPI client

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-11

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Target directory layout](#target-directory-layout)
  - [Module bootstrap](#module-bootstrap)
  - [OpenAPI client codegen](#openapi-client-codegen)
  - [Client wrapper](#client-wrapper)
  - [Provider scaffold](#provider-scaffold)
  - [garageclusterinfo data source](#garageclusterinfo-data-source)
  - [Acceptance test fixture](#acceptance-test-fixture)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Phase 1 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) turns the
current empty scaffold into a buildable provider that connects to a real Garage
admin v2 API. Concretely it delivers: a working Go module, an `oapi-codegen`
toolchain producing a client from the vendored Garage OpenAPI spec, a provider
block with bearer-token auth, a single `garage_cluster_info` data source as the
smoke test, and a `testcontainers-go` fixture that lets acceptance tests run
against a real Garage container in CI.

No v0.1 resources (`garage_bucket`, `garage_key`, `garage_bucket_key`,
`garage_bucket_alias`) land in Phase 1 — they begin in Phase 2.

## Goals and Non-Goals

### Goals

- Repo compiles end-to-end: `just build`, `just lint`, `just test`, `just generate`, `just testacc` all succeed
- `cd tools && go mod tidy` produces a working `tools/go.sum`; `just generate` runs `oapi-codegen` + `tfplugindocs` without error
- Provider authenticates against Garage admin API v2 with a bearer token
- One read-only data source — `garage_cluster_info` — works against a real Garage container
- `internal/acctest/` testcontainers fixture is reusable by Phases 2-5
- The Garage OpenAPI spec is vendored, pinned to a specific Garage version, with a documented upgrade procedure
- CI runs the new acceptance test, gated on Docker availability

### Non-Goals

- Any of the v0.1 resources (Phases 2-5)
- Write-only / ephemeral resource plumbing (Phase 3 — `garage_key`)
- Provider docs polish / `examples/` content beyond `examples/provider/` (Phase 7)
- Registry publishing / GoReleaser signing setup (Phase 7, blocks on ADR-0004)
- OpenTofu matrix in CI (deferred until at least one resource exists to test against)

## Background

State of the repo today:

- Forge `go-ext` blueprint + Terraform-provider-specific bits ported from the
  HashiCorp scaffolding template (`tools/`, `examples/provider/`,
  `terraform-registry-manifest.json`, `depguard` SDK-v2 ban in `.golangci.yml`)
- `cmd/terraform-provider-garage/main.go` is a `package main` stub with no body
- No `go.mod`, no `internal/`, no `tools/go.sum`
- `justfile`, `.goreleaser.yml`, CI workflows are provider-shaped but have nothing to act on yet

Decisions that gate Phase 1 are mostly **already documented as notes** in
[`docs/additional.md`](../additional.md):

| Topic                     | Status            | Phase 1 action                                |
|---------------------------|-------------------|-----------------------------------------------|
| Plugin Framework vs SDKv2 | ADR-0002 notes    | Author ADR-0002 from notes; ratify            |
| OpenAPI client via oapi-codegen | ADR-0003 notes | Author ADR-0003 from notes; ratify          |
| Testcontainers-Go         | ADR-0005 notes    | Author ADR-0005 from notes; ratify            |
| `garage_key` secret model | [ADR-0001](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md) Proposed | Not Phase 1 — Phase 3      |
| Resource boundaries       | ADR-0006 notes    | Not Phase 1 — Phases 2-5                      |
| Dual registry + GPG signing | ADR-0004 notes  | Not Phase 1 — Phase 7                         |
| **Provider license** (Apache-2.0 vs MPL-2.0) | **No notes — new** | **Author ADR-0007 before writing copyright headers** |

Forge's `go-ext` default is Apache-2.0. RFC `docs/repo-init.md` §6 recommends
MPL-2.0 for ecosystem alignment with OpenTofu, pre-BSL Terraform, and most
existing providers. This decision needs an ADR before Phase 1 ships, because
the copyright headers on every Go file written in Phase 1 depend on it.

## Detailed Design

### Target directory layout

After Phase 1, this is what exists on disk:

```
cmd/terraform-provider-garage/
  main.go                       # providerserver.Serve(); -debug flag
go.mod
go.sum
internal/
  client/
    openapi/
      garage-admin-v2.json      # vendored spec, pinned to Garage vX.Y.Z
      generated.go              # oapi-codegen output (generated)
      doc.go                    # //go:generate directive for oapi-codegen
    client.go                   # thin wrapper: bearer auth, typed errors, retries
  provider/
    provider.go                 # GarageProvider, Schema, Configure
  datasources/
    cluster_info/
      data_source.go
      data_source_test.go
  acctest/
    fixture.go                  # testcontainers helper
    provider.go                 # testAccProtoV6ProviderFactories + helpers
tools/
  go.mod                        # adds oapi-codegen
  go.sum
  tools.go                      # tfplugindocs + oapi-codegen //go:generate
examples/
  provider/provider.tf          # already exists
  data-sources/garage_cluster_info/
    data-source.tf              # new in Phase 1
```

Two notable layout choices worth calling out:

1. **Subpackage layout for data sources** (`internal/datasources/cluster_info/`)
   per RFC-0001 §Design/Architecture, not the HashiCorp scaffolding's flat
   `internal/provider/` layout. The v0.1 surface has 4 resources + 3 data
   sources; keeping each in its own package isolates schema and test code and
   matches the RFC.
2. **`//go:generate` for the OpenAPI client lives in
   `internal/client/openapi/doc.go`**, not in `tools/tools.go`. The reason: the
   generated code is part of the provider's main module (it's imported by
   `client.go`), so its `go:generate` must run from a file in the main module.
   `tools/tools.go` continues to drive `tfplugindocs` and runs from the build-only
   `tools` module.

### Module bootstrap

```bash
go mod init github.com/donaldgifford/terraform-provider-garage
```

Direct dependencies added during Phase 1:

| Dep                                                          | Why                                            |
|--------------------------------------------------------------|------------------------------------------------|
| `github.com/hashicorp/terraform-plugin-framework`            | Provider implementation                         |
| `github.com/hashicorp/terraform-plugin-go`                   | tfprotov6 server                                |
| `github.com/hashicorp/terraform-plugin-log`                  | tflog                                           |
| `github.com/hashicorp/terraform-plugin-testing`              | Acceptance test harness                         |
| `github.com/oapi-codegen/runtime`                            | Runtime helpers for oapi-codegen output         |
| `github.com/testcontainers/testcontainers-go`                | Acceptance test fixture                         |

`tools/go.mod` (the build-only module) adds:

| Dep                                                                       | Why                              |
|---------------------------------------------------------------------------|----------------------------------|
| `github.com/hashicorp/terraform-plugin-docs` (already pinned)             | `tfplugindocs generate`          |
| `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`                | Client generator                 |

### OpenAPI client codegen

Per ADR-0003 (notes): generate types + client from the Garage admin v2 OpenAPI
spec using `oapi-codegen`.

**Spec source:** vendor `garage-admin-v2.json` from a pinned Garage release tag
(e.g. `v2.4.0`). The spec ships in the Garage repo under `doc/api/`. Pin
mechanism: a `SPEC_VERSION` constant in `internal/client/openapi/doc.go` plus a
commit-time check that the vendored file matches the upstream URL at that tag.

**Generator config:** `internal/client/openapi/oapi-codegen.yaml`:

```yaml
package: openapi
output: generated.go
generate:
  models: true
  client: true
  embedded-spec: false
```

Types + client (not types-only). The thin wrapper in `internal/client/client.go`
adapts the generated `ClientWithResponses` for the rest of the provider — bearer
token injection, request/response logging via `tflog`, retry-on-5xx, and typed
error translation.

**Generate directive** (in `internal/client/openapi/doc.go`):

```go
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml garage-admin-v2.json
```

Generation invocation extended in `just generate`: runs both
`go generate ./internal/client/openapi/...` (client) and `cd tools && go generate ./...`
(docs). Order matters — client first, then docs.

### Client wrapper

`internal/client/client.go` exposes:

```go
type Client struct {
    api  *openapi.ClientWithResponses
    endpoint string
    // tflog handled per-call via context
}

func New(endpoint, token string) (*Client, error) { ... }
func (c *Client) GetClusterStatus(ctx context.Context) (*ClusterStatus, error) { ... }
// etc., one wrapper method per provider operation, hand-written as v0.1 grows
```

Bearer auth is injected via `oapi-codegen`'s `RequestEditorFn`. Retries: only
on 5xx, exponential backoff capped at 3 attempts, configurable later via env.
Errors from the API: typed (`*ErrNotFound`, `*ErrUnauthorized`, etc.) so
resource/data-source code doesn't need to inspect HTTP status codes directly.

### Provider scaffold

`internal/provider/provider.go` defines `GarageProvider`. Single-package
provider definition; data sources are registered from their subpackages.

Schema:

```hcl
provider "garage" {
  endpoint = "https://garage.example.com:3903"   # or GARAGE_ENDPOINT
  token    = var.garage_admin_token              # or GARAGE_TOKEN
}
```

```go
func (p *GarageProvider) Schema(...) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "endpoint": schema.StringAttribute{
                MarkdownDescription: "Garage admin API endpoint URL. Defaults to GARAGE_ENDPOINT.",
                Optional: true,
            },
            "token": schema.StringAttribute{
                MarkdownDescription: "Admin API bearer token. Defaults to GARAGE_TOKEN.",
                Optional: true,
                Sensitive: true,
            },
        },
    }
}
```

`Configure()` precedence: provider block value → env var → diagnostic error if
neither is set. Both attributes are `Optional: true` so a CI workflow can
configure entirely via env. Token marked `Sensitive: true` so it doesn't appear
in plan output.

Provider satisfies only `provider.Provider` in Phase 1 — not
`ProviderWithFunctions`, `ProviderWithActions`, or `ProviderWithEphemeralResources`.
(RFC-0001 deliberately excludes those primitives from v0.1.)

### `garage_cluster_info` data source

Purpose: end-to-end smoke test. If this data source reads cleanly against a
real Garage instance, the provider, the client wrapper, the auth flow, and
the testcontainers fixture all work.

Backing call: `GET /v2/GetClusterStatus` (exact path subject to spec
inspection during impl).

Schema (provisional — finalize against actual spec response):

```hcl
data "garage_cluster_info" "this" {}

output "garage_version" { value = data.garage_cluster_info.this.garage_version }
output "layout_version" { value = data.garage_cluster_info.this.layout_version }
output "node_count"     { value = length(data.garage_cluster_info.this.nodes) }
```

Computed attributes: `garage_version`, `layout_version`, `nodes` (list of
objects with id/role/zone/capacity). `Read()` calls `client.GetClusterStatus`,
maps response → model, writes state.

### Acceptance test fixture

`internal/acctest/fixture.go` wraps testcontainers-go:

```go
type Garage struct {
    container testcontainers.Container
    Endpoint  string
    AdminToken string
}

func Start(t *testing.T) *Garage { ... }
```

- Image: `dxflrs/garage:v2.4.0` (pinned; bump deliberately)
- Args: `--single-node --default-bucket` (requires Garage v2.3.0+)
- Env: `GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`,
  `GARAGE_DEFAULT_BUCKET` — values randomized per container
- Admin token: discovered by either env-injection or parsing container logs
  (decide during impl based on what v2.4.0 exposes)
- Per-package lifecycle (`TestMain`-style), not per-test — Garage cold start is
  ~2-5s and we don't want to pay that per `t.Run`
- Returns endpoint URL + token, ready to be plugged into `provider "garage" {}`
  blocks in test configs

`internal/acctest/provider.go` exposes:

```go
var TestAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
    "garage": providerserver.NewProtocol6WithError(provider.New("test")()),
}

func PreCheck(t *testing.T) {
    // Verify Docker is available; skip if not (local dev without Docker)
}
```

No `echoprovider` variant in Phase 1 — that's a Phase 3 (`garage_key`) concern.

## API / Interface Changes

This is greenfield — nothing to migrate. The provider's external surface after
Phase 1:

```hcl
terraform {
  required_providers {
    garage = { source = "donaldgifford/garage" }
  }
}

provider "garage" {
  endpoint = "..."   # or GARAGE_ENDPOINT
  token    = "..."   # or GARAGE_TOKEN
}

data "garage_cluster_info" "this" {}
```

`justfile` interface gains no new recipes — `just generate`, `just testacc`,
etc., already exist and start doing real work.

`.github/workflows/ci.yml` adds an `acceptance` job:

```yaml
acceptance:
  name: Acceptance — Terraform ${{ matrix.terraform }}
  runs-on: ubuntu-latest
  strategy:
    fail-fast: false
    matrix:
      terraform: ['1.13.*', '1.14.*']
  steps:
    - uses: actions/checkout@v6
    - uses: actions/setup-go@v6
      with: { go-version-file: go.mod }
    - uses: hashicorp/setup-terraform@v3
      with:
        terraform_version: ${{ matrix.terraform }}
        terraform_wrapper: false
    - uses: extractions/setup-just@v3
    - run: just testacc
```

OpenTofu added in a later phase once we have more surface area to validate.

## Data Model

Phase 1 introduces one Terraform-state-visible type: `garage_cluster_info`
attributes. Schema mirrors the OpenAPI response one-to-one for now —
no derived/computed enrichment. Detailed attribute set decided during impl
once the spec is inspected.

Generated types from `oapi-codegen` (under `internal/client/openapi/`) are
internal — not part of the Terraform state surface, not exported beyond the
provider's `internal/`.

## Testing Strategy

**Unit tests:** `just test` covers anything in `internal/client/` that has
non-trivial logic (retry wrapper, error translation). The generated code is
not unit-tested (would be testing oapi-codegen itself).

**Acceptance tests:** `just testacc` runs `internal/datasources/cluster_info/data_source_test.go`
against a real Garage container. Single test (`TestAccDataSourceClusterInfo`)
with one step: configure the provider with the fixture's endpoint/token,
read the data source, assert non-empty `garage_version`.

**CI gates:**

| Gate                                        | Job                  | Blocks merge |
|---------------------------------------------|----------------------|--------------|
| `just lint` (incl. depguard SDK-v2 ban)     | `lint`               | yes          |
| `just test` (unit, race detector)           | `test-go`            | yes          |
| `just generate` produces no diff            | `generate`           | yes          |
| `goreleaser build --snapshot`               | `build`              | yes          |
| `just testacc` (TF matrix)                  | `acceptance` (new)   | yes          |
| `govulncheck` + Trivy                       | `security`           | yes          |

**Local dev without Docker:** `PreCheck()` skips acceptance tests with a clear
message if Docker isn't reachable, so `just test` (unit only) and
`just testacc` (acceptance) have a clean separation.

## Migration / Rollout Plan

Strict order — each step is buildable / runnable before the next starts:

1. **Resolve license.** Author ADR-0007 for Apache-2.0 vs MPL-2.0. Capture
   decision; update copyright header convention for the project.
2. **Ratify pending ADRs.** Author ADR-0002 (framework), ADR-0003 (oapi-codegen),
   ADR-0005 (testcontainers) from their notes in `docs/additional.md`.
3. **`go mod init`** and write the minimal `cmd/terraform-provider-garage/main.go`
   + `internal/provider/provider.go` stubs so `just build` succeeds with no
   resources / data sources registered.
4. **Vendor OpenAPI spec.** Copy `garage-admin-v2.json` from the pinned Garage
   tag into `internal/client/openapi/`. Add `oapi-codegen.yaml`, `doc.go` with
   `//go:generate`, and a `oapi-codegen` entry in `tools/go.mod`. Run
   `cd tools && go mod tidy`.
5. **Generate client.** Wire `just generate` to run client codegen before docs
   codegen. Verify generated code compiles.
6. **Client wrapper.** Implement `internal/client/client.go`: bearer auth via
   `RequestEditorFn`, retry policy, typed errors, the single
   `GetClusterStatus` wrapper method.
7. **Provider `Configure()`.** Wire `endpoint`/`token` → `client.New()`, hand
   client to `DataSourceData` / `ResourceData`.
8. **`garage_cluster_info` data source.** Schema + `Read()` against the client.
9. **Acceptance fixture.** `internal/acctest/` testcontainers helper.
10. **Acceptance test.** `TestAccDataSourceClusterInfo` — fixture starts Garage,
    test reads data source, asserts non-empty version.
11. **`just testacc` passes locally.** Manual gate before pushing to CI.
12. **CI acceptance job.** Add the matrix job to `.github/workflows/ci.yml`.
13. **Provider docs.** Run `just generate` for `tfplugindocs`. Commit
    `docs/index.md` + `docs/data-sources/garage_cluster_info.md`.

Each step can be a separate commit / PR for reviewability.

## Open Questions

1. **OpenAPI spec source URL.** Garage repo path? Mirror in
   `eyebrowkang/garage-admin-console`? Decide during step 4 — prefer the
   official Garage repo if the spec is committed there.
2. **Garage version to pin.** Minimum: `v2.3.0` for `--single-node --default-bucket`.
   Recommend latest stable `v2.x` at impl time, document upgrade procedure.
3. **`oapi-codegen` major version.** v2 is current. Pin in `tools/go.mod`.
4. **Admin token discovery in the testcontainers fixture.** Env-injection,
   container log parsing, or pre-baked image? Spike during step 9.
5. **Per-package vs per-test container lifecycle.** Default to per-package
   (`TestMain`), reassess if tests start needing isolated state.
6. **Retry strategy details.** 3 attempts, exponential backoff with what base?
   Configurable via provider config attribute or hardcoded? Hardcode in Phase 1,
   externalize later if needed.
7. **OpenTofu CI matrix.** RFC-0001 §Publishing requires dual-CLI testing.
   Adding `tofu` to the matrix now is cheap but adds noise if `setup-opentofu`
   isn't quite the same shape as `setup-terraform`. Defer to Phase 2 unless
   trivial.
8. **License choice (Apache-2.0 vs MPL-2.0).** Tracked as ADR-0007 in step 1
   above — flagged here because it's the only unresolved decision that blocks
   writing the first line of Go code.

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
- [ADR-0001: `garage_key` secret handling](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md)
- [Additional ADRs (notes — ADR-0002, 0003, 0004, 0005, 0006)](../additional.md)
- [`docs/repo-init.md`](../repo-init.md) — license discussion in §6
- [Garage admin API v2 documentation](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [terraform-plugin-framework documentation](https://developer.hashicorp.com/terraform/plugin/framework)
- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
- [testcontainers-go](https://golang.testcontainers.org/)

---
id: IMPL-0001
title: "Phase 1: provider scaffold, OpenAPI client, smoke test"
status: Draft
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0001: Phase 1: provider scaffold, OpenAPI client, smoke test

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-15

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Foundational decisions (ADRs)](#phase-1-foundational-decisions-adrs)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Module bootstrap](#phase-2-module-bootstrap)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: OpenAPI spec + client codegen](#phase-3-openapi-spec--client-codegen)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Client wrapper](#phase-4-client-wrapper)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Provider Configure()](#phase-5-provider-configure)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: garageclusterinfo data source](#phase-6-garageclusterinfo-data-source)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Acceptance test fixture](#phase-7-acceptance-test-fixture)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: First acceptance test](#phase-8-first-acceptance-test)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
  - [Phase 9: CI integration](#phase-9-ci-integration)
    - [Tasks](#tasks-8)
    - [Success Criteria](#success-criteria-8)
  - [Phase 10: Provider docs generation](#phase-10-provider-docs-generation)
    - [Tasks](#tasks-9)
    - [Success Criteria](#success-criteria-9)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute Phase 1 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
per the detailed design in
[DESIGN-0001](../design/0001-phase-1-implementation-provider-scaffold-and-openapi-client.md).
Outcome: a buildable provider with one read-only data source
(`garage_cluster_info`) verified against a real Garage container in CI, plus a
reusable acceptance-test fixture that Phases 2-5 of the RFC will consume.

**Implements:** DESIGN-0001, which scopes Phase 1 of RFC-0001.

## Scope

### In Scope

- All four pending ADRs (license, framework, oapi-codegen, testcontainers)
- `go mod init` and module bootstrap
- Vendored Garage admin v2 OpenAPI spec, pinned and documented
- `oapi-codegen` generation wired into `just generate`
- Thin client wrapper: bearer auth, typed errors, retry-on-5xx
- Provider block (endpoint, token) with env-var fallback
- `garage_cluster_info` data source
- `internal/acctest/` testcontainers-go fixture
- First acceptance test, passing locally and in CI
- `examples/data-sources/garage_cluster_info/` + regenerated `docs/`

### Out of Scope

- Any of the v0.1 resources (`garage_bucket`, `garage_key`, `garage_bucket_key`,
  `garage_bucket_alias`) — Phases 2-5 of RFC-0001
- Write-only / ephemeral resource plumbing — Phase 3 of RFC-0001 (`garage_key`)
- GoReleaser signing, registry submission — Phase 7 of RFC-0001, blocks on ADR-0004
- OpenTofu CI matrix — deferred until at least one resource exists to test against
- README rewrite, quickstart guide — Phase 7 of RFC-0001

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all tasks are
checked off and its success criteria are met. The phase boundaries are
deliberately granular so each can land as its own commit or PR.

---

### Phase 1: Foundational decisions (ADRs)

Lock in the four pending ADRs before writing Go. The license decision (ADR-0007)
is the only one that strictly blocks code (copyright headers depend on it); the
other three are ratifications of decisions already captured as notes in
[`docs/additional.md`](../additional.md).

#### Tasks

- [x] Author **ADR-0007: Provider license — Apache-2.0 vs MPL-2.0** → decided MPL-2.0; status Accepted
- [x] Author **ADR-0002: Use terraform-plugin-framework over SDKv2** from notes → Accepted
- [x] Author **ADR-0003: OpenAPI-generated client via oapi-codegen** from notes → Accepted
- [x] Author **ADR-0005: testcontainers-go for acceptance tests** from notes → Accepted
- [x] Run `docz update adr` to refresh the ADR index
- [x] If MPL-2.0 selected: add `SPDX-License-Identifier: MPL-2.0` to the
      file-header convention; otherwise document Apache-2.0 convention →
      MPL-2.0 chosen; convention documented in ADR-0007 §File-header
      convention and CLAUDE.md §Licensing and file headers. `LICENSE` at
      repo root holds the canonical MPL-2.0 text fetched from mozilla.org.
- [x] Update the file header in `tools/tools.go` to match chosen license →
      `// Copyright (c) 2026 Donald Gifford` + `// SPDX-License-Identifier: MPL-2.0`

#### Success Criteria

- ADR-0002, -0003, -0005, -0007 exist with status `Accepted` (or at minimum
  `Proposed` with an explicit "ratified for Phase 1 use" note)
- Copyright header convention documented and applied to existing Go file
  (`tools/tools.go`)
- `docs/adr/README.md` lists all four ADRs

---

### Phase 2: Module bootstrap

Get the repo to a buildable state with no resources or data sources registered.
Intentionally minimal — just enough scaffolding that `just build` and
`just lint` succeed against an empty provider.

#### Tasks

- [x] `go mod init github.com/donaldgifford/terraform-provider-garage`
- [x] `go get` the Phase 1 direct deps:
  - `github.com/hashicorp/terraform-plugin-framework`
  - `github.com/hashicorp/terraform-plugin-framework-validators` (used in Phase 5)
  - `github.com/hashicorp/terraform-plugin-go`
  - `github.com/hashicorp/terraform-plugin-log`
  - `github.com/hashicorp/terraform-plugin-testing`
- [x] Write `cmd/terraform-provider-garage/main.go` — `providerserver.Serve`
      with `-debug` flag wired
- [x] Write `internal/provider/provider.go` with `GarageProvider` skeleton
  - `Metadata()` sets `resp.TypeName = "garage"`
  - `Schema()` with empty attribute map (filled in Phase 5)
  - `Configure()` is a no-op (filled in Phase 5)
  - `Resources()` and `DataSources()` return empty slices
- [x] `go mod tidy`
- [x] Verify `just build` succeeds (binary at `build/bin/terraform-provider-garage`)
- [x] Verify `just lint` succeeds (no `depguard` violations)
- [x] Verify `just test` succeeds (no tests yet, but `go test ./...` exits 0)

#### Success Criteria

- `just build` produces a runnable provider binary
- `just lint` exits clean
- `just test` exits clean
- `go.mod` and `go.sum` committed; no `go.sum` regen drift

---

### Phase 3: OpenAPI spec + client codegen

Vendor the Garage admin v2 OpenAPI spec and wire `oapi-codegen` into the
generate pipeline. Per ADR-0003 (authored in Phase 1 above).

#### Tasks

- [x] Fetch `https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json` (Garage HQ's
      auto-published spec, generated by `utoipa` from the upstream Rust source)
      and save to `internal/client/openapi/garage-admin-v2.json`
- [x] Record the spec's `info.version` in a Go constant alongside (e.g.
      `internal/client/openapi/version.go` → `const SpecVersion = "v2.3.0"`).
      Latest stable at impl time satisfies the v2.3.0 minimum needed for
      `--single-node --default-bucket`
- [x] **Verify `oapi-codegen` v2 handles OpenAPI 3.1.0** — confirmed: it does
      *not*. `kin-openapi` (oapi-codegen's underlying parser) rejects 3.1.0
      constructs: type-unions (`"type": ["X", "null"]`), nullable-ref idiom
      (`oneOf: [{type:"null"}, X]`), and tuple schemas (`prefixItems` +
      `items: false`). Resolution: a downgrade-shim Go program at
      `tools/cmd/spec-downgrade/` rewrites the spec to a 3.0.3-compatible
      form, and oapi-codegen runs against the rewritten output. Both raw
      and downgraded JSON are committed; the shim runs as the first step of
      `just generate`
- [x] Document the upgrade procedure inline in `internal/client/openapi/doc.go`
      (fetch URL, version constant, regenerate, commit drift)
- [x] Write `internal/client/openapi/oapi-codegen.yaml`:
  - `package: openapi`
  - `output: generated.go`
  - `generate: {models: true, client: true, embedded-spec: false}`
  - `output-options.response-type-suffix: HTTPResponse` — required to avoid
    name collisions between schema-derived model types and client wrapper
    types (oapi-codegen defaults to `<OpID>Response` for both, which clashes
    for operations whose response schema is `<OpID>Response` in the spec)
- [x] Write `internal/client/openapi/doc.go` with package comment and
      `//go:generate go run -modfile=../../../tools/go.mod github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml garage-admin-v2.openapi30.json`
- [x] Add `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` to
      `tools/go.mod`; `cd tools && go mod tidy`
- [x] Add `github.com/oapi-codegen/runtime` to the main `go.mod`
- [x] Update `just generate` recipe to run client codegen before docs codegen,
      with the downgrade shim as the very first step
- [x] Run `just generate`; verify `internal/client/openapi/generated.go`
      compiles (`go build ./...`)
- [x] Commit `internal/client/openapi/generated.go` (matches the
      `tfplugindocs`-generated `docs/` pattern; CI drift check covers it)
- [x] Ensure `just generate` is idempotent (run twice, no diff — confirmed
      against generated.go, downgraded spec, and docs/index.md)

#### Success Criteria

- `just generate` produces a working client without errors
- `go build ./...` succeeds with generated code in place
- Re-running `just generate` produces zero diff
- Upgrade procedure documented in `internal/client/openapi/doc.go`

---

### Phase 4: Client wrapper

Hand-written thin wrapper around the generated client. Centralizes bearer auth,
retry logic, typed errors, and `tflog` integration so resource/data-source code
doesn't see HTTP-level concerns.

#### Tasks

- [x] Write `internal/client/client.go` with:
  - `type Client struct { api *openapi.ClientWithResponses; endpoint string }`
  - `func New(endpoint, token string) (*Client, error)` — performs only
        cheap validation (URL parseable, http(s) scheme, token non-empty);
        does **not** make a network call. Constructs the generated client
        with a `RequestEditorFn` that injects `Authorization: Bearer <token>`
  - `func (c *Client) GetClusterStatus(ctx context.Context) (*openapi.GetClusterStatusResponse, error)`
  - `func (c *Client) Endpoint() string` — accessor used by acctest fixtures
- [x] Define typed error sentinels: `ErrNotFound`, `ErrUnauthorized`,
      `ErrForbidden`, `ErrServerError`. `APIError` struct wraps a sentinel
      with the HTTP status code, body message, and operation name; supports
      `errors.Is` against the sentinel
- [x] Implement retry-on-5xx with exponential backoff: **3 attempts max,
      250ms base, 2x multiplier** (so 250ms → 500ms → 1000ms; worst-case
      ~1.75s total wait). Implemented as an `http.RoundTripper` wrapper
      at `internal/client/transport.go` so retry policy is centralized
- [x] Retry only **idempotent verbs** (GET, HEAD, OPTIONS). POST/PUT/PATCH/
      DELETE 5xx responses pass through immediately to avoid risking
      duplicate server-side side effects
- [x] Map HTTP error codes → typed errors in `statusToError()` helper in
      `internal/client/errors.go`
- [x] Wire `tflog.Trace` for request/response cycles in client methods,
      `tflog.Debug` for retry attempts
- [x] Write unit tests for retry behavior using `httptest.NewServer`
- [x] Write unit tests for error mapping (each typed error sentinel reachable)
- [x] Run `just test` — verify wrapper coverage (~0.76s per retry test;
      all tests pass)

#### Success Criteria

- `just test` passes; wrapper unit-tested in isolation (no live Garage needed)
- Retry behavior verified against a controllable HTTP test server
- All exported types and functions have doc comments

---

### Phase 5: Provider `Configure()`

Wire the provider's schema and `Configure()` to construct a `*client.Client`
and propagate it as `DataSourceData` / `ResourceData`.

#### Tasks

- [x] Define `GarageProviderModel` struct with `Endpoint` and `Token` fields
      (both `types.String`)
- [x] Fill in `Schema()`:
  - `endpoint` — Optional, MarkdownDescription mentions `GARAGE_ENDPOINT`
        fallback
  - `token` — Optional, Sensitive, MarkdownDescription mentions `GARAGE_TOKEN`
        fallback
- [x] Add endpoint URL validator using `terraform-plugin-framework-validators`:
      `stringvalidator.RegexMatches(regexp.MustCompile("^https?://"), "endpoint must be an http(s) URL")`
- [x] Implement `Configure()`:
  - Read plan model
  - Diagnose Unknown-valued attributes early (improves error attribution)
  - Resolve attribute values with env-var fallback
        (`endpoint` → `GARAGE_ENDPOINT`, `token` → `GARAGE_TOKEN`)
  - If still unset for either, append diagnostic error
  - Construct `client.New(endpoint, token)`
  - Hand client to `resp.DataSourceData` and `resp.ResourceData`
- [x] `examples/provider/provider.tf` already matches the final schema for
      docs purposes; final-form rewrite to the minimal `tfplugindocs`
      convention deferred to Phase 10 per OQ #13
- [ ] Manual smoke test: build binary, configure `~/.terraformrc` with
      `dev_overrides`, run `terraform plan` against a throwaway config — expect
      "no resources to configure" output without error (deferred to Phase 6
      when a data source exists to actually exercise the client)

#### Success Criteria

- Provider initializes cleanly against a real Garage admin endpoint (manual)
- Missing both endpoint config and `GARAGE_ENDPOINT` env returns a clear diag
- Missing both token config and `GARAGE_TOKEN` env returns a clear diag
- Token never appears in plan output (sensitive masking works)

---

### Phase 6: `garage_cluster_info` data source

First read-only operation. Smoke-tests the whole stack: provider config →
client wrapper → Garage admin API → state.

#### Tasks

- [x] Create `internal/datasources/clusterinfo/` subpackage (renamed from
      `cluster_info/` to satisfy `staticcheck ST1003` — Go packages should
      not use underscores in names)
- [x] Write `data_source.go`:
  - Implements `datasource.DataSource` and `datasource.DataSourceWithConfigure`
  - `New()` constructor (returns the framework's `datasource.DataSource`)
  - `Metadata()` sets `req.ProviderTypeName + "_cluster_info"`
  - `Schema()` — all attributes Computed. Reflecting the actual
        `openapi.GetClusterStatusResponse` shape: top-level
        `layout_version` (int64) and `nodes` list. Each node carries
        `id`, `addr`, `hostname`, `garage_version`, `is_up`, `draining`,
        `last_seen_secs_ago`, and a nested `role` object with `zone`,
        `capacity`, `tags`. There is no cluster-wide garage_version —
        the IMPL plan's earlier wording was inaccurate; each node
        reports its own version (heterogeneous clusters during upgrades
        are expected). Synthetic `id` attribute set to `layout_version`
        so the data source reads stably under `terraform refresh`
  - `Configure()` asserts `*client.Client` from `req.ProviderData`
  - `Read()` calls `client.GetClusterStatus`, maps response to model
        (pointer-shaped upstream fields → nullable `types.*` values),
        writes state
- [x] Register the data source in `internal/provider/provider.go` `DataSources()`
- [x] Run `just build` — verify provider binary still builds
- [x] Run `just lint` — verify no new violations

#### Success Criteria

- Data source registered, provider compiles
- Schema attributes match the actual Garage API response shape (verified by
  reading the OpenAPI spec)
- Read path is straight-line: no side effects, no retries beyond what's in the
  wrapper

---

### Phase 7: Acceptance test fixture

Reusable testcontainers-go helper. Will be consumed by every acceptance test
from this point forward.

#### Tasks

- [ ] Create `internal/acctest/` package
- [ ] Write `fixture.go`:
  - `type Garage struct { container testcontainers.Container; Endpoint string; AdminToken string }`
  - `func Start(t *testing.T) *Garage` — starts `dxflrs/garage:v<pinned>`
        with `--single-node --default-bucket`
  - Environment: `GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`,
        `GARAGE_DEFAULT_BUCKET` (randomized)
  - Resolve container endpoint via `testcontainers.MappedPort`
  - Admin token: pass `GARAGE_ADMIN_TOKEN=<randomized>` via container env.
        Verify Garage v2.3.0 respects it on startup; if not supported, fall
        back to parsing the container's startup logs for the admin-token line
  - **Per-test lifecycle**: every Test* function calls `acctest.Start(t)` and
        gets its own container. Cold-start cost is ~2-5s; accepted in
        exchange for full state isolation. No `TestMain`
  - `t.Cleanup()` for container termination
- [ ] Write `provider.go`:
  - `var TestAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){...}`
  - `func PreCheck(t *testing.T)` — Docker availability check; `t.Skip` if
        unavailable
  - `func TestAccProviderConfig(g *Garage) string` — helper to render a
        `provider "garage" {}` block pointing at the fixture endpoint
- [ ] Verify the fixture builds (`just build` covers all packages)

#### Success Criteria

- `internal/acctest/` compiles
- A throwaway test using the fixture starts a container and prints its endpoint
- `PreCheck` cleanly skips when Docker is unavailable (manual test: stop
  Docker, run a using test, verify skip)

---

### Phase 8: First acceptance test

Pull Phases 1-7 together. The `garage_cluster_info` acceptance test is the
Phase 1 acceptance gate.

#### Tasks

- [ ] Write `internal/datasources/cluster_info/data_source_test.go`:
  - `TestAccDataSourceClusterInfo` function
  - `PreCheck: func() { acctest.PreCheck(t) }`
  - `ProtoV6ProviderFactories: acctest.TestAccProtoV6ProviderFactories`
  - One test step:
    - Config: `acctest.TestAccProviderConfig(g)` + `data "garage_cluster_info" "this" {}`
    - State check: `garage_version` is non-empty
    - State check: `nodes` list has length ≥ 1
- [ ] Call `t.Parallel()` inside `TestAccDataSourceClusterInfo` — establishes
      the parallelism pattern for Phase 2+ acceptance tests. With one test
      in Phase 1, this is functionally a no-op; serves to surface
      port/memory issues early if they exist
- [ ] Run `just testacc` locally — verify pass
- [ ] Verify the test runs in under 30s (cold container start + read)

#### Success Criteria

- `just testacc` passes against a freshly-pulled `dxflrs/garage:v<pinned>` image
- Test takes < 30s end-to-end
- No flakes on 5 consecutive runs

---

### Phase 9: CI integration

Wire the acceptance test into CI as a matrix job over Terraform versions.

#### Tasks

- [ ] Add `acceptance` job to `.github/workflows/ci.yml`:
  - `runs-on: ubuntu-latest`
  - Matrix: `terraform: ['1.13.*', '1.14.*']`
  - Steps: checkout → setup-go → setup-terraform → setup-just → `just testacc`
  - `timeout-minutes: 15` (well above expected runtime)
- [ ] Open a draft PR; verify the `acceptance` job runs green for both TF
      versions
- [ ] Confirm Docker is available on `ubuntu-latest` (it is by default)
- [ ] Decide whether `acceptance` is `needs:` of any earlier job, or runs in
      parallel with `build` / `lint` / `test-go`

#### Success Criteria

- `acceptance` job green for both TF versions on a draft PR
- Job runtime under 5 minutes per matrix entry (cold image pull + test)
- No cross-job flakes when run alongside other CI jobs

---

### Phase 10: Provider docs generation

Final phase. Fill in `examples/data-sources/garage_cluster_info/` and let
`tfplugindocs` produce the published provider documentation. The CI `generate`
job (already wired) becomes meaningful here.

#### Tasks

- [ ] Rewrite `examples/provider/provider.tf` to the `tfplugindocs` convention:
      a single minimal `provider "garage" {}` block, **no `terraform`
      required_providers block** (`tfplugindocs` synthesizes that section),
      **no `variable` declarations**. Match the HashiCorp scaffolding
      template style. Token attribute omitted; a comment notes that
      `GARAGE_TOKEN` is the recommended supply mechanism. Pure literal
      values only — examples render as static HCL in docs and don't need to
      be runnable
- [ ] Write `examples/data-sources/garage_cluster_info/data-source.tf` —
      minimal example block (just `data "garage_cluster_info" "this" {}` plus
      a single `output` showing how to consume one attribute)
- [ ] Run `just generate`
- [ ] Verify `docs/index.md` reflects the actual provider config schema
- [ ] Verify `docs/data-sources/garage_cluster_info.md` is generated and
      readable
- [ ] Confirm the CI `generate` job passes (no uncommitted drift)
- [ ] Spot-check the schema attribute descriptions for typos / clarity
- [ ] Commit generated docs

#### Success Criteria

- `docs/index.md` and `docs/data-sources/garage_cluster_info.md` exist
- CI `generate` drift check passes
- Generated docs render correctly in the GitHub web viewer

---

## File Changes

| File                                                       | Action | Notes                                             |
|------------------------------------------------------------|--------|---------------------------------------------------|
| `docs/adr/0002-*.md` … `0005-*.md`, `0007-*.md`            | Create | Phase 1 — author from notes                       |
| `go.mod`, `go.sum`                                         | Create | Phase 2                                           |
| `main.go`                                                  | Create | Phase 2 — repo-root entry (HashiCorp convention)  |
| `internal/provider/provider.go`                            | Create | Phase 2, expanded in Phase 5                      |
| `internal/client/openapi/garage-admin-v2.json`             | Create | Phase 3 — vendored, raw 3.1.0 spec                |
| `internal/client/openapi/garage-admin-v2.openapi30.json`   | Create | Phase 3 — downgraded 3.0.3 form (generated)       |
| `internal/client/openapi/oapi-codegen.yaml`                | Create | Phase 3                                           |
| `internal/client/openapi/doc.go`                           | Create | Phase 3                                           |
| `internal/client/openapi/version.go`                       | Create | Phase 3 — `SpecVersion` constant                  |
| `internal/client/openapi/generated.go`                     | Create | Phase 3 — generated (commit policy: OQ #5)        |
| `tools/cmd/spec-downgrade/main.go`                         | Create | Phase 3 — 3.1.0→3.0.3 shim                        |
| `tools/go.mod`, `tools/go.sum`                             | Modify | Phase 3 — add oapi-codegen                        |
| `internal/client/client.go`                                | Create | Phase 4                                           |
| `internal/client/client_test.go`                           | Create | Phase 4                                           |
| `internal/datasources/clusterinfo/data_source.go`          | Create | Phase 6                                           |
| `internal/datasources/clusterinfo/data_source_test.go`     | Create | Phase 8                                           |
| `internal/acctest/fixture.go`, `provider.go`               | Create | Phase 7                                           |
| `examples/data-sources/garage_cluster_info/data-source.tf` | Create | Phase 10                                          |
| `docs/index.md`                                            | Create | Phase 10 — generated by `tfplugindocs`            |
| `docs/data-sources/garage_cluster_info.md`                 | Create | Phase 10 — generated                              |
| `.github/workflows/ci.yml`                                 | Modify | Phase 9 — add `acceptance` matrix job             |
| `justfile`                                                 | Modify | Phase 3 — extend `generate` recipe to run codegen |
| `tools/tools.go`                                           | Modify | Phase 1 — update license header                   |

## Testing Plan

| Layer         | Where                                                   | When     |
|---------------|---------------------------------------------------------|----------|
| Unit          | `internal/client/client_test.go` (retry, error mapping) | Phase 4  |
| Acceptance    | `internal/datasources/cluster_info/data_source_test.go` | Phase 8  |
| CI Acceptance | `acceptance` job in `ci.yml` (matrix over TF versions)  | Phase 9  |
| Docs drift    | `generate` job in `ci.yml` (already present)            | Phase 10 |

## Dependencies

- **ADRs (Phase 1):** ADR-0002, ADR-0003, ADR-0005, ADR-0007 — all gate work
  in later phases
- **External:** Docker daemon for `just testacc` locally and in CI
- **Garage upstream:** OpenAPI spec quality and stability at the pinned version
- **GitHub Actions runners:** `ubuntu-latest` ships Docker pre-installed
  (confirmed)

## Decisions

All thirteen open questions raised at impl-planning time have been resolved
and folded into the phase tasks above. They're recorded here for audit and
to give future-readers the reasoning trail.

1. **[Phase 3] OpenAPI spec source URL.** Vendor from
   `https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json` — Garage HQ's
   auto-published spec, generated by `utoipa` from the upstream Rust source.
   The in-repo location at `git.deuxfleurs.fr/Deuxfleurs/garage` does not
   commit the JSON directly (utoipa generates it at build time). The
   website-hosted file is the canonical artifact.
   *Source: Garage v2.0.0 release notes; spec served live at the URL above.*

2. **[Phases 3, 7] Garage version pin.** Pin to the latest stable v2.x at impl
   time. The currently-served spec reports `info.version` = **`v2.3.0`**,
   which exactly meets the `--single-node --default-bucket` minimum. Capture
   the version in a Go constant so upgrades are explicit.
   *Source: owner call.*

3. **[Phase 7] Admin token discovery.** Inject via `GARAGE_ADMIN_TOKEN` env
   var on container start. Verify Garage v2.3.0 respects this env var during
   Phase 7 implementation; if not, fall back to log-parsing (regex against
   container startup output). Custom image baking is rejected — too coupled.
   *Source: owner call; verification pending in Phase 7.*

4. **[Phase 7] Container lifecycle.** **Per-test**, not per-package. Every
   `Test*` function calls `acctest.Start(t)` and gets its own container with
   `t.Cleanup()` for termination. Cold-start cost (~2-5s per test) is
   accepted in exchange for clean state isolation between tests. Revisit
   if Phase 2+ test count makes total acceptance-test runtime intolerable.
   *Source: owner call.*

5. **[Phase 3] `generated.go` commit policy.** Commit. Matches the
   `tfplugindocs`-generated `docs/` pattern, and the CI `generate` drift-check
   job catches uncommitted regeneration drift.
   *Source: owner call; matches HashiCorp convention.*

6. **[Phase 4] Bearer auth header.** Standard `Authorization: Bearer <token>`,
   verified against the spec's `securitySchemes` section while wiring the
   `RequestEditorFn`. Quick sanity check, not blocking.
   *Source: owner call.*

7. **[Phase 4] Retry backoff parameters.** 3 attempts max, 250ms base, 2x
   multiplier — schedule is 250 → 500 → 1000ms, worst-case total wait ~1.75s.
   *Source: owner call.*

8. **[Phase 4] Retry idempotency policy.** Retry only idempotent verbs
   (`GET`, `HEAD`). `POST` / `PUT` / `DELETE` 5xx responses pass through
   immediately to the caller. Prevents double-side-effects on operations
   that may have succeeded server-side before the connection failed.
   *Source: owner call; standard HTTP semantics.*

9. **[Phase 4] Validation timing in `client.New()`.** **Lazy.** `New()`
   validates only cheap, local invariants (URL parseable, token non-empty);
   no network call. First API request surfaces network/auth errors. This is
   the idiomatic Terraform Plugin Framework pattern — `terraform-provider-tls`
   uses this approach, and the framework docs explicitly leave the choice to
   implementers but recommend keeping `Configure()` cheap so `terraform plan`
   doesn't hit the network unnecessarily. The `garage_cluster_info` data
   source serves as the canonical "ping" users can run to verify their
   provider config end-to-end.
   *Source: researched (terraform-plugin-framework provider docs;
   `terraform-provider-tls` Configure implementation).*

10. **[Phase 5] Endpoint URL validator.** Use
    `terraform-plugin-framework-validators` with
    `stringvalidator.RegexMatches("^https?://", "endpoint must be an http(s) URL")`.
    Catches obviously-malformed endpoints at validate/plan time rather than
    deferring to a network error at apply.
    *Source: owner call. `terraform-plugin-framework-validators` added to
    main `go.mod` in Phase 2.*

11. **[Phase 6] `garage_cluster_info.nodes` schema.** Finalize attribute set
    while reading the vendored spec in Phase 6 (or earlier, during Phase 3
    spec inspection). Expected shape: list of objects with at least `id`,
    `role`, `zone`, `capacity` — confirm against the spec's
    `GetClusterStatus` response definition.
    *Source: owner call (deferred to spec inspection).*

12. **[Phase 8] Test parallelism.** Use `t.Parallel()` from the start. With
    one test in Phase 1, this is a no-op functionally but sets the pattern.
    Watch for OOM / port conflicts when Phase 2+ adds more tests on the same
    runner.
    *Source: owner call.*

13. **[Phase 10] `examples/provider/provider.tf` convention.** Rewrite to the
    minimal `tfplugindocs` convention: a single `provider "garage" {}` block,
    no `terraform { required_providers {} }` block (`tfplugindocs` synthesizes
    it), no `variable` declarations. Token attribute omitted — a comment
    documents `GARAGE_TOKEN` env as the recommended supply mechanism. This
    matches the HashiCorp scaffolding template's example exactly.
    *Source: researched (terraform-provider-scaffolding-framework
    `examples/provider/provider.tf`).*

## Open Questions

None — all initial open questions resolved into the Decisions section above.
Two verifications remain as in-phase tasks rather than upfront blockers:

- **[Phase 3]** Does `oapi-codegen` v2 cleanly handle OpenAPI 3.1.0? The
  Garage spec uses 3.1.0 and historically `oapi-codegen`'s 3.1 support has
  lagged 3.0. First task of Phase 3 surfaces this.
- **[Phase 7]** Does Garage v2.3.0 respect `GARAGE_ADMIN_TOKEN` as an env-var
  source for the admin token on container startup? First task of Phase 7
  verifies; falls back to log-parsing if not.

## References

- [DESIGN-0001: Phase 1 implementation — provider scaffold and OpenAPI client](../design/0001-phase-1-implementation-provider-scaffold-and-openapi-client.md)
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
- [ADR-0001: `garage_key` secret handling](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md)
- [Additional ADRs (notes — ADR-0002, -0003, -0004, -0005, -0006)](../additional.md)
- [terraform-plugin-framework documentation](https://developer.hashicorp.com/terraform/plugin/framework)
- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
- [testcontainers-go](https://golang.testcontainers.org/)
- [Garage admin API v2 documentation](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

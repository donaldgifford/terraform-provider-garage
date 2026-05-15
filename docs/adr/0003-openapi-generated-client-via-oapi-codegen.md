---
id: ADR-0003
title: "OpenAPI-generated client via oapi-codegen"
status: Accepted
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# 0003. OpenAPI-generated client via oapi-codegen

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
  - [Spec sourcing and pinning](#spec-sourcing-and-pinning)
  - [Generation pipeline](#generation-pipeline)
  - [Wrapper boundary](#wrapper-boundary)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Status

Accepted

## Context

The provider talks to Garage's admin v2 HTTP API. Garage publishes an
OpenAPI 3.1.0 specification for the API at
`https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json`, auto-generated
from the upstream Rust source by the `utoipa` crate. The spec is the
authoritative interface description and is regenerated on every Garage
release.

Three options for building the Go HTTP client:

1. **Hand-roll** request/response types and client methods to match the
   spec.
2. **Use an existing third-party SDK** — none currently exists for Go;
   `garage-admin-sdk-js` covers JavaScript but no Go SDK is maintained.
3. **Generate from the spec** with an OpenAPI code generator.

Hand-rolling 60+ endpoints with their request/response types is high
upfront cost and ongoing maintenance burden every time Garage publishes
new API methods. A generated client mechanically tracks the spec and
makes upstream API changes visible as a single regenerate-and-commit
operation in CI.

## Decision

Generate the Go HTTP client from a vendored copy of Garage's OpenAPI 3.1.0
spec using `github.com/oapi-codegen/oapi-codegen/v2`.

### Spec sourcing and pinning

- The spec is **vendored** at `internal/client/openapi/garage-admin-v2.json`.
  The canonical upstream URL is
  `https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json`.
- The spec's `info.version` is captured in a Go constant
  (`internal/client/openapi/version.go` → `const SpecVersion = "v2.3.0"`
  at initial vendoring time).
- Upgrade procedure documented in `internal/client/openapi/doc.go`:
  re-fetch the URL, bump the constant, regenerate, commit the diff.
- Pinning to a specific Garage version (rather than always tracking
  upstream) lets provider releases be tested against a known API
  surface; bumps are deliberate operations.

### Generation pipeline

- Tool: `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen`, pinned
  in the build-only `tools/go.mod` module (same pattern as `tfplugindocs`).
- Generation is driven by a `//go:generate` directive in
  `internal/client/openapi/doc.go`:

  ```go
  //go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml garage-admin-v2.json
  ```

- Configuration in `internal/client/openapi/oapi-codegen.yaml`:

  ```yaml
  package: openapi
  output: generated.go
  generate:
    models: true
    client: true
    embedded-spec: false
  ```

- `just generate` runs both client codegen and `tfplugindocs`:

  ```bash
  go generate ./internal/client/openapi/... && cd tools && go generate ./...
  ```

- The generated file `internal/client/openapi/generated.go` is **committed
  to the repo**. CI runs `just generate` and fails on any uncommitted diff
  — the same drift-check pattern `tfplugindocs`-generated docs use. This
  catches manual edits to generated code and forgotten regenerations.

### Wrapper boundary

The generated client is **internal**. Resource and data-source code in
`internal/datasources/` and `internal/resources/` never imports the
`openapi` package directly. A hand-written thin wrapper in
`internal/client/client.go` exposes the surface the provider needs:

- Bearer-auth injection via `RequestEditorFn`
- Retry-on-5xx with bounded exponential backoff (250ms base, 2x, 3
  attempts) — GET/HEAD only, to avoid double-side-effects on POST/PUT/DELETE
- Typed error sentinels (`ErrNotFound`, `ErrUnauthorized`, etc.) mapped
  from HTTP status codes
- `tflog` request/response tracing
- One typed method per provider operation (`GetClusterStatus`,
  `CreateBucket`, …), added incrementally as each phase of the RFC
  arrives

The wrapper boundary means the rest of the provider doesn't see
`oapi-codegen` types and stays insulated from generator-output churn
when the spec is regenerated.

## Consequences

### Positive

- **Spec drift becomes mechanical.** When Garage adds an API method, we
  re-fetch the spec, regenerate, and either expose it through the wrapper
  or leave it unused. No hand-written boilerplate to maintain.
- **Type safety from the upstream contract.** Request and response shapes
  are derived from the spec, so the Go compiler catches mismatches as the
  spec evolves.
- **Familiar pattern for contributors.** Matches the
  `wiz-sdk-gen` / `wiz-go-sdk` style we use elsewhere; nothing exotic.
- **The wrapper boundary makes the provider testable.** Higher-level code
  takes `*client.Client`, which can be substituted in unit tests with an
  `httptest`-backed stub.

### Negative

- **Generated code can be ugly.** `oapi-codegen` output is verbose and
  occasionally surprising (e.g. union types in OpenAPI 3.1 become
  awkward Go interfaces). The wrapper insulates downstream code from
  this.
- **OpenAPI 3.1.0 support in `oapi-codegen` v2 is newer than its 3.0
  support.** The Garage spec uses 3.1.0; the first task of IMPL-0001
  Phase 3 verifies generation works cleanly. If it doesn't, the
  fallback is either a 3.0-downgrade shim or evaluating an alternative
  generator — both add cost.
- **Two `go.mod` modules to manage.** The provider's main module and the
  `tools/` build-only module both need updating when bumping
  `oapi-codegen` major versions. Documented in `tools/tools.go`.

### Neutral

- **Generated file is committed.** `internal/client/openapi/generated.go`
  lives in git. Pro: easier to review API changes in PRs; CI doesn't
  need codegen to compile. Con: large diffs on regeneration. Matches
  `tfplugindocs` convention.
- **The Garage spec is auto-generated upstream.** We're vendoring a
  generated artifact (utoipa output), not a hand-written spec. If
  Garage's `utoipa` annotations have bugs, they propagate to our client.
  Mitigation: pin to a specific Garage release where the spec is known
  to work; bump deliberately.

## Alternatives Considered

- **Hand-rolled HTTP client.** Rejected — 60+ endpoints, ongoing
  maintenance burden, no offsetting benefit over a generated client when
  the spec exists and is well-maintained.
- **`openapitools/openapi-generator-cli`.** Java-based, heavier dependency
  to install in CI, and the Go output is less idiomatic than
  `oapi-codegen`'s (generated method signatures tend to be more verbose,
  package layout less Go-style). Rejected.
- **`swaggo/swag` or other server-side Go generators.** These are
  optimized for generating *server* code from Go annotations, not
  *client* code from a vendored spec. Wrong tool for this job.
- **Hosted spec fetch at generation time** (no vendored JSON). Rejected
  because it makes builds non-reproducible and breaks offline / air-gapped
  CI environments.

## References

- [RFC-0001 §Architecture (client section)](../rfc/0001-garage-terraformopentofu-provider.md)
- [ADR-0002](0002-use-terraform-plugin-framework-over-sdkv2.md) — framework
  context that pairs with this decision
- [`docs/additional.md` ADR-0003 notes](../additional.md) — original
  decision notes from which this ADR was formalized
- [IMPL-0001 §Decisions #1, #2, #5, #6, #7, #8](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md#decisions)
  — operational follow-through (spec URL, version pin, commit policy,
  bearer auth, retry policy)
- [oapi-codegen documentation](https://github.com/oapi-codegen/oapi-codegen)
- [Garage admin v2 API documentation](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [Garage v2.0.0 release notes](https://garagehq.deuxfleurs.fr/blog/2025-06-garage-v2/)
  — notes that the spec is `utoipa`-generated

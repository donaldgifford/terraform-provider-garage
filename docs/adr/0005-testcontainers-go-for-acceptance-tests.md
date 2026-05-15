---
id: ADR-0005
title: "testcontainers-go for acceptance tests"
status: Accepted
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# 0005. testcontainers-go for acceptance tests

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
  - [Image pinning](#image-pinning)
  - [Fixture API and lifecycle](#fixture-api-and-lifecycle)
  - [Admin token discovery](#admin-token-discovery)
  - [CI integration](#ci-integration)
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

Provider acceptance tests must exercise real Garage admin API behavior:
auth handshakes, error response shapes, multi-step resource lifecycles,
and version-specific quirks. A mock HTTP server can pass tests that
production then breaks on — masking failures is worse than catching them.

The candidate environments are:

1. **Shared dev Garage cluster.** Cheapest setup but creates a global
   state pollution problem: tests can't safely create/destroy buckets
   without collisions, parallel runs are fragile, and CI runners need
   credentialed access to a long-lived cluster.
2. **`docker-compose` fixture.** Brings up Garage per test run via
   compose. Works, but lifecycle management is manual (`docker compose
   up/down` glue inside test code or test scripts) and the result is
   less idiomatic in Go test harnesses.
3. **`testcontainers-go`.** Provides a Go API for managing container
   lifecycles inside the test process. Tests describe their needs as
   typed Go structs; the library handles pull, start, port resolution,
   environment injection, and cleanup.
4. **Long-running CI service container** (GitHub Actions `services:`).
   Boots Garage once per job; tests share state. Same isolation problem
   as the shared dev cluster, scoped to one workflow run.

Option (3) is the de-facto pattern across Go integration test suites for
exactly this shape of problem.

## Decision

Use `github.com/testcontainers/testcontainers-go` to manage Garage
containers inside acceptance tests. A helper package `internal/acctest/`
wraps the library and exposes a small Go API (`acctest.Start(t)`,
`acctest.TestAccProtoV6ProviderFactories`, `acctest.PreCheck(t)`).

### Image pinning

- Image: `dxflrs/garage:v<pinned>` from Docker Hub.
- Minimum version: **v2.3.0** (the release that introduced
  `--single-node --default-bucket` flags, which collapse the cluster
  bootstrap dance into a single-process invocation).
- Concrete version: the latest stable v2.x at impl time, pinned
  explicitly in the fixture as a `const`. Bumps are deliberate
  edits, not auto-tracking.
- Long-term: matrix-test against the last 2-3 Garage versions to catch
  regressions early — out of scope for Phase 1, deferred to a later
  RFC phase.

### Fixture API and lifecycle

```go
// internal/acctest/fixture.go
type Garage struct {
    container  testcontainers.Container
    Endpoint   string
    AdminToken string
}

func Start(t *testing.T) *Garage { /* … */ }
```

- **Per-test lifecycle.** Every `Test*` function calls `acctest.Start(t)`
  and gets its own container, with `t.Cleanup()` registering termination.
  Cold start is ~2-5s per test; accepted in exchange for full state
  isolation between tests.
- No `TestMain` shared-fixture model. Per-package state-sharing was
  considered but rejected for Phase 1 (see
  [IMPL-0001 §Decisions #4](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md#decisions));
  if Phase 2+ test counts make total runtime unacceptable, revisit.
- Tests opt into `t.Parallel()` from the start to surface
  port/memory issues early.

### Admin token discovery

Garage v2's `--single-node --default-bucket` mode generates an admin
token at startup. The fixture injects `GARAGE_ADMIN_TOKEN=<randomized>`
into the container env. If Garage v2.3.0 doesn't honor that env var
(verification is the first task of IMPL-0001 Phase 7), the fallback is
log-parsing: scrape the admin-token line from the container's stdout
on startup.

The randomized access/secret/bucket triple is similarly injected:
`GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`,
`GARAGE_DEFAULT_BUCKET`.

### CI integration

GitHub Actions `ubuntu-latest` runners ship with Docker pre-installed,
so no setup-docker action is needed. The acceptance test job is a
matrix over Terraform versions (`1.13.*`, `1.14.*` initially) and runs
`just testacc`.

For local dev, `acctest.PreCheck(t)` calls `testcontainers.NewDockerClient`
and `t.Skip`s with a clear message if Docker isn't reachable. Developers
without Docker can still run unit tests via `just test`.

## Consequences

### Positive

- **Per-test state isolation.** Tests can do anything to "their" Garage
  instance — create buckets named the same way, run destructive
  operations, etc. — without coordinating with other tests.
- **Parallel-safe.** `t.Parallel()` works out of the box because each
  test owns its own container.
- **Self-contained CI.** No infrastructure dependencies beyond Docker;
  no shared dev cluster to provision, credential, or babysit.
- **Reproducible.** Pinned image tag means the test environment is the
  same on every commit; bumps are explicit.
- **Familiar pattern.** Matches the `tftest` / sneakystack style used
  for Terraform module testing elsewhere; engineers transferring
  between projects don't have to learn a new harness.

### Negative

- **Cold-start cost.** Each test pays ~2-5s for image start. With one
  test in Phase 1 this is invisible; with hundreds of tests in v0.1+
  it becomes significant. Mitigations to evaluate later: image
  pre-pull on runner setup, reuse containers across tests in the same
  package, smaller Garage image variants.
- **Docker dependency for local dev.** Developers without Docker can't
  run acceptance tests locally. `PreCheck` handles this with a clean
  skip but it does reduce coverage in dev environments.
- **macOS Docker Desktop overhead.** Docker on macOS runs in a VM, so
  testcontainers' container-per-test pattern is slower than on Linux.
  Documented in `CONTRIBUTING.md` (to be authored). Less of an issue
  for CI, which is Linux.

### Neutral

- **Image source is Docker Hub.** Public image, no auth required.
  Migrate to GHCR mirror if Docker Hub rate limits become a problem.
- **`testcontainers-go` is a runtime dependency in `go.mod` (test scope
  via Go's `_test.go` convention).** It pulls a non-trivial dependency
  graph (containerd-related packages). Worth it for the API ergonomics;
  documented as a Phase 1 direct dep in IMPL-0001.

## Alternatives Considered

- **Mocked HTTP server (`httptest`).** Considered as a fast-iteration
  baseline. Rejected for acceptance tests — too easy to write a mock
  that passes tests but doesn't match production behavior. Mocks are
  appropriate for unit-testing the `internal/client/` wrapper's retry
  and error-mapping logic (see ADR-0003), but not for full
  resource/data-source acceptance tests.
- **`docker-compose` fixture.** Same effect, more manual glue,
  awkward to start/stop from inside `*testing.T`. Rejected.
- **Shared dev cluster.** Flaky, slow due to network, state pollution
  across tests, needs CI to hold persistent credentials. Rejected.
- **GitHub Actions service container** (`services:` in workflow YAML).
  Lifecycle is workflow-scoped, not test-scoped — same state-pollution
  problem as a shared cluster. Plus it only works in CI, not for local
  dev. Rejected.

## References

- [RFC-0001 §Testing](../rfc/0001-garage-terraformopentofu-provider.md)
- [`docs/additional.md` ADR-0005 notes](../additional.md) — original
  decision notes from which this ADR was formalized
- [IMPL-0001 §Decisions #3, #4, #12](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md#decisions)
  — admin token, lifecycle, parallelism
- [testcontainers-go documentation](https://golang.testcontainers.org/)
- [Garage v2.3.0 release notes (single-node mode)](https://garagehq.deuxfleurs.fr/blog/)
- [`dxflrs/garage` Docker Hub image](https://hub.docker.com/r/dxflrs/garage)

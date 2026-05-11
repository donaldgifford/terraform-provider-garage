# Additional ADRs to draft

These ADRs are referenced from
[RFC-0001](rfc/0001-garage-terraform-opentofu-provider.md) but deferred for
later authoring. Each entry captures enough context, decision direction, and key
alternatives that the ADR can be drafted from these notes when needed.

When ready to author, use:

```bash
docz create adr "<title>"
```

The numbering shown here is the suggested order; `docz create` will auto-assign
the next available number. Numbering picks up from ADR-0002 since ADR-0001 is
the `garage_key` secret handling decision.

---

## ADR-0002: terraform-plugin-framework over SDKv2

**Status:** Proposed (notes only)

### Context

HashiCorp maintains two plugin systems: the legacy `terraform-plugin-sdk` v2 and
the modern `terraform-plugin-framework`. The wire-level gRPC plugin protocol is
the same; the Go developer-facing API differs significantly. OpenTofu uses the
same plugin protocol and supports both libraries.

### Decision

Use `terraform-plugin-framework` exclusively. No `terraform-plugin-mux` to
combine with SDKv2.

### Key reasons

- Write-only arguments and ephemeral resource integration require the framework
  — ADR-0001 depends on this
- Explicit `null` / `unknown` / `known` value semantics — clearer than SDKv2's
  `interface{}` handling
- Stronger schema-level validation primitives via
  `terraform-plugin-framework-validators`
- The framework is the actively-developed path; SDKv2 is in maintenance mode
- OpenTofu's "creating providers" docs explicitly recommend the framework

### Alternatives

- **SDKv2:** legacy; rejected for the reasons above
- **Plugin mux (framework + SDKv2):** unnecessary complexity for a greenfield
  provider

### Notes

- Verify OpenTofu's framework support is at full parity (gRPC protocol is
  identical; should be a non-issue but worth confirming with a `tofu` matrix
  test in CI)
- Cite the relevant HashiCorp blog post announcing write-only arguments in TF
  1.11 and the corresponding OpenTofu 1.11 release notes

---

## ADR-0003: OpenAPI-generated client via oapi-codegen

**Status:** Proposed (notes only)

### Context

Garage publishes an OpenAPI v3 spec for the admin v2 API
(`garage-admin-v2.json`, visible in the `eyebrowkang/garage-admin-console`
source and other community tooling). Three options for building the HTTP client:
hand-roll, use a third-party SDK if one exists, or generate from the spec.

### Decision

Generate the client from the vendored Garage OpenAPI spec using `oapi-codegen`.
Vendor the spec into `internal/client/openapi/garage-admin-v2.json` and pin it.
Drive generation via a `//go:generate` directive checked in CI.

### Key reasons

- Matches the AWS SDK v2-style generator pattern used in `wiz-sdk-gen` /
  `wiz-go-sdk`
- Garage maintains the spec — drift between client and server is mechanically
  detectable
- Avoids hand-written request/response boilerplate
- Easy to update on Garage API revisions (re-vendor spec, regenerate, commit
  diff)

### Alternatives

- **openapi-generator-cli:** Java-based, heavier dependency, less Go-idiomatic
  output
- **Hand-rolled HTTP client:** high maintenance cost, no obvious benefit when a
  spec exists
- **swag or other Go-specific generators:** less mature for client-side codegen

### Notes

- Wrap the generated client in a thin layer (`internal/client/client.go`) for
  typed Garage errors, retries on 5xx, optional rate-limit handling
- Pin Garage spec version explicitly; document the upgrade procedure in
  `CONTRIBUTING.md`
- CI check: regenerate from vendored spec and verify no diff (catches manual
  edits to generated code)
- Consider whether to generate types only versus types + client; types-only
  gives more flexibility but more code to write

---

## ADR-0004: Dual registry publishing (Terraform Registry + OpenTofu Registry)

**Status:** Proposed (notes only)

### Context

Two separate provider registries with separate submission flows:

- **Terraform Registry** (`registry.terraform.io`): GitHub releases with
  GPG-signed `_SHA256SUMS`; a one-time provider registration linking the GPG
  key; subsequent releases auto-detected from tagged GitHub releases
- **OpenTofu Registry** (`search.opentofu.org`): submission-based — open a PR
  against the `opentofu/registry` repo with a JSON entry pointing at the GitHub
  release

### Decision

Publish to both registries. A single GoReleaser config produces signed artifacts
compatible with both. Separate GitHub Actions workflows handle each registry's
submission flow.

### Key reasons

- Many users run `tofu` exclusively; serving them from the Terraform Registry
  creates BSL ambiguity
- Cost of dual publishing is low if automated
- Maintains the widest possible reach for the provider

### Mechanics

- GoReleaser builds for the standard provider OS/arch matrix
- GPG signing key stored in 1Password, exposed to GitHub Actions via repository
  secrets
- Action 1 (Terraform Registry): tag push → GitHub release → registry
  auto-detects
- Action 2 (OpenTofu Registry): tag push → action opens a PR against
  `opentofu/registry` with the version JSON

### Alternatives

- **TF Registry only:** excludes pure-OpenTofu users
- **OpenTofu Registry only:** excludes Terraform users; smaller current install
  base
- **Self-hosted registry only:** overkill for an open-source provider; loses
  discoverability

### Notes

- Register the provider on the Terraform Registry early — the one-time provider
  registration step has human-review latency
- For OpenTofu, the submission PR is typically reviewed quickly but is not fully
  automated on their side
- Document the manual escape hatch in `RELEASING.md` for both flows in case
  automation breaks
- Decide on a release cadence (probably tag-driven, no scheduled releases)

---

## ADR-0005: Testcontainers-Go for acceptance tests

**Status:** Proposed (notes only)

### Context

Provider acceptance tests need a real Garage instance — mock servers don't catch
API-level behavior, error format mismatches, or version-specific quirks.
Options: shared dev cluster, docker-compose fixture, testcontainers, or a
long-running CI service container.

### Decision

Use `testcontainers-go` with the `dxflrs/garage:v2.x` image, configured via
`GARAGE_DEFAULT_ACCESS_KEY` / `GARAGE_DEFAULT_SECRET_KEY` /
`GARAGE_DEFAULT_BUCKET` environment variables plus the
`--single-node --default-bucket` flags introduced in Garage v2.3.0.

### Key reasons

- Per-test isolation; no cross-test state pollution
- Parallel test execution
- Matches patterns used in `tftest` / sneakystack
- Self-contained CI — no infrastructure dependencies beyond Docker
- Pinning the Garage image version gives reproducible tests

### Alternatives

- **docker-compose fixture:** works but requires manual lifecycle management and
  is less idiomatic in Go test harnesses
- **Shared dev Garage cluster:** flaky, slow due to network, hard to
  parallelize, state pollution
- **Mock HTTP server:** doesn't catch real Garage behavior (e.g. error format
  mismatches, response shape quirks)

### Notes

- Pin Garage image to a specific patch version; bump deliberately as part of
  provider releases
- Consider matrix-testing against the last 2–3 Garage versions to catch
  regressions early
- Helper package `internal/acctest/` to wrap the container fixture and provide a
  pre-configured client to tests
- The container's admin token needs to be discoverable by the test harness —
  either via the default-bucket env vars or by reading the container logs

---

## ADR-0006: Resource boundaries — separate bucket_key and bucket_alias resources

**Status:** Proposed (notes only)

### Context

Bucket ↔ key permissions are an M:N relationship. Bucket aliases come in two
flavors: global (cluster-scoped) and local (per-key). Several modeling options:

1. All inline on `garage_bucket` (single resource, complex schema, hard M:N)
2. Inline on `garage_key` (permissions tied to key lifecycle)
3. Separate edge resources

### Decision

- **`garage_bucket`** — bucket lifecycle, global aliases (inline as a list
  attribute), quotas
- **`garage_key`** — key lifecycle, secret handling (ADR-0001)
- **`garage_bucket_key`** — the read/write/owner permission edge between a
  single bucket and a single key
- **`garage_bucket_alias`** — local (per-key) bucket aliases

### Key reasons

- M:N permissions naturally model as their own resource — this is the canonical
  AWS-IAM-style pattern (compare `aws_iam_role_policy_attachment`)
- Local aliases have different scoping rules than global aliases (per-key vs
  cluster-wide)
- Independent lifecycles: rotating a key shouldn't disturb a bucket's other
  permissions
- Mirrors patterns in `d0ugal/terraform-provider-garage` and
  `jkossis/terraform-provider-garage`, easing migration from those providers

### Alternatives

- **All-inline on `garage_bucket`:** simpler at first glance, but impossible to
  express "add this key's access without modifying the bucket resource"; doesn't
  model M:N well
- **Permissions inline on `garage_key`:** ties permission lifecycle to key
  lifecycle; same M:N problem in reverse
- **Hybrid (some inline, some separate):** inconsistent and harder to document

### Notes

- Global aliases inline on `garage_bucket` is a debatable call. They could be a
  separate `garage_bucket_global_alias` resource for full symmetry. The decision
  is "inline" because adding/removing the last global alias and the bucket
  itself are usually a single semantic operation; making them a separate
  resource forces users to manage that ordering explicitly with no real benefit.
- Document the M:N modeling clearly with examples — users coming from
  object-storage operators with monolithic resources may expect inline
  permissions
- Consider whether `garage_bucket_key` should accept lists of buckets or lists
  of keys for ergonomics, or whether the canonical single-edge form is enough.
  Decision deferred to the design phase.

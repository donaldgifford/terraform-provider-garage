---
id: RFC-0001
title: "Garage Terraform/OpenTofu provider"
status: Draft
author: Donald Gifford
created: 2026-05-11
---

<!-- markdownlint-disable-file MD025 MD041 -->

# RFC 0001: Garage Terraform/OpenTofu provider

**Status:** Draft **Author:** Donald Gifford **Date:** 2026-05-11

<!--toc:start-->

- [RFC 0001: Garage Terraform/OpenTofu provider](#rfc-0001-garage-terraformopentofu-provider)
  - [Summary](#summary)
  - [Problem Statement](#problem-statement)
  - [Proposed Solution](#proposed-solution)
    - [v0.1 surface](#v01-surface)
    - [v0.2+ scope (deferred)](#v02-scope-deferred)
  - [Design](#design)
    - [Architecture](#architecture)
    - [Authentication](#authentication)
    - [Key design decision: `garage_key` secret handling](#key-design-decision-garagekey-secret-handling)
    - [Testing](#testing)
    - [Publishing](#publishing)
  - [Alternatives Considered](#alternatives-considered)
  - [Implementation Phases](#implementation-phases)
    - [Phase 1: Scaffolding and client generation](#phase-1-scaffolding-and-client-generation)
    - [Phase 2: `garage_bucket` resource](#phase-2-garagebucket-resource)
    - [Phase 3: `garage_key` resource (the load-bearing piece)](#phase-3-garagekey-resource-the-load-bearing-piece)
    - [Phase 4: `garage_bucket_key` resource](#phase-4-garagebucketkey-resource)
    - [Phase 5: `garage_bucket_alias` resource](#phase-5-garagebucketalias-resource)
    - [Phase 6: Data sources](#phase-6-data-sources)
    - [Phase 7: Docs and publishing](#phase-7-docs-and-publishing)
    - [Phase 8 (post-v0.1): Websites, lifecycle, admin tokens](#phase-8-post-v01-websites-lifecycle-admin-tokens)
  - [Risks and Mitigations](#risks-and-mitigations)
  - [Success Criteria](#success-criteria)
  - [References](#references)
  <!--toc:end-->

## Summary

Build a first-party Terraform/OpenTofu provider for the Garage S3 admin API v2,
using `terraform-plugin-framework` with an OpenAPI-generated client. The v0.1
scope covers bucket lifecycle, access key lifecycle, bucket-key permission
edges, and bucket aliases. Published to both the Terraform Registry and the
OpenTofu Registry.

## Problem Statement

Garage is the self-hosted S3 implementation underpinning the homelab object
storage layer (observability bucket store, backup target, Renovate cache) and is
being evaluated as a backend for self-hosted module/provider registries.
Provisioning Garage resources today happens through one of:

- The `garage` CLI executed manually or via shell scripts
- The `garage json-api` subcommand wrapped in ad-hoc tooling
- Community Terraform providers (`darkmukke/garage`,
  `d0ugal/terraform-provider-garage`, `jkossis/terraform-provider-garage`,
  `arsolitt/terraform-provider-garagehq`)

None of these match the operational model we want:

1. The community providers are inconsistent in framework choice (mix of SDKv2
   and plugin-framework), admin API version targeting (some v1, some v2), and
   resource modeling. None has a clear maintenance lead.
2. None implement write-only attribute support for access secrets — the secret
   always lands in state.
3. CLI/script-based provisioning is imperative and difficult to integrate
   cleanly with our Terragrunt + ArgoCD GitOps workflow.

We want declarative provisioning of Garage buckets and credentials with:

- Modern `terraform-plugin-framework` for current and future primitives
  (write-only, ephemeral)
- Dual Terraform + OpenTofu compatibility, validated in CI
- An optional ephemeral/write-only path so credentials can be routed to a secret
  store (1Password) without touching Terraform state
- An OpenAPI-generated client (matching the `wiz-sdk-gen` / `wiz-go-sdk`
  pattern)
- testcontainers-based acceptance tests (matching `tftest` / sneakystack
  patterns)

## Proposed Solution

A new provider, `terraform-provider-garage`, built fresh on
`terraform-plugin-framework`. The provider talks to the Garage admin API v2 via
a client generated from Garage's published OpenAPI spec (`garage-admin-v2.json`)
using `oapi-codegen`.

### v0.1 surface

**Resources:**

| Name                  | Purpose                                                                              |
| --------------------- | ------------------------------------------------------------------------------------ |
| `garage_bucket`       | Bucket lifecycle, global aliases, quotas                                             |
| `garage_key`          | S3 access key lifecycle with explicit `secret_source` mode — see ADR-0001            |
| `garage_bucket_key`   | Permission edge: which keys hold which read/write/owner permissions on which buckets |
| `garage_bucket_alias` | Local (per-key) bucket aliases                                                       |

**Data sources:**

| Name                  | Purpose                                                                  |
| --------------------- | ------------------------------------------------------------------------ |
| `garage_bucket`       | Look up an existing bucket by ID or global alias                         |
| `garage_key`          | Look up an existing key by ID or name                                    |
| `garage_cluster_info` | Cluster health and metadata; useful for validation and conditional logic |

**Provider config:**

```hcl
provider "garage" {
  endpoint = "https://garage.example.com:3903"  # or GARAGE_ENDPOINT
  token    = var.garage_admin_token             # or GARAGE_TOKEN
}
```

### v0.2+ scope (deferred)

- Website hosting configuration on `garage_bucket` (plus the `/check` domain
  verification data source)
- Lifecycle rules on `garage_bucket`
- `garage_admin_token` resource for bootstrapping scoped tokens
- K2V API support if a concrete use case materializes

## Design

### Architecture

```
internal/
  client/
    openapi/          # Vendored garage-admin-v2.json
    generated.go      # oapi-codegen output, regenerated via go generate
    client.go         # Thin wrapper: typed errors, retries, request signing
  provider/
    provider.go       # Provider definition
    config.go         # Provider-level config schema
  resources/
    bucket/
    key/
    bucket_key/
    bucket_alias/
  datasources/
    bucket/
    key/
    cluster_info/
main.go
```

The thin client wrapper translates Garage HTTP error responses into typed Go
errors, retries 5xx with exponential backoff, and provides a clean seam for
future concerns (rate limiting, metrics).

### Authentication

Bearer token via the admin API. Configured at the provider block or via
`GARAGE_TOKEN`. The token's scope (which API endpoints it can call) is managed
outside the provider via `garage admin-token create --scope ...`. The provider
does not manage admin tokens themselves in v0.1.

### Key design decision: `garage_key` secret handling

The load-bearing call. Documented in detail in
[ADR-0001](../adr/0001-garage-key-secret-handling.md). Summary: a required
`secret_source` attribute with values `"garage"` (Garage auto-generates, secret
lands in state with a persistent warning) or `"external"` (user provides via
write-only attribute, no state persistence). No default — the choice is forced.

### Testing

Acceptance tests use `testcontainers-go` to spin up `dxflrs/garage:v2.x` per
test, configured via the `--single-node --default-bucket` flags introduced in
Garage v2.3.0. Details deferred to ADR-0005.

### Publishing

Dual registry: Terraform Registry and OpenTofu Registry. Single GoReleaser
configuration produces signed artifacts; separate GitHub Actions handle
submission to each registry. Details deferred to ADR-0004.

## Alternatives Considered

**Fork an existing community provider.** Rejected. None of the four candidates
use the patterns we want (modern framework, OpenAPI codegen, write-only support,
testcontainers tests). Forking would mean inheriting code we'd rewrite anyway or
contributing back to a project with no clear maintainer. Starting fresh with
attribution to the prior art is cleaner.

**`terraform-plugin-sdk` v2.** Rejected. The legacy SDK doesn't support
write-only attributes or ephemeral values, which are central to the
`secret_source = "external"` workflow. SDKv2 is in maintenance mode. See
ADR-0002.

**Hand-written HTTP client.** Rejected. Garage publishes a well-maintained
OpenAPI spec. Generating the client matches existing internal patterns and
avoids hand-rolled boilerplate. See ADR-0003.

**Contribute to community providers instead of building first-party.**
Considered but rejected for v0.1. The secret-handling design (ADR-0001) is a
meaningful departure from what the existing providers do; pushing such a change
into a project we don't own is slow and risky. After v0.1 stabilizes, we can
re-evaluate offering a maintenance handoff or merger.

## Implementation Phases

### Phase 1: Scaffolding and client generation

- Initialize repo from `hashicorp/terraform-provider-scaffolding-framework`
- Vendor Garage admin API OpenAPI spec
- Configure `go generate` for `oapi-codegen`
- Implement provider block, auth, and a minimal `garage_cluster_info` data
  source as a smoke test
- testcontainers-go fixture for acceptance tests

### Phase 2: `garage_bucket` resource

- Create/read/update/delete
- Global aliases inline
- Quotas (`max_size`, `max_objects`)
- Acceptance tests covering full lifecycle

### Phase 3: `garage_key` resource (the load-bearing piece)

- Both `secret_source` modes per ADR-0001
- Cross-attribute validators for mode-specific schema
- `ValidateConfig` warning for `secret_source = "garage"`
- Acceptance tests for both modes, including the write-only path with
  `ephemeral "random_password"`

### Phase 4: `garage_bucket_key` resource

- Permission edge with read/write/owner
- Idempotent allow/deny logic
- Acceptance tests covering permission state transitions

### Phase 5: `garage_bucket_alias` resource

- Local (per-key) aliases
- Acceptance tests

### Phase 6: Data sources

- `garage_bucket`, `garage_key`, `garage_cluster_info` filled in

### Phase 7: Docs and publishing

- `tfplugindocs` integration for auto-generated provider docs
- `examples/` directory: Loki/Tempo observability stack, basic single-bucket
  setup
- GoReleaser with GPG signing
- GitHub Actions for both registries
- README with quickstart, version compatibility matrix, and the two-mode
  `garage_key` walkthrough

### Phase 8 (post-v0.1): Websites, lifecycle, admin tokens

Tracked separately.

## Risks and Mitigations

| Risk                                                           | Impact                                  | Likelihood                                    | Mitigation                                                                          |
| -------------------------------------------------------------- | --------------------------------------- | --------------------------------------------- | ----------------------------------------------------------------------------------- |
| Garage admin API breaking changes                              | Forces provider rework mid-cycle        | Low — Garage commits to no breaks within v2.x | Pin OpenAPI spec version; integration tests against pinned image                    |
| Users on TF < 1.11 / OpenTofu < 1.11 can't use `external` mode | Confusion in mixed-version environments | Medium                                        | Document version requirement; `garage` mode works on older versions                 |
| Dual registry publishing operational overhead                  | Slow releases                           | Low (if automated)                            | GoReleaser + GitHub Actions; document the manual escape hatch in `RELEASING.md`     |
| Generated client drift from spec                               | Subtle bugs                             | Low                                           | Regenerate on every release; CI check that generated code matches checked-in output |
| A community provider becomes well-maintained mid-development   | Effort duplication                      | Medium                                        | Periodically re-evaluate; offer merger/handoff once we stabilize                    |

## Success Criteria

- All v0.1 resources implemented with acceptance tests passing on both
  `terraform` and `tofu` CLIs (matrix test in CI)
- Published to both Terraform Registry and OpenTofu Registry with auto-generated
  docs
- Working example provisioning the homelab observability stack (Loki, Tempo,
  Mimir buckets and keys)
- `secret_source = "external"` path verified end-to-end with the `onepassword`
  provider in an example
- No `secret_access_key` value appears in state files for `external` mode —
  verified by an explicit acceptance test that inspects the state JSON after
  apply

## References

- [ADR-0001: garage_key secret handling — explicit secret_source modes](../adr/0001-garage-key-secret-handling.md)
- [Additional ADRs (notes for later authoring)](../additional.md)
- [Garage admin API v2 documentation](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [terraform-plugin-framework documentation](https://developer.hashicorp.com/terraform/plugin/framework)
- [OpenTofu provider development docs](https://opentofu.org/docs/internals/provider-protocol/)
- Existing community providers (prior art):
  - `darkmukke/terraform-provider-garage`
  - `d0ugal/terraform-provider-garage`
  - `jkossis/terraform-provider-garage`
  - `arsolitt/terraform-provider-garagehq`

---
id: ADR-0002
title: "Use terraform-plugin-framework over SDKv2"
status: Accepted
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# 0002. Use terraform-plugin-framework over SDKv2

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
  - [Library pinning and depguard enforcement](#library-pinning-and-depguard-enforcement)
  - [Validation strategy](#validation-strategy)
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

HashiCorp ships two plugin systems for writing Terraform providers:

- `terraform-plugin-sdk/v2` ("SDKv2") — the legacy library, in active
  maintenance mode but no longer the preferred path for new providers.
- `terraform-plugin-framework` ("framework") — the modern library, where
  feature development happens.

The on-wire gRPC plugin protocol (protocol v6) is the same for both, and
OpenTofu supports both libraries transparently because it speaks the
same protocol. The choice is therefore an authoring-time decision about
Go API ergonomics and feature surface, not a runtime-compatibility
decision.

Two features on the v0.1 roadmap (per [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md))
are framework-only:

1. **Write-only arguments** (`WriteOnly: true`, Terraform 1.11+ /
   OpenTofu 1.11+) are required to support
   [ADR-0001](0001-garagekey-secret-handling-explicit-secretsource-modes.md)'s
   `secret_source = "external"` path on `garage_key`. SDKv2 has no
   write-only support and won't gain it.
2. **Ephemeral resource integration** — also framework-only — is a natural
   companion to the write-only flow, since the canonical pattern is
   `ephemeral.random_password.secret.value` feeding into a write-only
   provider attribute.

Beyond those two hard requirements, the framework has several softer
advantages: explicit `null` / `unknown` / `known` value semantics in
schemas, stronger schema-level validation primitives via
`terraform-plugin-framework-validators`, clearer separation between plan
and apply phases, and a more idiomatic Go API surface (interface-driven,
context-aware, no reliance on `interface{}` typing).

The framework is also the actively-developed path. New language features
(actions, ephemeral resources, write-only attributes, deferred actions)
land in the framework first and may never appear in SDKv2.

## Decision

This provider is built exclusively on `terraform-plugin-framework`. The
provider does **not** use `terraform-plugin-mux` to combine framework with
SDKv2 — there is no SDKv2 code to mux against.

### Library pinning and depguard enforcement

The provider's `go.mod` directly depends on:

- `github.com/hashicorp/terraform-plugin-framework`
- `github.com/hashicorp/terraform-plugin-framework-validators` (URL
  validation, etc.)
- `github.com/hashicorp/terraform-plugin-go` (protocol v6 server)
- `github.com/hashicorp/terraform-plugin-log` (`tflog` context-aware
  logging)
- `github.com/hashicorp/terraform-plugin-testing` (acceptance test
  harness)

To prevent accidental SDKv2 usage creeping in over time, `.golangci.yml`
carries a `depguard` rule that **denies imports from
`github.com/hashicorp/terraform-plugin-sdk/v2`** with a custom error
message pointing developers at the framework equivalent (e.g.
"Use github.com/hashicorp/terraform-plugin-testing/helper/resource").
The rule is enforced in CI via `just lint`.

### Validation strategy

Per the framework's idiomatic pattern (and per
[IMPL-0001 §Decisions #9](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md#decisions)),
`Configure()` performs only cheap local validation (URL parses, token
non-empty) and constructs the client struct. No network call. First API
request — typically `garage_cluster_info` as a smoke test — surfaces
auth or connectivity errors. This matches what
`terraform-provider-tls` and other HashiCorp-maintained providers do.

## Consequences

### Positive

- **Unblocks ADR-0001's write-only `secret_source = "external"` flow**
  — the load-bearing v0.1 feature.
- **Clearer schema semantics.** `types.String` vs `types.StringNull()` vs
  `types.StringUnknown()` is more correct than SDKv2's `interface{}`
  juggling.
- **Better validation surface** via
  `terraform-plugin-framework-validators` — declarative validators on
  schema attributes catch errors at plan time, not apply time.
- **Future-proof.** Actions, ephemeral resources, deferred actions, and
  whatever comes next will all land in the framework first.
- **Aligns with the broader provider ecosystem.** Most actively-maintained
  providers have migrated or are migrating.

### Negative

- **Steeper learning curve for contributors used to SDKv2.** The
  framework's interface-driven model (each resource is a struct
  implementing several interfaces) is more verbose than SDKv2's flat
  `*schema.Resource` form. Mitigation: link contributors to the
  HashiCorp scaffolding template and to the framework's tutorials in
  `CONTRIBUTING.md`.
- **More files per primitive.** A resource needs at minimum a `Schema`,
  `Configure`, `Create/Read/Update/Delete` methods, and `ImportState` —
  often split across multiple receivers. SDKv2 would put the same thing
  in one `*schema.Resource` value. Counts as verbosity, not real cost.

### Neutral

- **OpenTofu support is identical.** Both libraries speak protocol v6;
  OpenTofu can consume either. Phase-2-and-later CI matrix will exercise
  `tofu` alongside `terraform`.
- **`terraform-plugin-testing` replaces the SDKv2 `helper/resource`
  package.** Same `resource.Test` API shape, just a different import
  path. The `depguard` rule's custom messages help redirect anyone who
  reaches for the old import out of muscle memory.
- **gRPC protocol version is v6 (`protocol_versions: ["6.0"]` in
  `terraform-registry-manifest.json`).** Same value SDKv2 would emit;
  not a framework-specific concern.

## Alternatives Considered

- **SDKv2 only.** Rejected. Lacks write-only and ephemeral support;
  blocks ADR-0001. Plus the framework is the actively-developed library
  — choosing SDKv2 for a greenfield provider locks in tech debt from day
  one.
- **Plugin mux (framework + SDKv2 via `terraform-plugin-mux`).** The mux
  approach exists for providers migrating gradually from SDKv2 to the
  framework — it lets some resources use one library and others use the
  other, with the mux dispatching at the protocol layer. For a
  greenfield provider with no SDKv2 baseline, the mux adds complexity
  with zero offsetting benefit. Rejected.
- **A custom protocol-v6 server (no library at all).** Implementing the
  Terraform protocol from scratch is feasible (the protobuf definitions
  are public) but is roughly two orders of magnitude more work than
  using the framework. Rejected outright.

## References

- [RFC-0001 §Alternatives Considered](../rfc/0001-garage-terraformopentofu-provider.md#alternatives-considered)
- [ADR-0001](0001-garagekey-secret-handling-explicit-secretsource-modes.md)
  — write-only requirement that motivates the choice
- [`docs/additional.md` ADR-0002 notes](../additional.md) — original
  decision notes from which this ADR was formalized
- [HashiCorp: terraform-plugin-framework introduction](https://developer.hashicorp.com/terraform/plugin/framework)
- [HashiCorp: Which SDK should I use?](https://developer.hashicorp.com/terraform/plugin/framework-benefits)
- [OpenTofu: Provider protocol documentation](https://opentofu.org/docs/internals/provider-protocol/)
- [HashiCorp blog: Write-only arguments in Terraform 1.11](https://www.hashicorp.com/blog/terraform-1-11-adds-write-only-arguments)

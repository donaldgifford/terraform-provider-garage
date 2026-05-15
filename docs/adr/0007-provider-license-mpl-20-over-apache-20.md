---
id: ADR-0007
title: "Provider license: MPL-2.0 over Apache-2.0"
status: Accepted
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# 0007. Provider license: MPL-2.0 over Apache-2.0

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
  - [File-header convention](#file-header-convention)
  - [Garage admin OpenAPI spec vendoring](#garage-admin-openapi-spec-vendoring)
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

The forge `go-ext` blueprint that bootstrapped this repository defaults to
Apache-2.0, while [`docs/repo-init.md`](../repo-init.md) §6 — written when the
project was still planned on top of the HashiCorp scaffolding template —
recommends MPL-2.0 for ecosystem alignment. The license has to be chosen
before any Go source files are written, because the file-header convention
(`SPDX-License-Identifier`) depends on it.

The Terraform / OpenTofu provider ecosystem has a strong de-facto licensing
norm:

- **OpenTofu** is licensed MPL-2.0
- **Terraform**, prior to the August 2023 relicensing to BSL, was MPL-2.0
- The vast majority of community and HashiCorp-maintained providers
  (`terraform-provider-aws`, `terraform-provider-tls`,
  `terraform-provider-random`, the
  `terraform-provider-scaffolding-framework` template that originally
  seeded this project) are MPL-2.0
- HashiCorp Registry submission documentation, the `tfplugindocs` tooling,
  and the `terraform-plugin-framework` repository itself are all MPL-2.0

A secondary consideration: the project vendors `garage-admin-v2.json`, an
OpenAPI specification served by Garage HQ that is auto-generated from
**AGPL-3.0**-licensed Rust source. The license of the spec file itself is
ambiguous (data describing an interface generally isn't copyrightable in the
same way the source is), but the prudent reading is that the vendored JSON
inherits the upstream project's license terms. The choice of provider
license affects whether the resulting repo can comfortably package the
vendored spec alongside the generated Go client.

## Decision

The provider is licensed **MPL-2.0** (Mozilla Public License, version 2.0),
matching the ecosystem norm and the original repo-init.md recommendation.

The decision rests on three points, in order of weight:

1. **Ecosystem alignment.** Every nontrivial consumer of this provider will
   already be running MPL-2.0 software (OpenTofu, or pre-BSL Terraform, plus
   most of their other providers). Matching that licensing reduces friction
   for downstream organizations doing compliance review.
2. **File-level weak copyleft is the right shape for a provider.** MPL-2.0
   requires modifications to MPL-licensed files to remain MPL-2.0 but
   permits combining them with code under other licenses in the same
   project. That's exactly the model a Terraform provider needs:
   contributions back upstream stay open, but consumers can use the
   provider inside any Terraform configuration regardless of their own
   licensing.
3. **Compatibility with the vendored Garage spec.** Garage is AGPL-3.0; the
   admin OpenAPI spec we vendor is derived from that source. MPL-2.0 is
   considered compatible with AGPL-3.0 for the purpose of distributing
   interface definitions (the AGPL's source-disclosure obligations apply
   to derivative *software*, not to interface specs we don't modify). An
   Apache-2.0 provider would have a weaker compatibility story here,
   because Apache-2.0's patent grant interacts with the AGPL in ways that
   make hybrid-licensed distributions awkward.

### File-header convention

All Go source files in this repository (including build-only files under
`tools/`) carry the following two-line header:

```go
// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0
```

- The copyright year is fixed at the year of original authorship (2026); it
  is **not** incremented on every modification per modern SPDX guidance —
  authorship attribution lives in git history.
- The SPDX identifier is the machine-readable license reference; `LICENSE`
  at the repo root holds the full text.
- Files derived from third-party MPL-2.0 sources (e.g. anything originally
  copied from the HashiCorp scaffolding template) retain their upstream
  copyright header in addition to ours.

Non-Go files (Markdown, YAML, JSON, HCL examples) do not carry an inline
SPDX header; the repo-level `LICENSE` file applies project-wide.

### Garage admin OpenAPI spec vendoring

The vendored file `internal/client/openapi/garage-admin-v2.json` carries no
modifications from the upstream Garage HQ–published version. A header
comment in the sibling `doc.go` documents:

- The source URL (`https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json`)
- The pinned `info.version` of the spec
- A note that the spec is auto-generated by `utoipa` from Garage's
  AGPL-3.0–licensed Rust source, and that downstream redistribution of the
  vendored JSON honours Garage HQ's published terms

The `oapi-codegen`-generated Go client (`generated.go`) is a derivative work
of the provider source (`oapi-codegen` itself is Apache-2.0) and carries
our standard MPL-2.0 header. The generated code does not inherit AGPL-3.0
terms — interface specifications are not copyrightable software, per
established case law (e.g. Oracle v. Google).

## Consequences

### Positive

- **Drop-in fit for OpenTofu users.** Same license as the runtime they
  use; no compliance friction.
- **Aligned with the wider Terraform provider ecosystem.** Following the
  prevailing norm reduces the cost for users to vendor or audit the
  provider.
- **File-level weak copyleft keeps contributions open without overreaching
  into consumer projects.** Modifications to provider sources must remain
  MPL-2.0; downstream Terraform configurations using the provider are
  unaffected.
- **Compatible with the AGPL-3.0 origin of the Garage OpenAPI spec** for
  the narrow purpose of redistributing the interface description.

### Negative

- **Less permissive than Apache-2.0.** Forks that wanted to relicense
  modified versions under, say, BSD or proprietary terms cannot do so
  without re-engineering the modified files cleanly.
- **No explicit patent grant.** Apache-2.0 includes an explicit patent
  license; MPL-2.0's patent grant is narrower (implicit, file-scoped). For
  a Terraform provider this is a non-issue in practice — providers are not
  patentable subject matter — but it's a real licensing difference.

### Neutral

- **`LICENSE` file at the repo root holds the full MPL-2.0 text.** Forge's
  `go-ext` blueprint shipped an Apache-2.0 `LICENSE`; that file is replaced
  as part of this ADR's implementation.
- **A NOTICE file is optional.** Recommended only if we later embed
  third-party MPL/Apache code that requires attribution beyond what
  `LICENSE` covers. None such exists today.
- **Future relicensing is hard but not impossible.** MPL-2.0 → Apache-2.0
  requires either rewriting the MPL-2.0–touched files cleanly or
  collecting CLAs from every contributor. Standard cost of switching
  copyleft → permissive licenses, no different from any other project.

## Alternatives Considered

- **Apache-2.0** (forge default): permissive, explicit patent grant, used
  by some HashiCorp tooling adjacent to the provider ecosystem (e.g.
  `oapi-codegen` itself). Rejected because the dominant TF/OpenTofu
  provider norm is MPL-2.0 and matching the norm has real value for
  downstream compliance review.
- **MIT / BSD-3-Clause**: maximally permissive, common in Go libraries.
  Rejected for the same reason as Apache-2.0 — too far from the provider
  ecosystem norm — plus they offer weaker provenance guarantees than
  copyleft variants.
- **AGPL-3.0** (matching the upstream Garage project): the strongest
  copyleft option; would propagate source-disclosure obligations to
  Terraform configurations using the provider, which is not desirable
  for a provider distributed via public registries. Rejected.
- **BSL** (matching post-2023 HashiCorp Terraform): incompatible with
  OpenTofu distribution and adds usage restrictions. Rejected outright.

## References

- [`docs/repo-init.md`](../repo-init.md) §6 — original license discussion
  carrying over from the HashiCorp-scaffolding plan
- [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
- [IMPL-0001](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md) §Decisions — operational follow-through
- [Mozilla Public License 2.0 full text](https://www.mozilla.org/en-US/MPL/2.0/)
- [MPL-2.0 FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/) — the
  authoritative reading on file-level weak copyleft semantics
- [OpenTofu licensing rationale](https://opentofu.org/blog/the-future-of-terraform-must-be-open/)
- [SPDX license identifiers reference](https://spdx.org/licenses/)

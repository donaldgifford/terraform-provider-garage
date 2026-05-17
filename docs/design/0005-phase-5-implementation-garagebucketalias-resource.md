---
id: DESIGN-0005
title: "Phase 5 implementation: garage_bucket_alias resource"
status: Draft
author: Donald Gifford
created: 2026-05-17
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0005: Phase 5 implementation: garage_bucket_alias resource

**Status:** Draft — **Sketch only.** Finalized alongside DESIGN-0003 / IMPL-0003.
**Author:** Donald Gifford
**Date:** 2026-05-17

<!--toc:start-->
- [Overview](#overview)
- [Status](#status)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design — sketch](#detailed-design--sketch)
  - [The global vs local question](#the-global-vs-local-question)
  - [Resource shape options](#resource-shape-options)
  - [Conflict with inline `global_aliases`](#conflict-with-inline-global_aliases)
  - [Lifecycle mapping](#lifecycle-mapping)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Phase 5 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
introduces `garage_bucket_alias`: a Terraform-managed alias edge on top
of a bucket. Garage supports two alias *flavors* via the same admin
endpoint (`AddBucketAlias` / `RemoveBucketAlias`) with a union request
body:

- **Global aliases** — cluster-wide name → bucket mapping. Already
  managed inline as `garage_bucket.global_aliases` (a `Set[String]`)
  per DESIGN-0002.
- **Local aliases** — per-key name → bucket mapping, scoped to a
  specific `access_key_id`. Not yet exposed.

The core design question for Phase 5 is whether this resource should
cover both flavors, only local, or whether the two flavors should be
two separate resource types.

## Status

**Sketch.** This doc captures the design space and the key questions
without committing to a resolution. Finalized alongside DESIGN-0003 /
IMPL-0003, once Phase 3's key resource gives us a concrete
`garage_key.access_key_id` reference to plumb into the local-alias
schema.

## Goals and Non-Goals

### Goals

- Some mechanism to manage **local aliases** (the surface that's
  currently inaccessible from Terraform) — per-key naming so HCL like
  `s3 cp s3://my-local-alias/...` works under a specific key
- Acceptance tests for the local-alias lifecycle
- Clear interaction model with `garage_bucket.global_aliases` so users
  don't get conflicting plans

### Non-Goals

- Reimplementing global-alias management for the common case — the
  inline `garage_bucket.global_aliases` from Phase 2 already covers it
  ergonomically. A separate `garage_bucket_alias` for globals is only
  needed for the cross-team scenario (Team A owns the bucket, Team B
  owns one of its aliases — see Open Question 1)

## Background

After Phase 4 the repo has:

- `internal/client/client.go` with `AddBucketAlias` and
  `RemoveBucketAlias` (both wired in IMPL-0002 Phase 1)
- The Phase 2 verification that `AddBucketAlias` is idempotent on the
  same bucket (2xx no-op) and that last-global-alias removal is refused
  by Garage (HTTP 400 with a specific message — captured in
  IMPL-0002 §Decisions #2)
- The `garage_bucket.global_aliases` Set attribute with adds-before-removes
  diff semantics — Phase 2 Decision 9 deferred the "external resource
  conflict" question to this doc

Garage admin v2 schema:

```go
// BucketAliasEnum is a oneOf union:
//   BucketAliasEnum0 → {bucketId, globalAlias}   (global flavor)
//   BucketAliasEnum1 → {bucketId, accessKeyId, localAlias}  (local flavor)
type BucketAliasEnum struct { ... }
```

The endpoint is the same (`AddBucketAlias` / `RemoveBucketAlias`); the
discriminator is which fields are present in the body.

## Detailed Design — sketch

### The global vs local question

Three plausible shapes for the resource:

**Option A: Single resource, both flavors.**

```hcl
resource "garage_bucket_alias" "team_b_alias" {
  bucket_id = garage_bucket.shared.id
  alias     = "team-b-name"
  # access_key_id present => local; absent => global
  access_key_id = garage_key.team_b.access_key_id  # optional
}
```

Schema discriminates on whether `access_key_id` is set. Risk:
ambiguous HCL — readers must check `access_key_id` presence to know
which flavor. Cross-attribute validators can mitigate.

**Option B: Two separate resources.**

```hcl
resource "garage_bucket_global_alias" "team_b_alias" {
  bucket_id = garage_bucket.shared.id
  alias     = "team-b-name"
}

resource "garage_bucket_local_alias" "team_b_alias" {
  bucket_id     = garage_bucket.shared.id
  access_key_id = garage_key.team_b.access_key_id
  alias         = "team-b-name"
}
```

Two resource types, each with a narrower schema. Clearer at the cost
of duplicating package boilerplate.

**Option C: Local-only.**

Ship only `garage_bucket_local_alias`. Defer global-alias-as-resource
to a later phase, document the cross-team workaround (Team B's
Terraform manages the bucket too) as good enough for v0.1.

Current lean: **Option B**. Avoids the ambiguity of A; lets the
inline-global-aliases-on-bucket pattern stand as the ergonomic default
without being clobbered. The local resource is small enough that
duplicating the boilerplate is a one-time cost.

### Resource shape options

(Final shape resolved alongside DESIGN-0003.)

Sketches assuming **Option B**:

```hcl
# Global flavor — cross-team scenario
resource "garage_bucket_global_alias" "team_b_alias" {
  bucket_id = "<bucket id owned by another HCL config>"
  alias     = "team-b-canonical-name"
}

# Local flavor — per-key naming
resource "garage_bucket_local_alias" "loki_logs" {
  bucket_id     = garage_bucket.logs.id
  access_key_id = garage_key.loki.access_key_id
  alias         = "logs"
}
```

Synthetic id format (sketch):

- Global: `<bucket_id>/global/<alias>`
- Local:  `<bucket_id>/<access_key_id>/<alias>`

### Conflict with inline `global_aliases`

The Phase 2 design (DESIGN-0002 §Open Questions, deferred-to-here)
flagged the conflict: if `garage_bucket.foo.global_aliases` declares
`["images.example.com"]` and `garage_bucket_global_alias.bar` *also*
declares `alias = "images.example.com"` for the same bucket id, the
two resources will fight on every plan.

Resolution options:

- **A** — `garage_bucket_global_alias` refuses (via Read-time
  diagnostic) to manage an alias already in the bucket's `global_aliases`
  Set. Requires the resource to peek at the bucket's state, which is
  awkward across resource boundaries.
- **B** — `garage_bucket.global_aliases` ignores aliases not in its
  own state (i.e. lets external aliases stay). Convert the schema to
  "additive — only manages aliases declared in this attr; doesn't
  delete others." Risk: surprising the existing Phase 2 users, who
  expect `global_aliases = []` to mean "no global aliases."
- **C** — Document the conflict in both resources' MarkdownDescriptions
  and let users avoid it. No code-level enforcement.

Current lean: **C** for v0.1 (cheapest), with **A** as a follow-up if
users hit the foot-gun. The Phase 2 inline pattern is what 99% of
users will reach for; cross-team global aliases are the 1% case and
those users have the context to read the docs.

### Lifecycle mapping

Same shape for both flavors (the BucketAliasEnum union determines body):

| TF lifecycle | Garage admin operation                                         |
|--------------|----------------------------------------------------------------|
| Create       | `AddBucketAlias({BucketAliasEnum<flavor>})`                    |
| Read         | `GetBucket(bucket_id)` → check `GlobalAliases[]` or `Keys[].LocalAliases[]` for the alias |
| Update       | `alias` is `RequiresReplace` (rename = delete + create);  `bucket_id` and `access_key_id` likewise |
| Delete       | `RemoveBucketAlias({BucketAliasEnum<flavor>})` — Garage refuses last-global-alias removal (Phase 2 finding); local-alias removal has no such restriction |

`Read` is two-stage for the local flavor: fetch the bucket, find the
key in `Keys[]`, then check `LocalAliases[]`. Verify alternative path
(`GetKeyInfo` → `Buckets[].LocalAliases[]`) gives the same answer —
this is DESIGN-0004 Open Question 2 territory.

## Open Questions

1. **Single resource vs two resources (Option A/B/C above).** Lean is
   B (two resources). Decide before IMPL-0005 scaffold so we don't
   refactor package layout halfway through.

2. **Inline-vs-external global alias conflict resolution.** Lean is C
   (document) for v0.1. Revisit if users open issues.

3. **Local-alias-on-deleted-key drift.** If the key referenced by a
   `garage_bucket_local_alias` is deleted out-of-band, Garage probably
   garbage-collects the alias. Verify and document the drift signal.

4. **Naming.** `garage_bucket_global_alias` is verbose. Alternatives:
   `garage_global_alias`, `garage_bucket_alias` (with a flavor
   discriminator — Option A). Decision deferred to the same Open
   Question 1 above; the name is downstream of the resource-shape
   decision.

5. **Validation of alias strings.** Garage accepts arbitrary strings
   as aliases (the bucket resource's `global_aliases` accepts anything
   already). Should this resource add stricter validation (e.g. DNS-1123
   subdomain pattern for S3 wildcard routing)? Probably not for v0.1
   — match the bucket's permissive behavior.

6. **Import.** `terraform import garage_bucket_global_alias.foo <bucket_id>/<alias>`
   and `garage_bucket_local_alias.foo <bucket_id>/<access_key_id>/<alias>`.
   Verify both id strings parse cleanly (no `/` in any component —
   inherits the constraints from DESIGN-0004 Open Question 3).

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 5 entry
- [DESIGN-0002: Phase 2 garage_bucket resource](0002-phase-2-implementation-garagebucket-resource.md)
  — `global_aliases` inline pattern that this resource interacts with;
  §Decisions #9 (deferred-to-Phase-5) is the parent of Open Question 2
- [DESIGN-0003: Phase 3 garage_key resource](0003-phase-3-implementation-garagekey-resource.md)
  — the resource local aliases reference
- [DESIGN-0004: Phase 4 garage_bucket_key resource](0004-phase-4-implementation-garagebucketkey-resource.md)
  — companion edge resource on (bucket, key); Open Question 2 about
  read-path source-of-truth applies here too
- [Garage admin v2 API: alias endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [Phase 2 IMPL §Decisions #2](../impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — captures the "last-global-alias removal refused" Garage behavior

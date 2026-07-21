---
id: DESIGN-0005
title: "Phase 5 implementation: standalone bucket alias resources (global + local)"
status: Draft
author: Donald Gifford
created: 2026-05-17
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0005: Phase 5 implementation: standalone bucket alias resources (global + local)

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-17 (revised 2026-07-20)

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Two standalone resources, no inline](#two-standalone-resources-no-inline)
  - [Schema — global alias](#schema--global-alias)
  - [Schema — local alias](#schema--local-alias)
  - [Lifecycle mapping](#lifecycle-mapping)
  - [Removing the inline alias attribute from the bucket](#removing-the-inline-alias-attribute-from-the-bucket)
  - [The last-alias-removal constraint](#the-last-alias-removal-constraint)
  - [Identity and import](#identity-and-import)
  - [Client wrapper extensions](#client-wrapper-extensions)
  - [Package layout](#package-layout)
- [API / Interface Changes](#api--interface-changes)
- [Data Model](#data-model)
- [Testing Strategy](#testing-strategy)
- [Migration / Rollout Plan](#migration--rollout-plan)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Phase 5 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
introduces two standalone alias resources and removes alias management
from the bucket:

- `garage_bucket_global_alias` — a cluster-wide name → bucket mapping
  (any key resolves it). This is the "normal" bucket name.
- `garage_bucket_local_alias` — a name → bucket mapping scoped to a
  specific `access_key_id` (only that key resolves it). This is the
  surface currently inaccessible from Terraform.

The inline `global_aliases` attribute on `garage_bucket` (shipped in
Phase 2) is **removed** so each alias has exactly one owning resource.
This follows the modern provider convention — one canonical resource per
relationship, no inline duplication — and eliminates the
inline-vs-standalone conflict that Terraform cannot validate at plan
time (a resource sees only its own config, and Garage treats a
same-bucket duplicate alias as an idempotent no-op, so the collision
would only surface later as permanent drift).

Because inline aliases are gone, `garage_bucket_global_alias` is
**required** to give a bucket a resolvable name — it is not optional and
ships in the same change as the removal.

## Goals and Non-Goals

### Goals

- `garage_bucket_global_alias` (cluster) and `garage_bucket_local_alias`
  (key-scoped) resources, each with Create / Read / Delete / Import and
  drift detection (no Update — every attribute is `RequiresReplace`)
- Remove `global_aliases` from `garage_bucket` so aliases have a single
  owner; ship a migration guide + an ADR recording the breaking change
- Graceful handling of Garage's last-alias-removal refusal so
  `terraform destroy` of a bucket and its aliases works
- Acceptance tests for both resources, drift, import,
  destroy-with-sole-alias, and the migration path off inline aliases
- Generated docs for both resources via `tfplugindocs`

### Non-Goals

- Keeping inline aliases on `garage_bucket` — deliberately removed for a
  single source of truth
- A single unified `garage_bucket_alias` with an optional `access_key_id`
  — rejected in favor of two explicit resources whose schemas are each
  exactly what they need (global has no key; local requires one),
  clearer in HCL and in import ids
- Cross-resource plan-time validation of duplicate aliases — not
  possible in Terraform; designed *around* via single-source-of-truth

## Background

After Phase 4 the repo has:

- `internal/client/client.go` with `AddBucketAlias` /
  `RemoveBucketAlias` (cluster flavor, `BucketAliasEnum0`) from IMPL-0002
- `internal/resources/bucket/` shipping `garage_bucket` with an inline
  `global_aliases` (`Set[String]`) attribute + adds-before-removes diff
- IMPL-0002 Phase 2 findings: `AddBucketAlias` is idempotent on the same
  bucket (2xx); Garage **refuses to remove a bucket's last alias**
  (HTTP 400 `"...please delete it instead of just unaliasing."`); an
  alias pointing at a different bucket returns HTTP 400
- `garage_key` (Phase 3) providing `access_key_id`

Garage models both flavors through the same endpoint; the request body
discriminates:

```go
type BucketAliasEnum0 struct { BucketId, GlobalAlias string }               // cluster-wide
type BucketAliasEnum1 struct { AccessKeyId, BucketId, LocalAlias string }   // key-scoped
```

On read they surface in different places on `GetBucketInfoResponse`:
cluster → `GlobalAliases []string`; key-scoped →
`Keys[].BucketLocalAliases []string` (per `access_key_id`).

## Detailed Design

### Two standalone resources, no inline

Aliases are managed by two resources and nowhere else. The inline
`garage_bucket.global_aliases` attribute is removed. Global and local
aliases live in different namespaces (cluster-wide vs per-key), so the
two resources never collide with each other; and with inline gone, there
is no inline-vs-standalone collision either. That leaves alias
management fully single-owner.

### Schema — global alias

```go
// garage_bucket_global_alias
"id":        schema.StringAttribute{Computed: true, /* <bucket_id>/<alias> */ PlanModifiers: UseStateForUnknown},
"bucket_id": schema.StringAttribute{Required: true, PlanModifiers: RequiresReplace},
"alias":     schema.StringAttribute{Required: true, PlanModifiers: RequiresReplace},
```

No `access_key_id`. No restriction on `/` in `alias` (import parsing is
unambiguous — see [Identity and import](#identity-and-import)).

### Schema — local alias

```go
// garage_bucket_local_alias
"id":            schema.StringAttribute{Computed: true, /* <bucket_id>/<access_key_id>/<alias> */ PlanModifiers: UseStateForUnknown},
"bucket_id":     schema.StringAttribute{Required: true, PlanModifiers: RequiresReplace},
"access_key_id": schema.StringAttribute{Required: true, PlanModifiers: RequiresReplace},
"alias":         schema.StringAttribute{Required: true, PlanModifiers: RequiresReplace},
```

`access_key_id` is **required** here (a local alias is meaningless
without its key).

### Lifecycle mapping

| TF lifecycle | `garage_bucket_global_alias`                          | `garage_bucket_local_alias`                                   |
|--------------|------------------------------------------------------|--------------------------------------------------------------|
| Create       | `AddBucketAlias(bucket_id, alias)`                   | `AddBucketLocalAlias(bucket_id, access_key_id, alias)`       |
| Read         | `GetBucket(bucket_id)` → `GlobalAliases[]`           | `GetBucket(bucket_id)` → `Keys[]` for the key → `BucketLocalAliases[]` |
| Update       | none — all attributes `RequiresReplace`             | none — all attributes `RequiresReplace`                      |
| Delete       | `RemoveBucketAlias` (with last-alias handling)       | `RemoveBucketLocalAlias`                                     |
| Import       | `<bucket_id>/<alias>`                                | `<bucket_id>/<access_key_id>/<alias>`                        |

Read removes the resource from state (`RemoveResource`) if the bucket is
gone (`ErrNotFound`) or the alias is absent from the relevant list.

### Removing the inline alias attribute from the bucket

`garage_bucket` loses `global_aliases`, its alias-diff logic, and the
`AddBucketAlias` / `RemoveBucketAlias` calls in Create/Update. The client
methods stay (the new resources use them). `garage_bucket` Create becomes
`CreateBucket` (+ quotas) only; a bucket with no aliases is valid
(addressable by its 64-char hex id until a `garage_bucket_global_alias`
names it).

This is a **breaking change** to a shipped resource. Pre-1.0 permits it,
but it needs a migration path (import existing aliases as resources) and
an ADR. The two new resources must ship **with** the removal so users are
never without a way to name a bucket.

### The last-alias-removal constraint

Garage refuses to remove a bucket's *only* alias (IMPL-0002 Verification
A). With aliases as separate resources, this bites `terraform destroy`:
an alias references `garage_bucket.x.id`, so Terraform destroys the alias
**before** the bucket — and if it is the bucket's last alias, Garage
refuses, failing the destroy.

Handling (Open Question 2): on a last-alias HTTP 400 during
`garage_bucket_global_alias` `Delete`, issue `GetBucket` — bucket gone →
success; bucket still present → warn and remove from state, relying on
`DeleteBucket` to clean the alias. This keeps destroy working; the only
leak is removing a bucket's sole global alias while keeping the bucket
(rare), which the warning names. Local aliases likely have no such
restriction (Open Question / livecheck).

### Identity and import

- global: `<bucket_id>/<alias>` — parse by splitting on the **first**
  `/` (bucket_id is a `/`-free 64-char hex; the remainder is the alias,
  which may itself contain `/`)
- local: `<bucket_id>/<access_key_id>/<alias>` — split on the **first
  two** `/` (bucket_id and access_key_id are both `/`-free; the
  remainder is the alias)

Because each resource type has a fixed number of leading id segments,
parsing is unambiguous even if the alias contains `/`. No alias-format
restriction is needed.

```
terraform import garage_bucket_global_alias.images  <bucket_id>/<alias>
terraform import garage_bucket_local_alias.logs      <bucket_id>/<access_key_id>/<alias>
```

### Client wrapper extensions

Two new methods for the key-scoped flavor (cluster methods already
exist):

```go
func (c *Client) AddBucketLocalAlias(ctx context.Context, bucketID, accessKeyID, localAlias string) error
func (c *Client) RemoveBucketLocalAlias(ctx context.Context, bucketID, accessKeyID, localAlias string) error
```

Both build `BucketAliasEnum1` bodies and follow the established wrapper
conventions (`op` constant, `statusToError`, `tflog.Trace`, empty-arg
guards).

### Package layout

```
internal/
  client/
    client.go              # + AddBucketLocalAlias / RemoveBucketLocalAlias
  resources/
    bucket/                # global_aliases attribute + diff logic REMOVED
    bucketalias/
      global.go            # garage_bucket_global_alias resource
      local.go             # garage_bucket_local_alias resource
      model.go             # shared id helpers + findGlobalAlias / findLocalAlias
      *_test.go
```

Both resources share one package (`bucketalias`) to reuse the `GetBucket`
read path and id helpers; each is registered separately in
`provider.Resources()`. Splitting into two packages is a reasonable
alternative (Open Question / reviewer preference).

## API / Interface Changes

New resources:

```hcl
resource "garage_bucket_global_alias" "images" {
  bucket_id = garage_bucket.pics.id
  alias     = "images"
}

resource "garage_bucket_local_alias" "logs" {
  bucket_id     = garage_bucket.logs.id
  access_key_id = garage_key.loki.access_key_id
  alias         = "logs"
}
```

Removed (breaking):

```hcl
resource "garage_bucket" "pics" {
  global_aliases = ["images"]   # <-- no longer exists
}
```

## Data Model

```go
type GlobalAliasModel struct {
    ID       types.String `tfsdk:"id"`
    BucketID types.String `tfsdk:"bucket_id"`
    Alias    types.String `tfsdk:"alias"`
}

type LocalAliasModel struct {
    ID          types.String `tfsdk:"id"`
    BucketID    types.String `tfsdk:"bucket_id"`
    AccessKeyID types.String `tfsdk:"access_key_id"`
    Alias       types.String `tfsdk:"alias"`
}
```

Shared helpers in `model.go`: `globalID` / `localID`, `parseGlobalID` /
`parseLocalID`, `findGlobalAlias(*GetBucketInfoResponse, alias)` and
`findLocalAlias(*GetBucketInfoResponse, accessKeyID, alias)`.

## Testing Strategy

| Layer      | What it covers                                                       | Location                                  |
|------------|----------------------------------------------------------------------|-------------------------------------------|
| Unit       | `AddBucketLocalAlias` / `RemoveBucketLocalAlias` wrappers             | `internal/client/client_test.go`          |
| Unit       | id round-trip + parse (both), `findGlobalAlias` / `findLocalAlias`   | `internal/resources/bucketalias/*_test.go`|
| Live probe | Local round-trip; read parity; last-alias (global + local); GC       | `internal/client/livecheck_test.go`       |
| Acceptance | Global alias: create / read / drift / import                        | `internal/resources/bucketalias/*_test.go`|
| Acceptance | Local alias: create / read / drift / import                         | same                                      |
| Acceptance | `terraform destroy` of bucket + sole global alias                   | same                                      |
| Acceptance | Migration: bucket that previously used `global_aliases`             | same                                      |

## Migration / Rollout Plan

1. Ship `garage_bucket_global_alias` + `garage_bucket_local_alias` fully
   working — IMPL-0005 Phases 1–5
2. Remove `global_aliases` from `garage_bucket` — IMPL-0005 Phase 6 —
   with a migration guide: for each old alias, add a
   `garage_bucket_global_alias` and `terraform import
   <bucket_id>/<alias>` (the alias already exists in Garage, so import is
   a no-op read), then drop `global_aliases` from the bucket config
3. Record the breaking decision in a new ADR (Open Question 1)
4. Version bump reflects the break (pre-1.0 convention — Open Question 1)

## Decisions

1. **Two standalone resources, no inline** — `garage_bucket_global_alias`
   + `garage_bucket_local_alias`; `global_aliases` removed from
   `garage_bucket`. Single owner per alias; matches the modern canonical
   pattern. *(Owner call, 2026-07-20.)*
2. **Explicit resources over a unified optional-key resource** — each
   schema is exactly what it needs; global's id is 2-part, local's is
   3-part, so import parsing is unambiguous and aliases can contain `/`.
3. **All attributes `RequiresReplace`; no Update.**
4. **`garage_bucket_global_alias` is required, not optional** — with
   inline gone, it is the only way to give a bucket a cluster name.
5. **Read source-of-truth is `GetBucket`** — `GlobalAliases[]` /
   `Keys[].BucketLocalAliases[]`. Consistent with DESIGN-0004.

## Open Questions

**Resolution (2026-07-21): all resolved to option (a).** OQ1 hard-remove
`global_aliases` + migration guide +
[ADR-0008](../adr/0008-remove-inline-globalaliases-in-favor-of-standalone-alias.md);
OQ2 lenient global-alias
`Delete`; OQ3 one `bucketalias` package holding both resources; OQ4
missing key/alias on Read → `RemoveResource` (confirm GC in the Phase 2
livecheck); OQ5 single PR. Options retained below for the record.

Each option is lettered — **a is the recommendation**, b+ are
alternatives, or supply your own under *other*.

1. **How to remove `global_aliases` from `garage_bucket` (breaking
   change mechanics).**
   - **a. (Recommended)** Hard-remove in one release + migration guide +
     new ADR (pre-1.0 permits the break).
   - b. Deprecate one release (warn + read-only) then remove.
   - c. Keep it as a read-only computed mirror of the global-alias
     resources.
   - *other:* ______

2. **Last-alias-removal / `terraform destroy` ordering.**
   - **a. (Recommended)** Lenient `Delete` on the global-alias resource:
     on Garage's last-alias HTTP 400, `GetBucket`; bucket gone →
     success, bucket present → warn + remove from state (rely on
     `DeleteBucket`). Keeps `destroy` working.
   - b. Strict — surface the HTTP 400 (may deadlock `terraform destroy`).
   - c. Keep a bucket-owned "primary" alias inline after all (partial
     revert of Decision 1).
   - *other:* ______

3. **One package or two for the resources.**
   - **a. (Recommended)** One `bucketalias` package holding both
     resources (shared `GetBucket` read + id helpers), each registered
     separately. Less duplication.
   - b. Two packages (`bucketglobalalias` / `bucketlocalalias`) to match
     the one-resource-per-package convention elsewhere in the repo.
   - *other:* ______

4. **Local-alias behavior on a deleted key.**
   - **a. (Recommended)** Treat a missing key/alias on Read as
     `RemoveResource` (Garage likely GCs the local alias when the key is
     deleted); confirm in the Phase 2 livecheck.
   - b. Surface an explicit diagnostic if the key vanished out-of-band.
   - *other:* ______

5. **PR / branch strategy.**
   - **a. (Recommended)** Single PR — both resources + the removal land
     atomically so there is never a release without a way to name a
     bucket.
   - b. Two PRs — resources first, then the breaking removal.
   - *other:* ______

## References

- [ADR-0008: Remove inline global_aliases in favor of standalone alias resources](../adr/0008-remove-inline-globalaliases-in-favor-of-standalone-alias.md)
  — records this breaking decision and its rationale
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 5 entry
- [DESIGN-0002: Phase 2 garage_bucket resource](0002-phase-2-implementation-garagebucket-resource.md)
  — the `global_aliases` inline attribute this design removes;
  §Decisions #9 (deferred-to-Phase-5) is the parent of this pivot
- [DESIGN-0003: Phase 3 garage_key resource](0003-phase-3-implementation-garagekey-resource.md)
  — provides `access_key_id`, referenced by `garage_bucket_local_alias`
- [DESIGN-0004: Phase 4 garage_bucket_key resource](0004-phase-4-implementation-garagebucketkey-resource.md)
  — companion edge resource; shares the `GetBucket` read pattern
- [IMPL-0002: Phase 2 implementation plan](../impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — cluster `AddBucketAlias` / `RemoveBucketAlias` already exist;
  §Decisions #2 captures the last-alias-removal refusal
- [Garage admin v2 API: alias endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

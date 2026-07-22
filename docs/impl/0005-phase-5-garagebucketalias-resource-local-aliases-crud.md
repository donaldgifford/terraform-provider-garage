---
id: IMPL-0005
title: "Phase 5 bucket alias resources: standalone global + local, remove inline aliases"
status: Draft
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0005: Phase 5 bucket alias resources: standalone global + local, remove inline aliases

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-20

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Client wrapper — key-scoped alias methods](#phase-1-client-wrapper--key-scoped-alias-methods)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Live-API behavior verifications](#phase-2-live-api-behavior-verifications)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Resource scaffold](#phase-3-resource-scaffold)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Create + Read](#phase-4-create--read)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Delete + ImportState](#phase-5-delete--importstate)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Remove the bucket's inline alias attribute](#phase-6-remove-the-buckets-inline-alias-attribute)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: Acceptance suite + docs](#phase-7-acceptance-suite--docs)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute Phase 5 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) per
[DESIGN-0005](../design/0005-phase-5-implementation-garagebucketalias-resource.md).
Outcome: two standalone alias resources —
`garage_bucket_global_alias` (cluster-wide) and
`garage_bucket_local_alias` (key-scoped) — with full lifecycle, import,
drift, and acceptance tests, plus **removal of the inline `global_aliases`
attribute from the shipped `garage_bucket` resource** (a breaking change)
so each alias has a single owning resource.

**Implements:** DESIGN-0005, which scopes Phase 5 of RFC-0001.

> **Breaking change.** This plan removes `global_aliases` from
> `garage_bucket` (Phase 2). It is sequenced so both new resources ship
> fully working (Phases 1–5) *before* the removal (Phase 6), and it
> carries a migration guide + a new ADR. Once inline aliases are gone,
> `garage_bucket_global_alias` is the only way to give a bucket a
> cluster name — so it is required, not optional.

## Scope

### In Scope

- 2 new `*Client` methods for the key-scoped flavor:
  `AddBucketLocalAlias` / `RemoveBucketLocalAlias` (the cluster flavor
  already exists from IMPL-0002 Phase 1)
- `internal/resources/bucketalias/` implementing **two** resources —
  `garage_bucket_global_alias` and `garage_bucket_local_alias` — each
  Create / Read / Delete / ImportState (no Update; all attributes
  `RequiresReplace`)
- HCL: global = `bucket_id` + `alias`; local = `bucket_id` +
  `access_key_id` + `alias`; both with a computed `id`
- Last-alias-removal handling on the global resource so
  `terraform destroy` of a bucket + its aliases works
- **Removal of `global_aliases` from `garage_bucket`** + migration guide
  + a new ADR
- Acceptance tests: both resources, drift, import,
  destroy-with-sole-alias, and the migration path
- `examples/` + regenerated docs for both resources

### Out of Scope

- A single unified `garage_bucket_alias` with optional key — rejected in
  favor of two explicit resources
- Keeping inline aliases on the bucket — removed
- Cross-resource plan-time duplicate detection — not possible in
  Terraform; solved by single-source-of-truth
- Per-key `allow_create_bucket` / bucket-key permission edges — Phase 4

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phases land as
separate commits on `feat/design-0005-phase-5-alias-resources` and ship
as a single PR (Open Question 5 → a); the breaking removal (Phase 6)
lands only after Phases 1–5 are green.

---

### Phase 1: Client wrapper — key-scoped alias methods

Add the key-scoped (`BucketAliasEnum1`) flavor. Cluster methods already
exist. Pure unit-testable work.

#### Tasks

- [ ] Add `func (c *Client) AddBucketLocalAlias(ctx context.Context, bucketID, accessKeyID, localAlias string) error`
      — POST `/v2/AddBucketAlias`; body from `BucketAliasEnum1` via the
      generated union constructor (mirror `AddBucketAlias`'s
      `BucketAliasEnum0`)
- [ ] Add `func (c *Client) RemoveBucketLocalAlias(ctx context.Context, bucketID, accessKeyID, localAlias string) error`
      — POST `/v2/RemoveBucketAlias`; same `BucketAliasEnum1` body
- [ ] Empty-arg guards for `bucketID` / `accessKeyID` / `localAlias`
- [ ] Unit tests in `internal/client/client_test.go`:
  - Happy path serializes `BucketAliasEnum1` (asserts `accessKeyId` +
    `localAlias`, not `globalAlias`)
  - 404 → `ErrNotFound`; 401 / 403 → auth sentinels
  - Last-alias HTTP 400 body preserved on `APIError.Message` (needed by
    the global resource's Delete handling)
  - Empty-arg guards short-circuit
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: add key-scoped bucket-alias client wrappers (IMPL-0005 Phase 1)`

#### Success Criteria

- Both methods exist with doc comments referencing `BucketAliasEnum1`
- Client-package coverage stays ≥80%; `just lint` / `just test` exit 0
- Generated client untouched

---

### Phase 2: Live-API behavior verifications

Resolve the alias unknowns against a live `dxflrs/garage:v2.3.0`
container. Probes live in `internal/client/livecheck_test.go`
(`//go:build garageprobe`), runnable via
`go test -tags=garageprobe -run TestLiveAlias ./internal/client/...`.

#### Tasks

- [ ] **Verification A — Local round-trip.** `AddBucketLocalAlias`, then
      `GetBucket(bucketID).Keys[]` → find the key →
      `BucketLocalAliases[]` contains the alias
- [ ] **Verification B — Read parity.** Cross-check
      `GetBucket.Keys[].BucketLocalAliases[]` vs
      `GetKeyInfo(accessKeyID).Buckets[].LocalAliases[]`; confirm
      agreement. `GetBucket` is authoritative
- [ ] **Verification C — Last-alias removal (global).** Confirm the
      IMPL-0002 finding still holds (HTTP 400 `"...please delete it
      instead of just unaliasing."`); capture the exact message for the
      global resource's Delete diagnostic
- [ ] **Verification D — Last-alias removal (local).** Determine whether
      removing a key's only local alias for a bucket has the same
      restriction (likely unrestricted). Document
- [ ] **Verification E — Deleted-key GC.** Delete a `garage_key`
      out-of-band that has a local alias; confirm whether Garage GCs the
      alias and what `GetBucket.Keys[]` shows. Confirm Read →
      `RemoveResource`
- [ ] **Verification F — Idempotency.** Add the same local alias twice →
      2xx no-op (mirrors the cluster finding)
- [ ] Fold findings into this doc's §Decisions and finalize DESIGN-0005;
      commit the doc + `livecheck_test.go` together

#### Success Criteria

- All six verifications documented with observed status codes / bodies /
  lifecycle implications
- The global Delete last-alias handling (Phase 5) and the local
  deleted-key drift path are unblocked with concrete behavior
- `livecheck_test.go` probes pass under `-tags=garageprobe`

---

### Phase 3: Resource scaffold

`internal/resources/bucketalias/` with both resources' boilerplate. No
lifecycle logic yet; not registered until Phase 4. One package holds both
resources (Open Question 3).

#### Tasks

- [ ] Create `internal/resources/bucketalias/` package
- [ ] Write `global.go` — `garage_bucket_global_alias`:
  - `Resource` struct + `NewGlobal()` ctor
  - `Metadata()` = `req.ProviderTypeName + "_bucket_global_alias"`
  - `Configure()` asserts `*client.ProviderData`, pulls `.Client`
  - `Schema()`: `id` (Computed, `UseStateForUnknown`); `bucket_id`,
    `alias` (Required, `RequiresReplace`)
  - Lifecycle stubbed; `Update()` a defensive no-op
  - Interface assertions (`Resource`, `ResourceWithConfigure`,
    `ResourceWithImportState`)
- [ ] Write `local.go` — `garage_bucket_local_alias`:
  - Same shape; `Metadata()` = `..._bucket_local_alias`; `Schema()` adds
    `access_key_id` (Required, `RequiresReplace`)
- [ ] Write `model.go`:
  - `GlobalAliasModel` / `LocalAliasModel`
  - `globalID` / `localID`; `parseGlobalID` (split first `/`) /
    `parseLocalID` (split first two `/`) — aliases may contain `/`
  - `findGlobalAlias(*openapi.GetBucketInfoResponse, alias)` — checks
    `GlobalAliases[]`
  - `findLocalAlias(*openapi.GetBucketInfoResponse, accessKeyID, alias)`
    — finds the key in `Keys[]`, checks `BucketLocalAliases[]`
- [ ] Unit tests in `model_test.go` — id round-trip + parse for both
      (including aliases containing `/`), `findGlobalAlias` /
      `findLocalAlias` hit/miss
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: scaffold bucket alias resources (IMPL-0005 Phase 3)`

#### Success Criteria

- `internal/resources/bucketalias/` compiles with both resources
- Schemas match DESIGN-0005; MarkdownDescription strings present,
  including the single-source-of-truth note
- Model helpers unit-tested; neither resource yet registered

---

### Phase 4: Create + Read

Implement Create + Read for both resources and register them.

#### Tasks

- [ ] Implement `garage_bucket_global_alias` `Create()` /
      `Read()`:
  - Create: `AddBucketAlias(bucket_id, alias)`; set `id = globalID`;
    refresh via `GetBucket`
  - Read: `GetBucket`; `ErrNotFound` or `!findGlobalAlias` →
    `RemoveResource`
- [ ] Implement `garage_bucket_local_alias` `Create()` / `Read()`:
  - Create: `AddBucketLocalAlias(bucket_id, access_key_id, alias)`; set
    `id = localID`; refresh via `GetBucket`
  - Read: `GetBucket`; `ErrNotFound` or `!findLocalAlias` →
    `RemoveResource`
  - Rollback on refresh failure: best-effort remove + `tflog.Warn`
- [ ] Register both in `provider.Resources()`
- [ ] Acceptance test `TestAccGarageBucketGlobalAlias_basic` — create,
      verify state + `GetBucket.GlobalAliases[]`. `t.Parallel()`
- [ ] Acceptance test `TestAccGarageBucketLocalAlias_basic` — create with
      a key, verify state + `Keys[].BucketLocalAliases[]`
- [ ] Acceptance tests `_driftDetection` for both — external removal in a
      `PreConfig` hook, re-apply re-adds
- [ ] Run `just lint` / `just test` / `TF_ACC=1 go test ./internal/resources/bucketalias/`
- [ ] Commit as `feat: bucket alias Create + Read (IMPL-0005 Phase 4)`

#### Success Criteria

- Both `_basic` tests pass; drift detected and corrected for both
- Existing suites still pass (`garage_bucket` keeps `global_aliases`
  until Phase 6)

---

### Phase 5: Delete + ImportState

Delete both; last-alias handling on the global resource; parse ids.

#### Tasks

- [ ] Implement `garage_bucket_global_alias` `Delete()`:
  - `RemoveBucketAlias(bucket_id, alias)`
  - **Last-alias handling (OQ2):** on Garage's last-alias HTTP 400,
    `GetBucket` — bucket gone → success; bucket present → `tflog.Warn`
    and remove from state (rely on `DeleteBucket`)
  - `ErrNotFound` → silent
- [ ] Implement `garage_bucket_local_alias` `Delete()`:
  - `RemoveBucketLocalAlias(bucket_id, access_key_id, alias)`;
    `ErrNotFound` → silent (per Verification D, no last-alias restriction
    expected)
- [ ] Implement `ImportState()` for both:
  - global: `parseGlobalID` (split first `/`)
  - local: `parseLocalID` (split first two `/`)
  - error with an actionable message on a malformed id; `Read` confirms
    existence
- [ ] Acceptance tests `_import` for both flavors (incl. an alias
      containing `/` to prove parsing)
- [ ] Acceptance test `TestAccGarageBucketGlobalAlias_destroySoleAlias` —
      a bucket whose only alias is a `garage_bucket_global_alias`;
      `terraform destroy` of both succeeds (exercises lenient last-alias)
- [ ] Acceptance tests `_rename` for both — `alias` change forces replace
- [ ] Run all gates green
- [ ] Commit as `feat: bucket alias Delete + ImportState (IMPL-0005 Phase 5)`

#### Success Criteria

- Both resources delete cleanly; `GetBucket` confirms removal
- `terraform destroy` of a bucket + its sole global alias succeeds
- Import round-trips for both (including `/`-containing aliases);
  malformed ids error clearly

---

### Phase 6: Remove the bucket's inline alias attribute

The breaking change. With both resources proven (Phases 1–5), remove
`global_aliases` from `garage_bucket`. Ship migration guide + ADR.

#### Tasks

- [ ] Confirm [ADR-0008](../adr/0008-remove-inline-globalaliases-in-favor-of-standalone-alias.md)
      (written during planning — records the breaking decision:
      single-source-of-truth, Terraform-can't-cross-validate,
      two-standalone-resource pattern, pre-1.0 break) is still accurate,
      and reference it from the changelog / upgrade note
- [ ] Remove the `global_aliases` attribute from
      `internal/resources/bucket/resource.go`
- [ ] Remove the alias-diff logic + `diffGlobalAliases` helper +
      `AddBucketAlias` / `RemoveBucketAlias` calls from the bucket's
      Create / Update (client methods stay — the alias resources use
      them). Bucket Create becomes `CreateBucket` (+ quotas) only
- [ ] Update the bucket `Model` and `applyBucketInfoToModel` to drop
      `GlobalAliases`
- [ ] Migrate / remove bucket acceptance tests asserting on
      `global_aliases` (`_updateAliases`, `_aliasReorderNoOp`, the alias
      arm of `_driftDetection`, `_createWithAliasesAndQuotas`, `_import`)
      into the `bucketalias` suite
- [ ] Add `TestAccGarageBucketGlobalAlias_migrationFromInline` — an alias
      created out-of-band on a bucket, then imported as a
      `garage_bucket_global_alias`; plan is clean
- [ ] Write the migration guide (ADR + `docs/resources/bucket.md` upgrade
      note): for each old `global_aliases` entry, add a
      `garage_bucket_global_alias` and `terraform import
      <bucket_id>/<alias>`, then drop `global_aliases`
- [ ] `just generate`; confirm `docs/resources/bucket.md` no longer lists
      `global_aliases`
- [ ] Run all gates green
- [ ] Commit as `feat!: remove inline global_aliases from garage_bucket (IMPL-0005 Phase 6)`

#### Success Criteria

- `garage_bucket` no longer exposes `global_aliases`; its Create/Update
  make no alias calls
- Alias coverage previously on the bucket suite now lives in the
  `bucketalias` suite; all green
- Migration guide exists and the migration acceptance test passes
- The ADR is written and linked from DESIGN-0005
- The commit is marked breaking (`feat!:`) for the version bump

---

### Phase 7: Acceptance suite + docs

Polish. Generate docs; CI green.

#### Tasks

- [ ] Write `examples/resources/garage_bucket_global_alias/{resource.tf,import.sh}`
      and `examples/resources/garage_bucket_local_alias/{resource.tf,import.sh}`
- [ ] Run `just generate`; verify `docs/resources/bucket_global_alias.md`
      and `bucket_local_alias.md` render cleanly, and
      `docs/resources/bucket.md` reflects the removal
- [ ] Add `_parallelSafety` for both — `count = 3` distinct aliases;
      concurrent apply
- [ ] Re-run `just generate`; verify zero diff (idempotent)
- [ ] Full alias suite passes under `TF_ACC=1`; wall time under budget
- [ ] CI green on the PR (lint + test + acceptance + `generate` no-diff)
- [ ] Commit final docs/test additions as
      `feat: bucket alias acceptance polish + docs (IMPL-0005 Phase 7)`

#### Success Criteria

- Both resources' docs exist and reflect their schemas
- Examples render cleanly; `just generate` idempotent
- All alias acceptance tests pass; CI green on the PR

---

## File Changes

| File                                                    | Action | Notes                                                       |
|---------------------------------------------------------|--------|-------------------------------------------------------------|
| `internal/client/client.go`                             | Modify | Phase 1 — add `AddBucketLocalAlias` / `RemoveBucketLocalAlias` |
| `internal/client/client_test.go`                        | Modify | Phase 1 — unit tests                                        |
| `internal/client/livecheck_test.go`                     | Modify | Phase 2 — `TestLiveAlias*` probes (Verif. A–F)             |
| `internal/resources/bucketalias/global.go`              | Create | Phase 3 — `garage_bucket_global_alias`                     |
| `internal/resources/bucketalias/local.go`               | Create | Phase 3 — `garage_bucket_local_alias`                      |
| `internal/resources/bucketalias/model.go`               | Create | Phase 3 — shared id/find helpers                           |
| `internal/resources/bucketalias/*_test.go`              | Create | Phase 3/4 — unit + acceptance suites                       |
| `internal/resources/bucket/resource.go`                 | Modify | Phase 6 — remove `global_aliases` + alias-diff logic       |
| `internal/resources/bucket/model.go`                    | Modify | Phase 6 — drop `GlobalAliases`                             |
| `internal/resources/bucket/resource_test.go`            | Modify | Phase 6 — migrate/remove alias assertions                  |
| `internal/provider/provider.go`                         | Modify | Phase 4 — register both alias resources                    |
| `docs/adr/0008-remove-inline-globalaliases-in-favor-of-standalone-alias.md` | Create | ADR-0008 — breaking-change record (written during planning) |
| `examples/resources/garage_bucket_global_alias/*`       | Create | Phase 7 — usage + import examples                          |
| `examples/resources/garage_bucket_local_alias/*`        | Create | Phase 7 — usage + import examples                          |
| `docs/resources/bucket_global_alias.md`                 | Create | Phase 7 — generated                                        |
| `docs/resources/bucket_local_alias.md`                  | Create | Phase 7 — generated                                        |
| `docs/resources/bucket.md`                              | Modify | Phase 6/7 — regenerated without `global_aliases`          |
| `docs/design/0005-*.md`                                 | Modify | Phase 2 — fold in live findings                            |
| `docs/impl/0005-*.md`                                   | Modify | Each phase — check off tasks                               |

## Testing Plan

| Layer         | Where                                                     | When    |
|---------------|-----------------------------------------------------------|---------|
| Unit          | `internal/client/client_test.go` (2 key-scoped methods)   | Phase 1 |
| Unit          | `internal/resources/bucketalias/model_test.go`            | Phase 3 |
| Live probe    | `internal/client/livecheck_test.go` (tagged)              | Phase 2 |
| Acceptance    | `internal/resources/bucketalias/*_test.go`                | 4–7     |
| Acceptance    | `internal/resources/bucket/resource_test.go` (migration)  | Phase 6 |
| CI acceptance | matrix over the pinned TF / OpenTofu versions             | Phase 7 |
| Docs drift    | `generate` job in CI (no-diff gate)                       | Phase 6/7 |

## Dependencies

- **Phase 3 of RFC-0001 (DESIGN-0003 / IMPL-0003)** — provides
  `garage_key` (`garage_bucket_local_alias` references `access_key_id`)
- **Phase 2 of RFC-0001 (DESIGN-0002 / IMPL-0002)** — provides
  `garage_bucket`, the cluster `AddBucketAlias` / `RemoveBucketAlias`
  wrappers, the `BucketAliasEnum` union, and the last-alias finding.
  Phase 6 modifies this shipped resource
- **External:** Docker daemon for acceptance tests
- **Garage upstream:** local-alias read path + last-alias semantics
  (Verifications A–F) confirmed before Delete finalizes

## Decisions

Finalized from DESIGN-0005 (2026-07-21 — all open questions resolved to
option (a); see the resolution block below). Live behavior is confirmed
in Phase 2's verifications.

1. **Two standalone resources, no inline.** `garage_bucket_global_alias`
   + `garage_bucket_local_alias`; `global_aliases` removed from
   `garage_bucket` (Phase 6). Single owner per alias; modern canonical
   pattern.
2. **Explicit resources over a unified optional-key resource** —
   unambiguous 2-vs-3-segment ids, aliases may contain `/`.
3. **All attributes `RequiresReplace`; no Update.**
4. **`garage_bucket_global_alias` is required, not optional** — the only
   way to name a bucket once inline is gone.
5. **Lenient last-alias Delete on the global resource** (OQ2 → a) so
   `terraform destroy` works.

## Open Questions

Mirror of DESIGN-0005's open questions. Each option is lettered — **a is
the recommendation**, b+ are alternatives, or supply your own under
*other*.

**Resolution (2026-07-21): all resolved to option (a).** OQ1 hard-remove
`global_aliases` + migration guide +
[ADR-0008](../adr/0008-remove-inline-globalaliases-in-favor-of-standalone-alias.md);
OQ2 lenient global-alias `Delete`; OQ3 one `bucketalias` package; OQ4
missing key/alias on Read → `RemoveResource`; OQ5 single PR. Options
retained below for the record.

1. **`global_aliases` removal mechanics.**
   - **a. (Recommended)** Hard-remove in one release + migration guide +
     new ADR (pre-1.0 permits the break).
   - b. Deprecate one release (warn + read-only) then remove.
   - c. Keep it as a read-only computed mirror.
   - *other:* ______

2. **Last-alias-removal / destroy ordering.**
   - **a. (Recommended)** Lenient global-alias Delete — on last-alias
     HTTP 400, `GetBucket`; bucket gone → success, present → warn +
     remove from state. Keeps `destroy` working.
   - b. Strict — surface the HTTP 400 (may deadlock `terraform destroy`).
   - c. Keep a bucket-owned "primary" alias inline (partial revert of
     Decision 1).
   - *other:* ______

3. **One package or two.**
   - **a. (Recommended)** One `bucketalias` package holding both
     resources (shared read + id helpers), registered separately.
   - b. Two packages (`bucketglobalalias` / `bucketlocalalias`) to match
     the one-resource-per-package convention.
   - *other:* ______

4. **Local-alias behavior on a deleted key.**
   - **a. (Recommended)** Missing key/alias on Read → `RemoveResource`
     (Garage likely GCs the local alias); confirm in Phase 2 livecheck.
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
  — the breaking-change decision Phase 6 executes
- [DESIGN-0005: Phase 5 implementation — standalone bucket alias resources](../design/0005-phase-5-implementation-garagebucketalias-resource.md)
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 5 entry
- [DESIGN-0002: Phase 2 garage_bucket resource](../design/0002-phase-2-implementation-garagebucket-resource.md)
  — the `global_aliases` inline attribute this plan removes (Phase 6)
- [DESIGN-0003: Phase 3 garage_key resource](../design/0003-phase-3-implementation-garagekey-resource.md)
  — provides `access_key_id`
- [IMPL-0002: Phase 2 implementation plan](0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — cluster `AddBucketAlias` / `RemoveBucketAlias` exist; §Decisions #2
  captures the last-alias-removal refusal; Phase 6 here modifies this work
- [IMPL-0003](0003-phase-3-garagekey-resource-client-wrapper-dual-secretsource.md)
  / [IMPL-0004](0004-phase-4-garagebucketkey-resource-permission-edge-crud-drift.md)
  — land first; provide `garage_key` and the shared `GetBucket` read pattern
- [Garage admin v2 API: alias endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

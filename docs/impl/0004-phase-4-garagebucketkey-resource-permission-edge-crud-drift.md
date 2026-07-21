---
id: IMPL-0004
title: "Phase 4 garage_bucket_key resource: permission-edge CRUD, drift, acceptance suite"
status: Draft
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0004: Phase 4 garage_bucket_key resource: permission-edge CRUD, drift, acceptance suite

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-19

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Client wrapper — DenyBucketKey](#phase-1-client-wrapper--denybucketkey)
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
  - [Phase 5: Update](#phase-5-update)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: Delete + ImportState](#phase-6-delete--importstate)
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

Execute Phase 4 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) per
[DESIGN-0004](../design/0004-phase-4-implementation-garagebucketkey-resource.md).
Outcome: a `garage_bucket_key` resource — a Terraform-managed permission
edge between a `garage_bucket` and a `garage_key`, carrying the
`(owner, read, write)` triple — with full CRUD, drift detection, import,
and acceptance tests, all green on a real Garage container in CI.

**Implements:** DESIGN-0004, which scopes Phase 4 of RFC-0001.

> **Decisions locked (2026-07-21, final).**
> DESIGN-0004 was a sketch; its shape decisions are now set to the
> recommended defaults: Create = defensive `AllowBucketKey` +
> `DenyBucketKey`, read = `GetBucket.Keys[]` authoritative, id =
> slash-separated. Only the *live behavior* (union-vs-set, read parity)
> is still confirmed empirically in Phase 2's verifications. See
> [Open Questions](#open-questions) to override.

## Scope

### In Scope

- 1 new `*Client` method: `DenyBucketKey` (`AllowBucketKey` already
  exists from IMPL-0002 Phase 6)
- `internal/resources/bucketkey/` package implementing the Plugin
  Framework resource lifecycle (Create / Read / Update / Delete /
  ImportState)
- HCL surface: `bucket_id` (Required, RequiresReplace), `access_key_id`
  (Required, RequiresReplace), `owner` / `read` / `write`
  (Optional+Computed, default false), computed `id`
  (`<bucket_id>/<access_key_id>`)
- Truth-table diff on update: `AllowBucketKey` for additions,
  `DenyBucketKey` for removals
- Drift detection: external permission flips surface in `plan`; deleted
  bucket/key removes the edge from state
- Acceptance tests over the meaningful permission-transition subset
  (not the full 8-cell table)
- `examples/resources/garage_bucket_key/{resource.tf,import.sh}` and
  regenerated `docs/resources/bucket_key.md`
- Live verifications for DESIGN-0004 Open Questions 1 and 2 (OQ4 drift is
  a light confirm of the already-proven `garage_bucket` pattern)

### Out of Scope

- Per-key `allow_create_bucket` — that's `garage_key.allow_create_bucket`
  (Phase 3), set via `UpdateKey`; not part of the bucket-key edge
- Inline `permissions` block on `garage_bucket` or `garage_key` — the
  edge is its own resource so the (n × m) relationship doesn't inflate
  either side's schema
- Bulk grant/revoke — Garage's API targets one (bucket, key) pair per
  call
- Local (per-key) bucket aliases — Phase 5 / IMPL-0005

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phases land as
separate commits on `feat/design-0004-phase-4-bucket-key-resource` and
ship as a single PR (Open Question 5 → a).

---

### Phase 1: Client wrapper — DenyBucketKey

Add the one missing admin method. `AllowBucketKey` already exists
(`client.go`, added in IMPL-0002 Phase 6 to grant the fixture key access
on Terraform-managed buckets). Pure unit-testable work.

#### Tasks

- [ ] Add `func (c *Client) DenyBucketKey(ctx context.Context, bucketID, accessKeyID string, perms openapi.ApiBucketKeyPerm) error`
      — POST `/v2/DenyBucketKey`; body is
      `BucketKeyPermChangeRequest{AccessKeyId, BucketId, Permissions}`,
      mirroring the existing `AllowBucketKey` signature exactly (inverts
      intent, not shape)
- [ ] Empty-arg validation gate for `bucketID` / `accessKeyID`, matching
      `AllowBucketKey`
- [ ] Confirm the retry posture: both `AllowBucketKey` and
      `DenyBucketKey` are POST → not retried by `retryingTransport`
      (idempotent by *effect* but the transport only auto-retries
      GET/HEAD; the resource's own re-issue on drift covers correctness)
- [ ] Unit tests in `internal/client/client_test.go`:
  - Happy path issues the expected body / method / path
  - 404 maps to `ErrNotFound`; 401 / 403 map to the auth sentinels
  - `perms` with mixed true/false flags round-trips through the request
    body
  - Empty-arg guards return an error without issuing a request
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: add DenyBucketKey client wrapper (IMPL-0004 Phase 1)`

#### Success Criteria

- `DenyBucketKey` exists with a doc comment mirroring `AllowBucketKey`
- Client-package coverage stays ≥80%
- `just lint` / `just test` exit 0
- Generated client untouched (consumes existing
  `BucketKeyPermChangeRequest` / `ApiBucketKeyPerm`)

---

### Phase 2: Live-API behavior verifications

Resolve DESIGN-0004's Open Questions against a live `dxflrs/garage:v2.3.0`
container before writing lifecycle code. Probes live in
`internal/client/livecheck_test.go` (`//go:build garageprobe`), runnable
via `go test -tags=garageprobe -run TestLiveBucketKey ./internal/client/...`.

#### Tasks

- [ ] **Verification A — Create semantics (DESIGN-0004 OQ1).** Does
      `AllowBucketKey({owner:false, read:true, write:true})` land exactly
      `(false, true, true)`, or does it *union* with whatever's already
      set on the edge? Set an edge to all-true, then `AllowBucketKey`
      with some flags false, and read back. Determines whether Create
      needs a `DenyBucketKey` follow-up to clear false flags
- [ ] **Verification B — Read source-of-truth (DESIGN-0004 OQ2).** Set
      perms via `AllowBucketKey`, then read the edge from both
      `GetBucket(bucketID).Keys[]` (search for `accessKeyId`,
      `.Permissions`) and `GetKeyInfo(accessKeyID).Buckets[]` (search
      for `bucketId`, `.Permissions`). Confirm they agree. Pick
      `GetBucket.Keys[]` as authoritative (recommended); document the
      drift case if they diverge
- [ ] **Verification C — `DenyBucketKey` idempotency.** Deny the same
      flags twice; expect a 2xx no-op (mirrors `AllowBucketKey` from
      IMPL-0002 Phase 6). Confirms safe re-issue on Create-rollback /
      drift correction
- [ ] **Verification D — Delete / zombie-row behavior.**
      `DenyBucketKey({owner:true, read:true, write:true})` to strip all
      perms; then `GetBucket(bucketID).Keys[]` — does the key vanish from
      `Keys[]`, or remain with all-false permissions? Determines how Read
      distinguishes "edge deleted" from "edge with no perms" (both should
      map to `RemoveResource`)
- [ ] **Verification E — Deleted bucket/key drift (light confirm).** The
      `Read`→`ErrNotFound`→`RemoveResource` pattern is already proven for
      `garage_bucket` (IMPL-0002; DESIGN-0004 OQ4 resolved-by-precedent).
      This is a confirmatory acceptance step that the edge reuses it
      cleanly (no misleading "will be destroyed" plan) — not a new live
      probe
- [ ] Confirm the `access_key_id` component never contains `/`
      (DESIGN-0004 OQ3): bucket ids are already confirmed `/`-free
      (IMPL-0002, 64-char hex); assert the same for a live `access_key_id`
- [ ] Fold observed behavior into this doc's §Decisions and finalize
      DESIGN-0004's Detailed Design; commit the doc + `livecheck_test.go`
      together

#### Success Criteria

- Verifications A–D documented with observed status codes / body shapes;
  E confirmed to follow the proven `garage_bucket` drift pattern
- The Create path (Verification A), Read path (B/D), and drift handling
  (E) are unblocked with concrete resolutions
- `livecheck_test.go` probes pass under `-tags=garageprobe`
- Confirmed: neither id component contains `/`, so the slash-separated
  synthetic id parses unambiguously

---

### Phase 3: Resource scaffold

`internal/resources/bucketkey/` package with the framework boilerplate.
No lifecycle logic yet; not registered in `provider.go` until Phase 4.

#### Tasks

- [ ] Create `internal/resources/bucketkey/` package (package name
      `bucketkey`, ST1003-clean)
- [ ] Write `resource.go`:
  - `type Resource struct { client *client.Client }` + `New()` ctor
  - `Metadata()` sets `req.ProviderTypeName + "_bucket_key"`
  - `Configure()` asserts `*client.ProviderData`, pulls `.Client`
  - `Schema()` per DESIGN-0004 §Resource shape: `id` (Computed,
    `UseStateForUnknown`); `bucket_id` (Required, `RequiresReplace`);
    `access_key_id` (Required, `RequiresReplace`); `owner` / `read` /
    `write` (Optional+Computed, `Default(false)`)
  - Lifecycle methods stubbed with "not yet implemented" diagnostics
  - Compile-time interface assertions (`Resource`,
    `ResourceWithConfigure`, `ResourceWithImportState`)
- [ ] Write `model.go`:
  - `Model` struct (`ID`, `BucketID`, `AccessKeyID`, `Owner`, `Read`,
    `Write`)
  - `syntheticID(bucketID, accessKeyID string) string` →
    `bucketID + "/" + accessKeyID` (per Open Question 3 resolution)
  - `permsFromModel(*Model) openapi.ApiBucketKeyPerm` and
    `applyPermsToModel(openapi.ApiBucketKeyPerm, *Model)`
  - `findEdge(*openapi.GetBucketInfoResponse, accessKeyID string)
    (*openapi.GetBucketInfoKey, bool)` — searches `resp.Keys[]`
    (the authoritative read path per Verification B)
- [ ] Unit tests in `model_test.go` — id round-trip, perm ↔ model
      mapping (all 8 flag combinations), `findEdge` hit/miss
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: scaffold garage_bucket_key resource package (IMPL-0004 Phase 3)`

#### Success Criteria

- `internal/resources/bucketkey/` compiles
- Schema matches DESIGN-0004 §Resource shape; MarkdownDescription
  strings present, including the cross-resource note (edge lives here,
  not on `garage_key`)
- Model helpers unit-tested; resource **not** yet registered

---

### Phase 4: Create + Read

Implement the two simplest lifecycle methods and register the resource.

#### Tasks

- [ ] Implement `Create()` per DESIGN-0004 §Lifecycle mapping and
      Verification A's resolution:
  1. `AllowBucketKey({bucketID, accessKeyID, perms: true-flags})`
  2. If Verification A showed `AllowBucketKey` unions (rather than
     sets), issue a `DenyBucketKey` for the false flags so the edge
     lands exactly at the desired triple. If it sets, skip this
  3. Set `id = syntheticID(...)`; refresh from `GetBucket` (authoritative
     read) and write state
  - Rollback: on failure after a partial grant, best-effort
    `DenyBucketKey` all-flags + `tflog.Warn`
- [ ] Implement `Read()`:
  - `GetBucket(bucket_id)`; on `ErrNotFound`: `RemoveResource`
  - `findEdge(resp, access_key_id)`; if the key is absent from `Keys[]`
    (or present with all-false per Verification D): `RemoveResource`
  - Otherwise `applyPermsToModel` and write state
- [ ] Register the resource in `provider.Resources()`
- [ ] Acceptance test `TestAccGarageBucketKey_readWrite` — create a
      bucket + key (via the Phase 2/3 resources), grant `read+write`,
      verify state. `t.Parallel()`
- [ ] Run `just lint` / `just test` / `TF_ACC=1 go test ./internal/resources/bucketkey/`
      — verify all green
- [ ] Commit as `feat: garage_bucket_key Create + Read (IMPL-0004 Phase 4)`

#### Success Criteria

- `TestAccGarageBucketKey_readWrite` passes against a live container
- The edge lands at exactly the configured `(owner, read, write)` triple
  (Verification A's clear-false-flags handling confirmed)
- `garage_bucket` / `garage_key` acceptance suites still pass

---

### Phase 5: Update

Diff the desired triple against current state; apply the delta.

#### Tasks

- [ ] Implement `Update()`:
  - Compute per-flag delta: flags flipping false→true → `AllowBucketKey`
    with those flags; flags flipping true→false → `DenyBucketKey` with
    those flags
  - Issue at most one `AllowBucketKey` and one `DenyBucketKey` (batch the
    additions and removals rather than one call per flag)
  - Refresh via `GetBucket` and write state
- [ ] Acceptance test `TestAccGarageBucketKey_updatePermissions` —
      read-only → read+write → revoke write; assert each transition via
      `GetBucket.Keys[].Permissions`
- [ ] Acceptance test `TestAccGarageBucketKey_ownerAlone` — set
      `owner = true` with `read`/`write` false; assert the flag lands.
      (IMPL-0002 Phase 6 found `owner` alone does *not* grant data-plane
      read/write — this documents the edge case, no data-plane assertion
      needed)
- [ ] Acceptance test `TestAccGarageBucketKey_driftDetection` — flip a
      flag out-of-band via direct `*client.Client.DenyBucketKey` in a
      `PreConfig` hook, then re-apply same HCL; plan detects and corrects
- [ ] Run all gates green
- [ ] Commit as `feat: garage_bucket_key Update lifecycle (IMPL-0004 Phase 5)`

#### Success Criteria

- All three permission-transition acceptance tests pass
- Drift on any single flag is detected and corrected on the next apply
- `owner`-alone lands as configured (behavior documented)

---

### Phase 6: Delete + ImportState

Strip all perms on delete; parse the synthetic id on import.

#### Tasks

- [ ] Implement `Delete()`:
  - `DenyBucketKey({bucketID, accessKeyID, perms:{owner:true, read:true, write:true}})`
    to strip all permissions (there's no dedicated delete-edge endpoint)
  - On `ErrNotFound` (bucket/key already gone): return silently
- [ ] Implement `ImportState()`:
  - Parse `req.ID` on the first `/` into `bucket_id` / `access_key_id`
    (exactly two components; error with an actionable message otherwise)
  - Set both attributes + `id`; the subsequent `Read` fills `owner` /
    `read` / `write` from `GetBucket`
- [ ] Acceptance test `TestAccGarageBucketKey_delete` — add an explicit
      step that asserts the edge is gone from `GetBucket.Keys[]` after
      destroy (teardown already exercises Delete; this pins the
      post-condition)
- [ ] Acceptance test `TestAccGarageBucketKey_import` — create an edge
      with `read+write`, then `ImportStateVerify` against
      `<bucket_id>/<access_key_id>`
- [ ] Run all gates green
- [ ] Commit as `feat: garage_bucket_key Delete + ImportState (IMPL-0004 Phase 6)`

#### Success Criteria

- Delete strips all perms; a follow-up `GetBucket` shows the key gone
  from `Keys[]` (or all-false per Verification D)
- Import by `<bucket_id>/<access_key_id>` round-trips; malformed ids
  produce an actionable error
- `TestAccGarageBucketKey_import` passes

---

### Phase 7: Acceptance suite + docs

Polish phase. Fill out the meaningful test subset; generate docs; CI
green.

#### Tasks

- [ ] Write `examples/resources/garage_bucket_key/resource.tf` —
      references `garage_bucket` + `garage_key` resources, grants
      `read+write`; matches the existing examples convention
- [ ] Write `examples/resources/garage_bucket_key/import.sh`
- [ ] Run `just generate`; verify `docs/resources/bucket_key.md` renders
      cleanly
- [ ] Add `TestAccGarageBucketKey_revokeAll` — read+write → all-false;
      assert the edge is removed from state
- [ ] Add `TestAccGarageBucketKey_parallelSafety` — `count = 3` distinct
      edges on distinct keys against one bucket; concurrent apply
- [ ] Re-run `just generate`; verify zero diff (idempotent)
- [ ] Ensure the full `TestAccGarageBucketKey_*` suite passes under
      `TF_ACC=1`; total wall time comfortably under the CI timeout
- [ ] CI green on the PR (lint + test + acceptance + `generate` no-diff)
- [ ] Commit final docs/test additions as
      `feat: garage_bucket_key acceptance polish + docs (IMPL-0004 Phase 7)`

#### Success Criteria

- `docs/resources/bucket_key.md` exists and reflects the schema
- `examples/resources/garage_bucket_key/resource.tf` renders cleanly
- All `TestAccGarageBucketKey_*` tests pass; `just generate` idempotent
- CI `acceptance` + `generate` jobs green on the PR

---

## File Changes

| File                                                  | Action | Notes                                                 |
|-------------------------------------------------------|--------|-------------------------------------------------------|
| `internal/client/client.go`                           | Modify | Phase 1 — add `DenyBucketKey`                         |
| `internal/client/client_test.go`                      | Modify | Phase 1 — unit tests for `DenyBucketKey`             |
| `internal/client/livecheck_test.go`                   | Modify | Phase 2 — `TestLiveBucketKey*` probes (Verif. A–E)   |
| `internal/resources/bucketkey/resource.go`            | Create | Phase 3 — Schema/Metadata/Configure + lifecycle      |
| `internal/resources/bucketkey/model.go`               | Create | Phase 3 — Model + id/perm helpers + `findEdge`       |
| `internal/resources/bucketkey/model_test.go`          | Create | Phase 3 — helper unit tests                          |
| `internal/resources/bucketkey/resource_test.go`       | Create | Phase 4 — acceptance suite (grows phase by phase)    |
| `internal/provider/provider.go`                       | Modify | Phase 4 — register `garage_bucket_key`               |
| `examples/resources/garage_bucket_key/resource.tf`    | Create | Phase 7 — usage example                              |
| `examples/resources/garage_bucket_key/import.sh`      | Create | Phase 7 — import command-line example                |
| `docs/resources/bucket_key.md`                        | Create | Phase 7 — generated by `tfplugindocs`               |
| `docs/design/0004-*.md`                               | Modify | Phase 2 — replace sketch placeholders with findings; finalize Detailed Design |
| `docs/impl/0004-*.md`                                 | Modify | Each phase — check off completed tasks               |

## Testing Plan

| Layer         | Where                                                    | When    |
|---------------|----------------------------------------------------------|---------|
| Unit          | `internal/client/client_test.go` (`DenyBucketKey`)       | Phase 1 |
| Unit          | `internal/resources/bucketkey/model_test.go`             | Phase 3 |
| Live probe    | `internal/client/livecheck_test.go` (tagged)             | Phase 2 |
| Acceptance    | `internal/resources/bucketkey/resource_test.go`          | 4–7     |
| CI acceptance | matrix over the pinned TF / OpenTofu versions            | Phase 7 |
| Docs drift    | `generate` job in CI (no-diff gate)                      | Phase 7 |

## Dependencies

- **Phase 3 of RFC-0001 (DESIGN-0003 / IMPL-0003)** — provides
  `garage_key` (this edge references `garage_key.access_key_id`) and the
  five key client methods
- **Phase 2 of RFC-0001 (DESIGN-0002 / IMPL-0002)** — provides
  `garage_bucket`, `AllowBucketKey`, the `GetBucketInfoKey` /
  `ApiBucketKeyPerm` types, and the resource-package pattern
- **External:** Docker daemon for acceptance tests
- **Garage upstream:** the union-vs-set semantics of `AllowBucketKey`
  (Verification A) and the `GetBucket` vs `GetKeyInfo` read agreement
  (Verification B) must be confirmed before Create/Read finalize

## Decisions

Finalized from DESIGN-0004 (2026-07-21, final — see the Open Questions
resolution block below). The shape decisions are set; Phase 2's live
verifications only confirm Garage's runtime behavior, they no longer gate
these choices.

1. **Read source-of-truth is `GetBucket.Keys[]`.** The bucket-side view
   is authoritative; `GetKeyInfo.Buckets[]` is the denormalized mirror
   (Verification B confirms they agree). Final (OQ2 → a).
2. **`bucket_id` and `access_key_id` are `RequiresReplace`.** A change
   to either is "a different edge entirely" — destroy + create, matching
   `aws_iam_user_policy_attachment`.
3. **Synthetic id is `<bucket_id>/<access_key_id>`** (slash-separated).
   Both components are `/`-free (Verification confirms). Final (OQ3 → a).
4. **Delete strips all perms via `DenyBucketKey`.** No delete-edge
   endpoint exists; denying `{owner, read, write}` removes the edge.
   Read maps "key absent from `Keys[]` or all-false" to `RemoveResource`.
5. **Both `AllowBucketKey` and `DenyBucketKey` are idempotent by
   effect.** Safe to re-issue on drift correction / rollback
   (Verification C).

## Open Questions

The shape decisions below are resolved (see the resolution block that
follows); options are retained for the record. Live behavior
(Verifications A–E) is confirmed in Phase 2 as tasks. Each option is
lettered — **a is the recommendation**, b+ are alternatives, or supply
your own under *other*.

**Resolution status (2026-07-21, final):**
resolved to option **(a)** — Q1 defensive Allow+Deny, Q2
`GetBucket.Keys[]` authoritative, Q3 slash-separated id, Q4
meaningful-subset test matrix, Q5 single PR. Options retained below for
the record; override any by editing.

1. **Create semantics: does `AllowBucketKey` set or union the triple?**
   (DESIGN-0004 OQ1 — resolved empirically in Phase 2 Verification A,
   but the *code strategy* is a choice.)
   - **a. (Recommended)** Implement defensively: `AllowBucketKey` the
     true flags, then unconditionally `DenyBucketKey` the false flags so
     the edge lands exactly at the configured triple regardless of
     union-vs-set semantics. One extra call, always-correct.
   - b. Assume `AllowBucketKey` *sets* (send the full triple, trust
     Garage to apply false flags) — one call, but wrong if Garage
     unions; relies on Verification A holding across Garage versions.
   - c. Always issue both `AllowBucketKey` (true flags) and
     `DenyBucketKey` (false flags) on every Create *and* Update —
     uniform, most calls.
   - *other:* ______

2. **Read path source-of-truth.** (DESIGN-0004 OQ2)
   - **a. (Recommended)** `GetBucket(bucket_id).Keys[]` — the
     bucket-side view; matches how DESIGN-0004 sketches Read.
   - b. `GetKeyInfo(access_key_id).Buckets[]` — the key-side view; same
     data, but requires a second lookup if we also validate the bucket
     exists.
   - *other:* ______

3. **Synthetic id format.** (DESIGN-0004 OQ3)
   - **a. (Recommended)** `<bucket_id>/<access_key_id>` — slash-separated,
     matches the AWS IAM edge convention; both components are `/`-free.
   - b. A different separator (e.g. `,` or `:`) — hedges against a
     future Garage id format that includes `/`.
   - c. JSON-encoded compound id — unambiguous but ugly on the CLI
     (`terraform import` string).
   - *other:* ______

4. **Acceptance-test matrix breadth.** (DESIGN-0004 OQ6)
   - **a. (Recommended)** Test the meaningful subset: read-only →
     read+write, owner-alone, read+write → revoke-all, and single-flag
     drift. Skips the full 8-cell truth table.
   - b. Exhaustive 8-cell truth table — maximal coverage, slow and
     repetitive.
   - *other:* ______

5. **PR / branch strategy for the 7 phases.**
   - **a. (Recommended)** Single PR on
     `feat/design-0004-phase-4-bucket-key-resource`, matching the
     IMPL-0002 / IMPL-0003 precedent.
   - b. Split client+scaffold (Phases 1–3) from lifecycle (Phases 4–7).
   - *other:* ______

## References

- [DESIGN-0004: Phase 4 implementation — garage_bucket_key resource](../design/0004-phase-4-implementation-garagebucketkey-resource.md)
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 4 entry
- [DESIGN-0003: Phase 3 garage_key resource](../design/0003-phase-3-implementation-garagekey-resource.md)
  — the resource this edge references; Decision 3 documents the
  deliberate `buckets` omission
- [IMPL-0003: Phase 3 implementation plan](0003-phase-3-garagekey-resource-client-wrapper-dual-secretsource.md)
  — must land first; provides `garage_key`
- [IMPL-0002: Phase 2 implementation plan](0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — `AllowBucketKey` wrapper already exists (Phase 6); livecheck probe
  pattern; the finding that owner/read/write are independent flags
- [Garage admin v2 API: bucket-key endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [AWS provider `aws_iam_user_policy_attachment`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_user_policy_attachment)
  — edge-resource patterns (synthetic id, RequiresReplace on tuple fields)

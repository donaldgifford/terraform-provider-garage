---
id: IMPL-0003
title: "Phase 3 garage_key resource: client wrapper, dual secret_source modes, CRUD, acceptance suite"
status: Draft
author: Donald Gifford
created: 2026-07-19
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0003: Phase 3 garage_key resource: client wrapper, dual secret_source modes, CRUD, acceptance suite

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-07-19

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Client wrapper key methods](#phase-1-client-wrapper-key-methods)
    - [Tasks](#tasks)
    - [Success Criteria](#success-criteria)
  - [Phase 2: Live-API behavior verifications](#phase-2-live-api-behavior-verifications)
    - [Tasks](#tasks-1)
    - [Success Criteria](#success-criteria-1)
  - [Phase 3: Resource scaffold](#phase-3-resource-scaffold)
    - [Tasks](#tasks-2)
    - [Success Criteria](#success-criteria-2)
  - [Phase 4: Create + Read (garage mode)](#phase-4-create--read-garage-mode)
    - [Tasks](#tasks-3)
    - [Success Criteria](#success-criteria-3)
  - [Phase 5: Update + Delete](#phase-5-update--delete)
    - [Tasks](#tasks-4)
    - [Success Criteria](#success-criteria-4)
  - [Phase 6: external mode + validators + ValidateConfig warning](#phase-6-external-mode--validators--validateconfig-warning)
    - [Tasks](#tasks-5)
    - [Success Criteria](#success-criteria-5)
  - [Phase 7: ImportState](#phase-7-importstate)
    - [Tasks](#tasks-6)
    - [Success Criteria](#success-criteria-6)
  - [Phase 8: Acceptance suite + docs](#phase-8-acceptance-suite--docs)
    - [Tasks](#tasks-7)
    - [Success Criteria](#success-criteria-7)
- [File Changes](#file-changes)
- [Testing Plan](#testing-plan)
- [Dependencies](#dependencies)
- [Decisions](#decisions)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Objective

Execute Phase 3 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) per
[DESIGN-0003](../design/0003-phase-3-implementation-garagekey-resource.md).
Outcome: a `garage_key` resource with full CRUD, both `secret_source`
modes (`"garage"` and `"external"`) per [ADR-0001](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md),
cross-attribute validators, a `ValidateConfig` warning on the in-state
mode, `allow_create_bucket` permission management, import, and acceptance
tests — all green on a real Garage container in CI.

This is the load-bearing resource of the provider: the write-only
`secret_access_key_wo` path is the differentiator from the four existing
community providers. Phases 4 and 5 (`garage_bucket_key`,
`garage_bucket_local_alias`) both reference `garage_key.access_key_id`,
so the schema and import semantics landed here constrain both follow-on
phases.

**Implements:** DESIGN-0003, which scopes Phase 3 of RFC-0001.

## Scope

### In Scope

- 5 new `*Client` methods in `internal/client/client.go` for the key
  admin v2 endpoints (`CreateKey`, `ImportKey`, `GetKeyInfo`,
  `UpdateKey`, `DeleteKey`)
- `internal/resources/key/` package implementing the Plugin Framework
  resource lifecycle (Create / Read / Update / Delete / ImportState)
- HCL surface: `name` (Required), `secret_source` (Required, OneOf
  `garage`/`external`, RequiresReplace), `secret_access_key_wo`
  (Optional, WriteOnly, Sensitive), `secret_access_key_wo_version`
  (Optional, Int64), `allow_create_bucket` (Optional+Computed, default
  false), plus computed `id`, `access_key_id`, `secret_access_key`
  (Sensitive), `created`, `expired`
- Both credential modes per ADR-0001:
  - `"garage"` — `CreateKey` mints the secret; stored `Computed +
    Sensitive` in state
  - `"external"` — `CreateKey` then `ImportKey` overwrites with the
    user-supplied `secret_access_key_wo`; secret never persisted
- Cross-attribute config validators (mode ↔ `secret_access_key_wo`
  presence; `RequiredTogether(_wo, _wo_version)`)
- `ValidateConfig` warning diagnostic when `secret_source = "garage"`
- Rotation via `secret_access_key_wo_version` bump and mode change
  (both trigger `RequiresReplace`)
- Acceptance tests for both modes, including the write-only path driven
  by an `ephemeral "random_password"` resource
- `examples/resources/garage_key/{resource.tf,import.sh}` and
  regenerated `docs/resources/key.md`
- In-implementation verifications from DESIGN-0003 §Error handling and
  idempotency: A and B (live probes), C (document-only — policy locked by
  the retry transport), plus E (inline-`allow` survival across
  `ImportKey`). D (`DeleteKey` method) is already resolved as POST

### Out of Scope

- Per-bucket permission edges (`garage_bucket_key`) — Phase 4 /
  IMPL-0004. `garage_key` deliberately omits a `buckets` attribute
  (DESIGN-0003 Decision 3)
- Local (per-key) bucket aliases — Phase 5 / IMPL-0005
- Key expiration (`expiration` / `never_expires` on `UpdateKey`) —
  deferred (DESIGN-0003 Decision 4); `expired` is exposed read-only so
  out-of-band expiration surfaces as drift
- AdminToken-style API tokens — RFC-0001 §Phase 8
- `ListKeys` wrapper and any "import by name" UX — deferred (see Open
  Question 3)
- Server-side rotation of `secret_source = "garage"` keys — `UpdateKey`
  has no in-place secret regeneration; the only path is replace

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phases land as
separate commits on `feat/design-0003-phase-3-key-resource` and ship as
a single PR (Open Question 1 → a).

---

### Phase 1: Client wrapper key methods

Extend `internal/client/client.go` with the five key methods the resource
will consume. Pure unit-testable work — uses `httptest.NewServer`, no
container fixture. Mirrors the Phase 1 bucket-method pattern (`op`
constant, `statusToError`, `tflog.Trace` on entry/exit, retry via the
existing `retryingTransport`).

#### Tasks

- [ ] Add `func (c *Client) CreateKey(ctx context.Context, body openapi.CreateKeyRequest) (*openapi.GetKeyInfoResponse, error)`
      — POST `/v2/CreateKey`; body is the `UpdateKeyRequestBody` alias
      (`CreateKeyRequest = UpdateKeyRequestBody`); returns
      `CreateKeyResponse` (`= GetKeyInfoResponse`). Note the returned
      `SecretAccessKey *string` is only populated here at create time
- [ ] Add `func (c *Client) ImportKey(ctx context.Context, body openapi.ImportKeyRequest) (*openapi.GetKeyInfoResponse, error)`
      — POST `/v2/ImportKey`; body is `{AccessKeyId, Name*, SecretAccessKey}`.
      Both `AccessKeyId` and `SecretAccessKey` are required by the spec
- [ ] Add `func (c *Client) GetKeyInfo(ctx context.Context, id string) (*openapi.GetKeyInfoResponse, error)`
      — GET `/v2/GetKeyInfo?id=<id>`; maps HTTP 404 → `ErrNotFound`.
      Do **not** set `showSecretKey=true` (secret is only returned at
      create; reads never carry it)
- [ ] Add `func (c *Client) UpdateKey(ctx context.Context, id string, body openapi.UpdateKeyRequestBody) (*openapi.GetKeyInfoResponse, error)`
      — POST `/v2/UpdateKey?id=<id>`; body carries `Name *string`,
      `Allow *KeyPerm`, `Deny *KeyPerm`; maps 404 → `ErrNotFound`
- [ ] Add `func (c *Client) DeleteKey(ctx context.Context, id string) error`
      — POST `/v2/DeleteKey?id=<id>` (RPC-style POST, verb confirmed
      `http.MethodPost` in the generated client, same quirk as
      `DeleteBucket`); 404 → `ErrNotFound` (treated as already-gone by
      the resource)
- [ ] Empty-arg validation gate for `id` on `GetKeyInfo` / `UpdateKey` /
      `DeleteKey`, and for `AccessKeyId` / `SecretAccessKey` on
      `ImportKey`, matching the bucket methods' up-front `id == ""`
      guards
- [ ] Extend the retry-policy posture: `CreateKey` and `ImportKey` are
      **non-idempotent** (POST with side effects; no server-side
      dedup). They already pass through the `retryingTransport`
      untouched because `isIdempotent` only retries GET/HEAD — assert
      this in a test rather than adding a lookup table
- [ ] Unit tests in `internal/client/client_test.go` for each method:
  - Happy path returns expected struct (incl. `SecretAccessKey`
    populated on `CreateKey`, `nil` on `GetKeyInfo`)
  - 404 maps to `ErrNotFound` (`GetKeyInfo` / `UpdateKey` / `DeleteKey`)
  - 401 / 403 map to `ErrUnauthorized` / `ErrForbidden`
  - `UpdateKey` allow/deny body shape round-trips (`Allow.CreateBucket`,
    `Deny.CreateBucket`)
  - POST verbs pass 5xx through with no retry; GET `GetKeyInfo` retries
    on 5xx
  - Empty-arg guards return an error without issuing a request
- [ ] Run `just lint` / `just test` — verify all green; client-package
      coverage stays ≥80%
- [ ] Commit as `feat: add key client wrapper methods (IMPL-0003 Phase 1)`

#### Success Criteria

- All 5 wrapper methods exist with doc comments
- Unit-test coverage for the client package stays ≥80%
- `just lint` / `just test` exit 0
- Generated client (`internal/client/openapi/generated.go`) untouched —
  these methods consume existing generated types
  (`CreateKeyRequest`, `ImportKeyRequest`, `GetKeyInfoResponse`,
  `UpdateKeyRequestBody`, `KeyPerm`)

---

### Phase 2: Live-API behavior verifications

The live-behavior questions from DESIGN-0003 that remain open — resolved
against a live `dxflrs/garage:v2.3.0` container before writing the
resource code that consumes them. Two of the original four are already
resolved without a probe (`DeleteKey` HTTP method from the generated
client; `CreateKey` retry policy from the retry-transport design — see
§Decisions), so only A, B, and E need live probes, with C downgraded to
a documentation note. Probes live in
`internal/client/livecheck_test.go` under `//go:build garageprobe`,
runnable via `go test -tags=garageprobe -run TestLiveKey ./internal/client/...`
(same pattern IMPL-0002 Phase 2 established).

#### Tasks

- [ ] **Verification A — `ImportKey` idempotency on identical (id,
      secret).** Call `ImportKey` twice with the same `accessKeyId` +
      `secretAccessKey`. Expected: 2xx no-op (mirrors `AddBucketAlias`).
      Needed so the external-mode rollback path can retry `ImportKey`
      safely. Record the observed status
- [ ] **Verification B — `ImportKey` with a *different* secret for an
      existing id.** Expected: HTTP 400; capture the exact message for
      the resource's diagnostic. If Garage silently overwrites instead,
      flag it — it changes the external-mode overwrite model
- [ ] **Verification C — `CreateKey` 5xx replay (document-only).** Policy
      is already locked: the retry transport only retries GET/HEAD, so
      `CreateKey` (POST) is never retried — same as `CreateBucket`. No
      probe needed to decide policy; just capture the observed 5xx body
      for the diagnostic if the case ever surfaces
- [ ] ~~**Verification D — `DeleteKey` HTTP method**~~ *(resolved — POST,
      confirmed from the generated client; no probe; see §Decisions #10)*
- [ ] **Verification E (new, from Decision 1) — inline `allow` on
      `CreateKey` and `allow` survival across `ImportKey`.** Confirm
      `CreateKey({allow:{createBucket:true}})` lands the permission at
      create time (so garage mode needs no follow-up `UpdateKey`), and
      confirm a subsequent `ImportKey` on that id preserves the
      permission (so external mode's overwrite doesn't silently reset
      `allow_create_bucket`). If `ImportKey` resets it, external mode
      must re-issue `UpdateKey` after the overwrite
- [ ] Fold observed status codes / message bodies / ordering
      implications into this doc's §Decisions; commit the doc + the
      `livecheck_test.go` additions together

#### Success Criteria

- The remaining verifications (A, B, E) documented with observed status
  code, response body shape, and lifecycle implications; C captured as a
  documentation note (no probe); D already resolved (POST)
- `livecheck_test.go` compiles and its `TestLiveKey*` probes pass against
  a live container under `-tags=garageprobe`
- No production code depends on the build-tagged tests; default builds
  ignore them
- The external-mode Create sequence (Phase 6) and garage-mode Create
  (Phase 4) are unblocked — the "does CreateKey honor inline allow"
  and "does ImportKey preserve allow" questions are answered

---

### Phase 3: Resource scaffold

`internal/resources/key/` package with the framework boilerplate:
Metadata, Schema, Configure, Model struct, conversion helpers, and the
config-validator / `ValidateConfig` method stubs. No lifecycle logic yet
— Create/Read/Update/Delete are stubbed to emit "not yet implemented"
diagnostics. Resource is **not** registered in `provider.go` until
Phase 4.

#### Tasks

- [ ] Create `internal/resources/key/` package (package name `key`,
      ST1003-clean, matching `bucket` / `clusterinfo`)
- [ ] Write `resource.go`:
  - `type Resource struct { client *client.Client }` with `New()` ctor
  - `Metadata()` sets `req.ProviderTypeName + "_key"`
  - `Configure()` asserts `*client.ProviderData` from `req.ProviderData`
    and pulls `.Client` (matches the bucket resource; keys don't touch
    the S3 fields on `ProviderData`)
  - `Schema()` with all attributes from DESIGN-0003 §Schema — `id` and
    `access_key_id` both `Computed + UseStateForUnknown`; `name`
    Required; `secret_source` Required + `OneOf("garage","external")` +
    `RequiresReplace`; `secret_access_key` Computed+Sensitive;
    `secret_access_key_wo` Optional+WriteOnly+Sensitive;
    `secret_access_key_wo_version` Optional Int64;
    `allow_create_bucket` Optional+Computed+`Default(false)`;
    `created` / `expired` Computed
  - Lifecycle methods stubbed with "not yet implemented" diagnostics
  - Compile-time interface assertions: `resource.Resource`,
    `resource.ResourceWithConfigure`, `resource.ResourceWithConfigValidators`,
    `resource.ResourceWithValidateConfig`, `resource.ResourceWithImportState`
- [ ] Write `model.go`:
  - `Model` struct mirroring the schema (DESIGN-0003 §Data Model —
    10 fields)
  - `applyKeyInfoToModel(*openapi.GetKeyInfoResponse, *Model) diag.Diagnostics`
    — populates everything except `SecretAccessKeyWO` (write-only, never
    read back) and `SecretAccessKey` (preserved from prior state)
  - `permFromModel(plan, state *Model) (allow, deny *openapi.KeyPerm)` —
    derives the `UpdateKey` allow/deny pair from desired vs current
    `AllowCreateBucket`
- [ ] Write `validators.go`:
  - `ConfigValidators()` returning
    `resourcevalidator.RequiredTogether(path secret_access_key_wo,
    path secret_access_key_wo_version)` plus a custom validator
    enforcing `secret_source = "external"` ↔ `secret_access_key_wo` set
    and `secret_source = "garage"` ↔ `secret_access_key_wo` null
  - `ValidateConfig()` emitting the `AddAttributeWarning` on
    `secret_source = "garage"` per DESIGN-0003 §ValidateConfig warning
- [ ] Unit tests in `model_test.go` / `validators_test.go`:
  - `applyKeyInfoToModel` round-trip (secret preservation, `Buckets`
    ignored, `expired` mapped)
  - `permFromModel` truth table (false→true → allow; true→false → deny;
    no change → both nil)
  - Config validator rejects mode/secret mismatches; accepts valid pairs
  - `ValidateConfig` emits exactly one warning on garage mode, none on
    external mode
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: scaffold garage_key resource package (IMPL-0003 Phase 3)`

#### Success Criteria

- `internal/resources/key/` package compiles
- Schema attribute set matches DESIGN-0003 §Schema exactly;
  MarkdownDescription strings present and proofread (incl. the loud
  post-import replace note and the `buckets`-omission note)
- `Model` ↔ openapi conversion helpers and both validators unit-tested
- Resource is **not** yet registered in `provider.Resources()` —
  confirmed the registration list is unchanged from Phase 2

---

### Phase 4: Create + Read (garage mode)

Implement the two simplest lifecycle methods for the *simpler* mode
(`secret_source = "garage"`) and register the resource so basic
provisioning works end-to-end. External mode lands in Phase 6.

#### Tasks

- [ ] Implement `Create()` for the garage-mode branch per DESIGN-0003
      §Create flow:
  1. Build `CreateKeyRequest{Name, Allow}` (inline `allow` per
     Decision 1 / Verification E); call `client.CreateKey`
  2. Store `id` = `access_key_id` = response `AccessKeyId`;
     `secret_access_key` = response `SecretAccessKey` (Sensitive)
  3. `applyKeyInfoToModel` for `name` / `created` / `expired` /
     `allow_create_bucket`
  - Guard: if `secret_source = "external"`, emit "not yet implemented"
    (Phase 6 fills it in) — keep the branch explicit so Phase 6 is a
    clean addition
- [ ] Implement `Read()`:
  - Read state's `id`; call `client.GetKeyInfo`
  - On `ErrNotFound`: `resp.State.RemoveResource(ctx)` (deleted
    out-of-band)
  - Otherwise `applyKeyInfoToModel`; **preserve** `secret_access_key`
    and `secret_access_key_wo`/`_wo_version` from prior state (Garage
    won't return the secret after create)
  - Ignore `GetKeyInfoResponse.Buckets` (Decision 3 — edges are Phase 4)
- [ ] Register the resource in `internal/provider/provider.go`
      `Resources()` alongside `garage_bucket`
- [ ] Acceptance test `TestAccGarageKey_garageMode_basic` — create with
      `secret_source = "garage"`, no `allow_create_bucket`; verify
      `access_key_id` set, `secret_access_key` non-empty in state.
      `t.Parallel()` from day one
- [ ] Acceptance test `TestAccGarageKey_garageMode_allowCreateBucket` —
      create with `allow_create_bucket = true`; verify the flag round-trips
      via `GetKeyInfo.Permissions.CreateBucket`
- [ ] Run `just lint` / `just test` / `TF_ACC=1 go test ./internal/resources/key/`
      — verify all green
- [ ] Commit as `feat: garage_key Create + Read (garage mode) (IMPL-0003 Phase 4)`

#### Success Criteria

- `TestAccGarageKey_garageMode_basic` and
  `TestAccGarageKey_garageMode_allowCreateBucket` pass against a live
  container
- Secret from `CreateKey` lands in state as a Sensitive value; a second
  `Read` (via a `RefreshState` / no-op plan step) preserves it rather
  than nulling it
- `allow_create_bucket` set inline at create is observed on `GetKeyInfo`
  (Verification E confirmed) — no post-create `UpdateKey` needed in
  garage mode
- Resource registered; `garage_bucket` acceptance tests still pass
  (no cross-resource regression)

---

### Phase 5: Update + Delete

Implement `UpdateKey`-backed updates (`name`, `allow_create_bucket`) and
the single-call `DeleteKey`. Both modes share these paths, so this phase
covers external mode's update/delete too (external mode's *create* is
still Phase 6).

#### Tasks

- [ ] Implement `Update()`:
  - `name` change → `UpdateKey({Name: &new})`
  - `allow_create_bucket` false→true → `UpdateKey({Allow:{CreateBucket:&true}})`
  - `allow_create_bucket` true→false → `UpdateKey({Deny:{CreateBucket:&true}})`
  - Compose via `permFromModel(plan, state)`; issue a single `UpdateKey`
    carrying `Name` + `Allow`/`Deny` as needed
  - Refresh state via the `UpdateKey` response (it returns
    `GetKeyInfoResponse`); preserve `secret_access_key` from prior state
  - `secret_source` and `secret_access_key_wo_version` changes never
    reach `Update()` — the framework routes them to replace (Phase 3
    plan modifiers). Add a defensive diagnostic if they somehow do
- [ ] Implement `Delete()`:
  - `client.DeleteKey(id)`; on `ErrNotFound` return silently
    (already gone). No S3-side cleanup — keys own no data (DESIGN-0003
    §Delete flow). No `force_destroy` equivalent
- [ ] Acceptance test `TestAccGarageKey_rename` — create then change
      `name`; verify the rename lands via `UpdateKey`
- [ ] Acceptance test `TestAccGarageKey_toggleAllowCreateBucket` —
      false→true→false; verify each flip via `GetKeyInfo`
- [ ] Acceptance test `TestAccGarageKey_driftDetection` — external
      `UpdateKey` rename in a `PreConfig` hook, then re-apply same HCL;
      plan detects drift and corrects it
- [ ] Run all gates green
- [ ] Commit as `feat: garage_key Update + Delete (IMPL-0003 Phase 5)`

#### Success Criteria

- Rename, `allow_create_bucket` toggle (both directions), and drift
  correction all pass in acceptance tests
- `Delete()` of a key already removed out-of-band is a no-op (no error
  surfaced)
- `secret_access_key` survives an `Update()` round-trip (not nulled by
  the refresh)

---

### Phase 6: external mode + validators + ValidateConfig warning

The differentiating mode. Implement the `secret_source = "external"`
Create path (the `CreateKey` + `ImportKey` dance), wire the
cross-attribute validators and `ValidateConfig` warning (scaffolded in
Phase 3) into the live lifecycle, and prove the write-only path with an
`ephemeral "random_password"` resource.

#### Tasks

- [ ] Implement the external-mode branch of `Create()` per DESIGN-0003
      §Create flow / Decision 1:
  1. `CreateKey({Name, Allow})` → capture `AccessKeyId`, discard the
     Garage-generated secret
  2. Read `secret_access_key_wo` from config (write-only values are
     read from `req.Config`, not plan/state); `ImportKey({AccessKeyId,
     Name, SecretAccessKey: <wo>})` overwrites the secret. Treat a
     Verification-A idempotent 2xx as success
  3. If Verification E showed `ImportKey` resets `allow`, re-issue
     `UpdateKey` to restore `allow_create_bucket`
  4. Store `access_key_id` in state; leave `secret_access_key` null
  - Rollback: if `ImportKey` fails after `CreateKey` succeeded, call
    `client.DeleteKey` on the partially-created id and `tflog.Warn`
    (mirrors the bucket Create rollback)
- [ ] Confirm the Phase 3 `ConfigValidators()` and `ValidateConfig()`
      fire correctly end-to-end (they were unit-tested in Phase 3; here
      verify at plan time in an acceptance test)
- [ ] Ensure `secret_access_key_wo_version` bump triggers replace: the
      framework's write-only version semantics should force it; add an
      acceptance step that bumps the version and asserts a replace in
      the plan
- [ ] Acceptance test `TestAccGarageKey_externalMode_create` — uses
      `ephemeral "random_password" { length = 40 }` (core
      `terraform-plugin-testing` / `hashicorp/random`); verify
      `secret_access_key` is null in state and the key authenticates
      (optional: a data-plane S3 smoke check if cheap; otherwise assert
      state shape only)
- [ ] Acceptance test `TestAccGarageKey_externalMode_rotate` — bump
      `secret_access_key_wo_version`; assert the resource is replaced
- [ ] Acceptance test `TestAccGarageKey_modeChange_forcesReplace` —
      garage → external; assert replace
- [ ] Acceptance test `TestAccGarageKey_validateConfig_garageWarning` —
      assert the in-state warning diagnostic is present on garage mode
      (and absent on external mode)
- [ ] Acceptance test `TestAccGarageKey_invalidConfig` — mode/secret
      mismatch (`external` without `_wo`, `garage` with `_wo`) fails at
      plan with the validator's error (`ExpectError`)
- [ ] Flip [ADR-0001](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md)
      status `Proposed` → `Accepted` once both modes are demonstrated
      green in acceptance tests
- [ ] Run all gates green
- [ ] Commit as `feat: garage_key external secret_source mode + validators (IMPL-0003 Phase 6)`

#### Success Criteria

- External-mode create works: the user-supplied `secret_access_key_wo`
  is the only active secret; `secret_access_key` stays null in state
- `secret_access_key_wo_version` bump and `secret_source` change both
  force replace (verified in plan)
- The `ValidateConfig` warning appears on garage mode and not on
  external mode
- Cross-attribute validators reject both mismatch directions at plan
  time
- Create-rollback on `ImportKey` failure deletes the partial key and
  logs a warning (documented + tflog-traced; runtime smoke of the
  rollback path exercised if a cheap failure injection exists,
  otherwise deferred with a note)
- ADR-0001 flipped to Accepted

---

### Phase 7: ImportState

Bare `access_key_id` import
(`terraform import garage_key.example <access_key_id>`). Small phase.

#### Tasks

- [ ] Implement `ImportState()` via
      `resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)`
      — seeds `id`; the subsequent `Read` populates the rest via
      `GetKeyInfo`. `secret_access_key` is null (unrecoverable);
      `secret_access_key_wo` / `_wo_version` are null in config
- [ ] Acceptance test `TestAccGarageKey_import`:
  - Step 1: create a garage-mode key with `allow_create_bucket = true`
  - Step 2: `ImportStateVerify` — with `ImportStateVerifyIgnore` on
    `secret_access_key`, `secret_access_key_wo`,
    `secret_access_key_wo_version`, and `secret_source` (none are
    recoverable from Garage; the post-import replace is by design per
    DESIGN-0003 §Import)
- [ ] Document the deliberate post-import replace behavior in the
      resource's `MarkdownDescription` (imported state can't carry
      credentials, so the next apply necessarily produces a diff)
- [ ] Run all gates green
- [ ] Commit as `feat: garage_key ImportState (IMPL-0003 Phase 7)`

#### Success Criteria

- Import by `access_key_id` populates `name` / `access_key_id` /
  `created` / `expired` / `allow_create_bucket` from Garage
- `TestAccGarageKey_import` passes with the documented
  `ImportStateVerifyIgnore` set
- The post-import replace is documented, not surfaced as a bug

---

### Phase 8: Acceptance suite + docs

Polish phase. Comprehensive acceptance tests; `tfplugindocs`-generated
docs; CI green on the PR.

#### Tasks

- [ ] Write `examples/resources/garage_key/resource.tf` — show both
      modes (garage-generated and external via `ephemeral
      "random_password"`), matching the existing
      `examples/resources/garage_bucket/` convention (no `terraform {
      required_providers }`, no `variable` blocks)
- [ ] Write `examples/resources/garage_key/import.sh` for the import
      command line
- [ ] Run `just generate`; verify `docs/resources/key.md` renders
      cleanly (attrs grouped Required / Optional / Read-Only; Example
      Usage from `resource.tf`; Import from `import.sh`)
- [ ] Add `TestAccGarageKey_parallelSafety` — `count = 3` with distinct
      names; concurrent Create lifecycles
- [ ] Re-run `just generate`; verify zero diff (idempotent)
- [ ] Ensure the full `TestAccGarageKey_*` suite passes under
      `TF_ACC=1` and total wall time stays well under the 120m CI
      timeout (target: comparable to the bucket suite's ~13s)
- [ ] CI green on the PR (lint + test + acceptance matrix over the
      pinned TF/OpenTofu versions + `generate` no-diff gate)
- [ ] Commit final docs/test additions as
      `feat: garage_key acceptance polish + docs (IMPL-0003 Phase 8)`

#### Success Criteria

- `docs/resources/key.md` exists and reflects the schema accurately,
  including both `secret_source` modes
- `examples/resources/garage_key/resource.tf` renders cleanly in docs
- All `TestAccGarageKey_*` tests pass; `just generate` idempotent
- CI `acceptance` + `generate` jobs green on the PR

---

## File Changes

| File                                              | Action | Notes                                                        |
|---------------------------------------------------|--------|--------------------------------------------------------------|
| `internal/client/client.go`                       | Modify | Phase 1 — add 5 key methods                                  |
| `internal/client/client_test.go`                  | Modify | Phase 1 — unit tests for the new methods                     |
| `internal/client/livecheck_test.go`               | Modify | Phase 2 — `TestLiveKey*` build-tagged probes (Verifications A–E) |
| `internal/resources/key/resource.go`              | Create | Phase 3 — Schema/Metadata/Configure + lifecycle (Phases 4–7) |
| `internal/resources/key/model.go`                 | Create | Phase 3 — Model + `applyKeyInfoToModel` / `permFromModel`    |
| `internal/resources/key/validators.go`            | Create | Phase 3 — `ConfigValidators` + `ValidateConfig` warning      |
| `internal/resources/key/model_test.go`            | Create | Phase 3 — helper + validator unit tests                      |
| `internal/resources/key/resource_test.go`         | Create | Phase 4 — acceptance suite (grows phase by phase)            |
| `internal/provider/provider.go`                   | Modify | Phase 4 — register `garage_key`                              |
| `examples/resources/garage_key/resource.tf`       | Create | Phase 8 — usage example (both modes)                         |
| `examples/resources/garage_key/import.sh`         | Create | Phase 8 — import command-line example                        |
| `docs/resources/key.md`                           | Create | Phase 8 — generated by `tfplugindocs`                        |
| `docs/adr/0001-*.md`                              | Modify | Phase 6 — status Proposed → Accepted                         |
| `docs/design/0003-*.md`                           | Modify | Phase 2 — replace verification placeholders with findings    |
| `docs/impl/0003-*.md`                             | Modify | Each phase — check off completed tasks                       |

## Testing Plan

| Layer         | Where                                                   | When    |
|---------------|---------------------------------------------------------|---------|
| Unit          | `internal/client/client_test.go` (5 new method tests)   | Phase 1 |
| Unit          | `internal/resources/key/model_test.go` (+validators)    | Phase 3 |
| Live probe    | `internal/client/livecheck_test.go` (tagged)            | Phase 2 |
| Acceptance    | `internal/resources/key/resource_test.go`               | 4–8     |
| CI acceptance | matrix over the pinned TF / OpenTofu versions           | Phase 8 |
| Docs drift    | `generate` job in CI (no-diff gate)                     | Phase 8 |

## Dependencies

- **Phase 2 of RFC-0001 (DESIGN-0002 / IMPL-0002)** — provides the
  client wrapper conventions, `ProviderData` plumbing, acctest fixture,
  and the resource-package pattern this phase clones
- **External:** Docker daemon for acceptance tests (local + CI)
- **External:** `hashicorp/random` provider (ephemeral
  `random_password`) — in core `terraform-plugin-testing`, no new
  module dep
- **Garage upstream:** API behavior at v2.3.0 must match the spec;
  divergence surfaces in Phase 2's live verifications (A–E)
- **Terraform / OpenTofu ≥ 1.11** for write-only argument support
  (`secret_access_key_wo`) — acceptance tests must run on a version that
  supports ephemeral values in managed resources

## Decisions

Carried from DESIGN-0003 §Decisions; the live verifications (A–E) resolve
into this section as Phase 2 completes.

1. **External-mode create is CreateKey + ImportKey.** ADR-0001's
   "external" flow implies a single call, but the spec has no
   "create-with-supplied-secret" shape: `CreateKey` takes no secret,
   `ImportKey` requires both id and secret. Resolution: mint a stable
   `accessKeyId` via `CreateKey` (discard its secret), then `ImportKey`
   to overwrite with the user's value. Garage assigns the id; the user
   supplies only the secret, which never enters state. (Revisit against
   Open Question 2.)
2. **`allow_create_bucket` set inline in the CreateKey body.**
   `CreateKeyRequest = UpdateKeyRequestBody` carries `Allow`, so garage
   mode sets it in one call. Verification E confirms Garage honors it at
   create time and that `ImportKey` preserves it (or, if not, external
   mode re-issues `UpdateKey`).
3. **`buckets` attribute deliberately omitted.** Exposing
   `GetKeyInfoResponse.Buckets` here would fight `garage_bucket_key`
   (Phase 4) for the same edge data. This resource ignores `Buckets` on
   Read.
4. **Key expiration deferred.** `expiration` / `neverExpires` on
   `UpdateKey` are out of v0.1; Garage's default ("never expires")
   matches omission. `expired` is exposed Computed so out-of-band
   expiration surfaces as drift. Adding expiration later is additive.
5. **No `force_destroy` equivalent.** Keys own no data — delete is a
   permissions revocation. Dangling `garage_bucket_key` rows are Phase 4's
   concern (Read-time drift).
6. **`access_key_id` is the canonical id.** Garage allows duplicate
   names; `name` is a mutable attribute, not identity. `id` and
   `access_key_id` carry the same value (kept as two attrs for
   readable HCL references).
7. **`secret_source` is Required, no default** (ADR-0001) — enforced by
   `Required` + `OneOf`; explicit choice or validation fails.
8. **`RequiresReplace` on mode change** (ADR-0001) — no `UpdateKey`
   pathway swaps a secret in place.
9. **`CreateKey` / `ImportKey` are non-idempotent** — POST with side
   effects, no server-side dedup; the `retryingTransport` already skips
   them (retries only GET/HEAD). Asserted, not table-driven
   (Verification C, resolved by precedent).
10. **`DeleteKey` is `POST`** (RPC-style, like `DeleteBucket`) — confirmed
   from the generated client (`NewDeleteKeyRequest` → `http.MethodPost`);
   no live probe (former Verification D, resolved).

## Open Questions

The live-behavior questions remain as in-phase tasks rather than upfront
blockers (Verifications A and B, plus E; resolved in Phase 2). The
questions below need an owner decision before or during implementation.
Each option is lettered — **a is the recommendation**, b+ are
alternatives, or supply your own under *other*.

**Resolution status (2026-07-21, final):** all
five resolved to option **(a)** — Q1 single PR, Q2
`CreateKey` + `ImportKey` (confirms DESIGN-0003 Decision 1), Q3 defer
`ListKeys`, Q4 always-on `ValidateConfig` warning, Q5 keep both `id` and
`access_key_id`. Options are retained below for the record; override any
by editing.

1. **PR / branch strategy for the 8 phases.**
   - **a. (Recommended)** Single PR for all 8 phases on
     `feat/design-0003-phase-3-key-resource`, matching IMPL-0002
     Decision 5 — preserves momentum, keeps the resource reviewable as
     one coherent unit.
   - b. Split into two PRs: Phases 1–5 (client + garage mode), then
     Phases 6–8 (external mode + validators + polish). Isolates the
     write-only complexity for focused review.
   - c. One PR per phase — maximal reviewability, maximal overhead.
   - *other:* ______

2. **External-mode key-creation strategy (revisits Decision 1).**
   - **a. (Recommended)** `CreateKey` then `ImportKey` to overwrite the
     secret. Garage assigns `access_key_id`; the user supplies only the
     secret. Matches DESIGN-0003 Decision 1 and keeps the schema free of
     a user-supplied id. Downside: a Garage-generated secret exists for
     a few milliseconds before being overwritten (never persisted).
   - b. `ImportKey` directly with a **user-supplied** `access_key_id`
     *and* secret — one call, no discarded secret, but adds an
     `access_key_id_wo`-style input and breaks the "Garage assigns the
     id" model; users must generate/track ids.
   - c. Keep (a) but add a client-generated `access_key_id` as an
     optional override for users who want deterministic ids (hybrid).
   - *other:* ______

3. **`ListKeys` wrapper + "import by name" now or later.**
   - **a. (Recommended)** Defer — v0.1 imports by `access_key_id` only;
     add `ListKeys` and a `garage_keys` data source / import-by-name in
     a later phase when there's demand. Keeps this phase focused.
   - b. Add the `ListKeys` wrapper now (cheap) so a future data source
     doesn't re-touch the client, even though nothing consumes it yet.
   - *other:* ______

4. **Should the `ValidateConfig` in-state warning be suppressible?**
   - **a. (Recommended)** Always warn on `secret_source = "garage"` —
     the trade-off is real and a one-time warning per plan is
     acceptable friction; matches DESIGN-0003.
   - b. Add a provider- or resource-level `suppress_state_secret_warning`
     flag for users who have accepted the trade-off and don't want the
     recurring diagnostic.
   - *other:* ______

5. **Collapse `id` and `access_key_id` into a single attribute?**
   - **a. (Recommended)** Keep both (same value) — `garage_key.foo.access_key_id`
     reads more meaningfully in downstream HCL than `.id`, and Phases
     4/5 reference `access_key_id` explicitly. Matches DESIGN-0003.
   - b. Expose only `id` and document that it *is* the access key id —
     one fewer attribute, but less readable cross-resource references.
   - *other:* ______

## References

- [DESIGN-0003: Phase 3 implementation — garage_key resource](../design/0003-phase-3-implementation-garagekey-resource.md)
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 3 entry
- [ADR-0001: garage_key secret handling — explicit secret_source modes](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md)
  — the schema and mode semantics this plan implements; flipped to
  Accepted in Phase 6
- [ADR-0002: Use terraform-plugin-framework over SDKv2](../adr/0002-use-terraform-plugin-framework-over-sdkv2.md)
  — WriteOnly + Computed + Sensitive support, `ValidateConfig` API
- [IMPL-0002: Phase 2 implementation](0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — conventions reused (commit shape, `op` constants, livecheck probe
  pattern, rollback semantics, `UseStateForUnknown`, MarkdownDescription
  style)
- [DESIGN-0004: Phase 4 garage_bucket_key resource](../design/0004-phase-4-implementation-garagebucketkey-resource.md)
  — the edge resource that references `garage_key.access_key_id`
- [DESIGN-0005: Phase 5 standalone bucket alias resources (global + local)](../design/0005-phase-5-implementation-garagebucketalias-resource.md)
  — the standalone alias resources; `garage_bucket_local_alias` references `garage_key.access_key_id`
- [Terraform Plugin Framework: Write-only Arguments](https://developer.hashicorp.com/terraform/plugin/framework/resources/write-only-arguments)
- [Garage admin v2 API: key endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

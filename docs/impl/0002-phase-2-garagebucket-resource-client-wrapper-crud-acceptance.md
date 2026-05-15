---
id: IMPL-0002
title: "Phase 2 garage_bucket resource: client wrapper, CRUD, acceptance suite"
status: Draft
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# IMPL 0002: Phase 2 garage_bucket resource: client wrapper, CRUD, acceptance suite

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-15

<!--toc:start-->
- [Objective](#objective)
- [Scope](#scope)
  - [In Scope](#in-scope)
  - [Out of Scope](#out-of-scope)
- [Implementation Phases](#implementation-phases)
  - [Phase 1: Client wrapper bucket methods](#phase-1-client-wrapper-bucket-methods)
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
  - [Phase 6: Delete + force_destroy](#phase-6-delete--force_destroy)
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

Execute Phase 2 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) per
[DESIGN-0002](../design/0002-phase-2-implementation-garagebucket-resource.md).
Outcome: a `garage_bucket` resource with full CRUD, inline global aliases,
nullable quota attributes, `force_destroy`, import, and acceptance tests —
all green on a real Garage container in CI.

**Implements:** DESIGN-0002, which scopes Phase 2 of RFC-0001.

## Scope

### In Scope

- 6 new `*Client` methods in `internal/client/` for the bucket admin v2
  endpoints (`CreateBucket`, `GetBucket`, `UpdateBucket`, `DeleteBucket`,
  `AddBucketAlias`, `RemoveBucketAlias`)
- `internal/resources/bucket/` package implementing the Plugin Framework
  resource lifecycle (Create / Read / Update / Delete / ImportState)
- HCL surface: `global_aliases` (Set[String]), `max_size` / `max_objects`
  (nullable Int64), `force_destroy` (Bool, default false), plus computed
  `id`, `created`, `bytes`, `objects`, `unfinished_multipart_uploads`
- Adds-before-removes alias diff order
- `force_destroy` empties the bucket via the S3 data plane (using
  `aws-sdk-go-v2`) before `DeleteBucket`
- Acceptance tests covering: create, read drift, alias add/remove/reorder,
  quota set/clear/zero, import, destroy (empty), force-destroy (non-empty),
  reject destroy on non-empty without force, parallel-safety
- `examples/resources/garage_bucket/resource.tf` and regenerated
  `docs/resources/bucket.md`
- 4 in-implementation verifications from DESIGN-0002 §Decisions

### Out of Scope

- `garage_key`, `garage_bucket_key`, `garage_bucket_alias` resources —
  Phases 3-5 of RFC-0001
- CORS rules, lifecycle rules, website config — additive future scope
- Local aliases (per-key) — Phase 5 of RFC-0001
- Cross-resource validation (e.g. external alias vs inline alias conflicts)
  — Phase 5
- Multi-version Garage matrix in CI — deferred

## Implementation Phases

Each phase builds on the previous one. A phase is complete when all its
tasks are checked off and its success criteria are met. Phases land as
separate commits on `feat/design-0002-phase-2-bucket-resource` and ship
as a single PR per §Decisions #5.

---

### Phase 1: Client wrapper bucket methods

Extend `internal/client/client.go` with the six bucket methods the resource
will consume. Pure unit-testable work — uses `httptest.NewServer`, no
container fixture. Mirrors the Phase 1 `GetClusterStatus` pattern in
shape and conventions.

#### Tasks

- [x] Add `func (c *Client) CreateBucket(ctx context.Context) (*openapi.GetBucketInfoResponse, error)`
      — empty request body, returns the new bucket info
- [x] Add `func (c *Client) GetBucket(ctx context.Context, id string) (*openapi.GetBucketInfoResponse, error)`
      — issues `GetBucketInfo?id=<id>`, maps `ErrNotFound` for HTTP 404
- [x] Add `func (c *Client) UpdateBucket(ctx context.Context, id string, quotas *openapi.ApiBucketQuotas) (*openapi.GetBucketInfoResponse, error)`
      — body shape per DESIGN-0002 §Quota semantics; CORS / lifecycle /
      website fields sent as `nil`
- [x] Add `func (c *Client) DeleteBucket(ctx context.Context, id string) error`
      — no return body on 2xx (Garage uses RPC-style POST for delete,
      surfaced in the test as an explicit method assertion)
- [x] Add `func (c *Client) AddBucketAlias(ctx context.Context, bucketID, globalAlias string) error`
      — body is `BucketAliasEnum0 { BucketId, GlobalAlias }`
- [x] Add `func (c *Client) RemoveBucketAlias(ctx context.Context, bucketID, globalAlias string) error`
- [x] Unit tests in `internal/client/client_test.go` for each method:
  - Happy path returns expected struct
  - 404 maps to `ErrNotFound` (Get only)
  - 401 / 403 map to `ErrUnauthorized` / `ErrForbidden`
  - Retry-on-5xx behavior preserved for GetBucket (idempotent verb)
  - POST/DELETE methods pass through 5xx with no retry
  - Quota body shape (nil / empty / literal-zero / non-zero) round-trips
    correctly through `UpdateBucket`
  - Empty-arg validation gate for id / bucketID / globalAlias
- [x] Run `just lint` / `just test` — verify all green (0 lint issues;
      84.2% client coverage)
- [x] Commit as `feat: add bucket client wrapper methods (IMPL-0002 Phase 1)`

#### Success Criteria

- All 6 wrapper methods exist with doc comments
- Unit-test coverage for client package stays ≥80% (Phase 1 baseline)
- `just lint` / `just test` exit 0
- Generated client (`internal/client/openapi/generated.go`) untouched
  — these methods consume existing generated types

---

### Phase 2: Live-API behavior verifications

Four DESIGN-0002 decisions named "verify in implementation". Resolve each
against a live `dxflrs/garage:v2.3.0` container before writing the resource
code that consumes the answers. Findings get folded into IMPL-0002 §Decisions
(this doc) and inform Phase 5/6 work.

#### Tasks

- [ ] **Verification A — Last-alias removal.** With a single-alias bucket,
      issue `RemoveBucketAlias`. Does Garage:
  (i) refuse (4xx),
  (ii) succeed and leave a no-alias bucket reachable only by ID, or
  (iii) succeed and delete the bucket entirely?
      Document the result. Adjust Phase 5 alias-diff ordering if (i) turns
      out not to apply
- [ ] **Verification B — `AddBucketAlias` idempotency for same bucket.**
      Add an alias, then add the same alias again to the same bucket.
      Does Garage return 2xx (no-op idempotent) or 4xx? Document
- [ ] **Verification C — `DeleteBucket` on non-empty bucket.** Spec text
      already says it refuses ("A bucket cannot be deleted if it is not
      empty"). Confirm and capture the exact error response shape so the
      resource can emit a useful diagnostic. Confirm there is no force/
      recursive flag we missed
- [ ] **Verification D — `AddBucketAlias` of alias owned by another
      bucket.** What HTTP status / error body does Garage return? Captured
      for diagnostic clarity, not control flow
- [ ] Capture verifications as scratch test functions inside
      `internal/resources/bucket/livecheck_test.go` (build-tag
      `garageprobe`, separate from the main acceptance suite — runnable on
      demand via `go test -tags=garageprobe`). The tests document the
      observed behavior in their assertions
- [ ] Replace DESIGN-0002 §Decisions text on items 2-5 with the resolved
      findings; commit doc + livecheck file together as
      `chore: capture live-API findings for IMPL-0002 (Phase 2)`

#### Success Criteria

- All four behaviors documented in DESIGN-0002 §Decisions with the actual
  observed behavior (status code, response body shape, ordering
  implications)
- `livecheck_test.go` compiles and passes against a live container when
  the `garageprobe` build tag is set
- No production code depends on the build-tagged tests; default builds
  ignore them

---

### Phase 3: Resource scaffold

`internal/resources/bucket/` package with the framework boilerplate:
Metadata, Schema, Configure, Model struct, conversion helpers. No
lifecycle methods yet — those land in Phases 4-7. Resource is **not**
registered in `provider.go` until Phase 4 (incomplete CRUD would surface
runtime errors).

#### Tasks

- [ ] Create `internal/resources/bucket/` package
- [ ] Write `resource.go`:
  - `type Resource struct { client *client.Client }` with `New()` ctor
  - `Metadata()` sets `req.ProviderTypeName + "_bucket"`
  - `Configure()` asserts `*client.Client` from `req.ProviderData`
  - `Schema()` with all attributes from DESIGN-0002 §Schema:
    managed (`global_aliases`, `max_size`, `max_objects`, `force_destroy`),
    computed (`id`, `created`, `bytes`, `objects`,
    `unfinished_multipart_uploads`)
- [ ] Write `model.go`:
  - `Model` struct mirroring schema (see DESIGN-0002 §Data Model)
  - Helpers: `modelToQuotas(Model) *openapi.ApiBucketQuotas`,
    `applyBucketInfoToModel(*openapi.GetBucketInfoResponse, *Model)`
- [ ] Apply `UseStateForUnknown` plan modifier on the `id` attribute
      (suppresses "(known after apply)" once the bucket has been created)
- [ ] Unit test schema marshaling round-trip (Model ↔ openapi types) in
      `model_test.go`
- [ ] Run `just lint` / `just test` — verify all green
- [ ] Commit as `feat: scaffold garage_bucket resource package (IMPL-0002 Phase 3)`

#### Success Criteria

- `internal/resources/bucket/` package compiles
- Schema attribute set matches DESIGN-0002 exactly; MarkdownDescription
  strings are present and proofread for typos
- Model ↔ openapi conversion helpers unit-tested
- Resource is **not** yet registered in `provider.Resources()` — confirmed
  by `grep` showing the slot still returns nil

---

### Phase 4: Create + Read

Implement the two simplest lifecycle methods and register the resource so
basic provisioning works end-to-end. This is the smallest possible "the
resource works" milestone.

#### Tasks

- [ ] Implement `Create()` per DESIGN-0002 §Create flow:
  1. `client.CreateBucket(ctx)` → capture new bucket ID
  2. For each `global_alias` in plan: `client.AddBucketAlias`
  3. If `max_size != null || max_objects != null`:
     `client.UpdateBucket(ctx, id, quotas)`
  4. Refresh state via `client.GetBucket`
  - On any step 2-4 failure where bucket exists: best-effort rollback via
    `client.DeleteBucket` (bucket has no objects yet — safe). Document in
    the function comment
- [ ] Implement `Read()`:
  - Read state's `id`; call `client.GetBucket`
  - On `ErrNotFound`: `resp.State.RemoveResource(ctx)` (drift cleanup —
    bucket deleted out-of-band)
  - Otherwise: `applyBucketInfoToModel` and write to state
- [ ] Register the resource in `internal/provider/provider.go`
      `Resources()`
- [ ] Add acceptance test `TestAccGarageBucket_minimal` — creates a bucket
      with no aliases or quotas, verifies state. `t.Parallel()` from day one
- [ ] Add acceptance test `TestAccGarageBucket_createWithAliasesAndQuotas`
      — creates with two aliases + both quotas, verifies state matches
- [ ] Run `just lint` / `just test` / `just testacc` — verify all green
- [ ] Commit as `feat: garage_bucket Create + Read lifecycle (IMPL-0002 Phase 4)`

#### Success Criteria

- `TestAccGarageBucket_minimal` and `TestAccGarageBucket_createWithAliasesAndQuotas`
  pass against a live container
- Manual smoke test deferred to Phase 8 (covered by the acceptance suite
  by then)
- Create rollback on partial failure documented; smoke-tested by
  temporarily forcing a step 3 failure (e.g. invalid quota value) and
  observing the rollback DeleteBucket call

---

### Phase 5: Update

Implement the alias-diff and quota-update flow per DESIGN-0002 §Update.
Single phase covers both since they share the Update() entry point and
test surface.

#### Tasks

- [ ] Compute alias diff: `addSet = plan.aliases - state.aliases`,
      `removeSet = state.aliases - plan.aliases`
- [ ] Issue `AddBucketAlias` for each `addSet` member before
      `RemoveBucketAlias` for each `removeSet` member (adds-before-removes;
      see DESIGN-0002 §Update flow rationale)
- [ ] If `plan.quotas != state.quotas`: call `client.UpdateBucket` with the
      plan's `ApiBucketQuotas`. Translation per DESIGN-0002 §Quota semantics
      (null = clear, zero = literal zero, set = literal value)
- [ ] Refresh state via `client.GetBucket` after mutations
- [ ] Acceptance test `TestAccGarageBucket_updateAliases`: 1→2 aliases,
      2→1 alias, alias rename (add new + remove old), reorder no-op
- [ ] Acceptance test `TestAccGarageBucket_updateQuotas`: set both quotas,
      clear `max_size` only, clear both, set `max_size = 0` (literal zero)
- [ ] Acceptance test `TestAccGarageBucket_driftDetection`: external
      alias remove (via `client.RemoveBucketAlias` outside Terraform),
      followed by `terraform plan` showing the drift
- [ ] Run all gates green
- [ ] Commit as `feat: garage_bucket Update lifecycle (IMPL-0002 Phase 5)`

#### Success Criteria

- All three Update acceptance tests pass
- Set semantics confirmed: reordering aliases in HCL produces zero plan
  diff
- Quota clear semantics: setting `max_size = null` issues `UpdateBucket`
  with `nil` in the field; Garage clears the quota; subsequent
  `GetBucket` reports no quota
- Drift detection works for alias and quota changes

---

### Phase 6: Delete + force_destroy

`Delete()` lifecycle method, plus the provider-level S3 attrs needed for
`force_destroy` and a bucket-emptying helper backed by
`github.com/aws/aws-sdk-go-v2`. Phase 2's Verification C informs the
diagnostic message shape on the "non-empty without force" path.

#### Tasks

- [ ] Add `github.com/aws/aws-sdk-go-v2`, `aws-sdk-go-v2/config`,
      `aws-sdk-go-v2/credentials`, `aws-sdk-go-v2/service/s3` to `go.mod`
- [ ] Extend `provider.Schema()` and `provider.Configure()` with three
      optional provider attributes:
  - `s3_endpoint` (String, env fallback `GARAGE_S3_ENDPOINT`)
  - `s3_access_key` (String, env fallback `GARAGE_S3_ACCESS_KEY`)
  - `s3_secret_key` (String, `Sensitive = true`, env fallback
    `GARAGE_S3_SECRET_KEY`)
      Plumb into `providerData` alongside the admin `*client.Client` so
      resources can access them. None are required at provider config
      time — only at Delete time when `force_destroy = true` is invoked
      on a non-empty bucket
- [ ] Implement `internal/resources/bucket/s3empty.go`:
      `emptyBucket(ctx, s3cfg, bucketName) error` using
      `s3.ListObjectsV2Paginator` to enumerate keys + `s3.DeleteObjects`
      to batch-delete (up to 1000 per request). Loop until paginator
      reports no more pages. Configure the S3 client with
      `BaseEndpoint = aws.String(s3Endpoint)`,
      `Credentials = credentials.NewStaticCredentialsProvider(access, secret, "")`,
      `Region = "garage"` (placeholder — Garage doesn't enforce AWS region
      names), and S3 option `UsePathStyle = true` (Garage requires
      path-style addressing)
- [ ] Implement `Delete()`:
  - Always issue `GetBucket` first to check `objects` count
  - If `objects == 0`: `client.DeleteBucket` directly
  - If `objects > 0` and `force_destroy = false`: emit diagnostic
    `"bucket 'X' is not empty (Y objects); set force_destroy = true to delete"`
    and return without calling `DeleteBucket`
  - If `objects > 0` and `force_destroy = true`: validate
    `s3_endpoint` / `s3_access_key` / `s3_secret_key` are configured;
    if any are missing, emit a diagnostic naming the missing attrs;
    otherwise call `emptyBucket(...)` then `client.DeleteBucket`
- [ ] Acceptance test `TestAccGarageBucket_deleteEmpty`: create empty,
      destroy, verify gone (no S3 creds needed)
- [ ] Acceptance test `TestAccGarageBucket_rejectNonEmptyWithoutForce`:
      create bucket, put an object via an acctest S3 helper, attempt
      destroy, expect failure with the documented diag
- [ ] Acceptance test `TestAccGarageBucket_forceDestroyNonEmpty`: same
      setup with `force_destroy = true` and S3 creds configured —
      expect successful destroy
- [ ] Acceptance test `TestAccGarageBucket_forceDestroyMissingS3Creds`:
      `force_destroy = true` on a non-empty bucket with no S3 creds on
      the provider — expect an actionable diagnostic, not a panic
- [ ] Run all gates green
- [ ] Commit as `feat: garage_bucket Delete + force_destroy (IMPL-0002 Phase 6)`

#### Success Criteria

- Empty-bucket destroy succeeds without S3 creds (admin API only)
- Non-empty bucket with `force_destroy = true` is emptied (S3 data plane)
  and deleted (admin API) in the resource's normal Delete call
- Non-empty bucket with `force_destroy = false` fails with an actionable
  diagnostic naming the bucket and the object count
- Missing S3 creds when `force_destroy` triggers them produces an
  actionable diagnostic, not a panic or generic 4xx

---

### Phase 7: ImportState

Bare-ID import (`terraform import garage_bucket.example <bucket_id>`).
Small phase; could be folded into Phase 4 if review pressure prefers
fewer commits.

#### Tasks

- [ ] Implement `ImportState()`:
  - Validate the import ID is a non-empty hex string (Garage bucket IDs
    are 64-char hex; accept any non-empty string and let `Read` surface
    "not found" if it's malformed)
  - `resp.State.SetAttribute(ctx, path.Root("id"), req.ID)`
  - Default `force_destroy` to false (provider-only attribute, not
    derivable from Garage)
- [ ] Acceptance test `TestAccGarageBucket_import`:
  - Step 1: create a bucket with aliases + quotas
  - Step 2: `ImportStateVerify` against the bucket ID; expect all
    attributes to match
- [ ] Run all gates green
- [ ] Commit as `feat: garage_bucket ImportState (IMPL-0002 Phase 7)`

#### Success Criteria

- Import by bucket ID round-trips: imported state matches the original
  resource state byte-for-byte (aside from `force_destroy`, which is
  always false on import)
- `TestAccGarageBucket_import` passes

---

### Phase 8: Acceptance suite + docs

Polish phase. Comprehensive acceptance tests; `tfplugindocs`-generated
provider docs; CI green on the PR.

#### Tasks

- [ ] Write `examples/resources/garage_bucket/resource.tf` — minimal
      example matching the `tfplugindocs` convention (single resource
      block, no `terraform { required_providers }`, no `variable`
      declarations, plus an `import.sh` for the import command line)
- [ ] Run `just generate`; verify `docs/resources/bucket.md` is generated
      and reads well
- [ ] Spot-check generated docs for Markdown rendering issues
- [ ] Add `TestAccGarageBucket_zeroQuotaSemantics`: `max_size = 0`,
      verify Garage reports quota = 0 (literal zero, not "no quota")
- [ ] Add `TestAccGarageBucket_aliasReorderNoOp`: same alias set in
      different orders should produce no plan diff
- [ ] Add `TestAccGarageBucket_parallelSafety`: 3 buckets in one config
      with `count = 3`; ensure parallel-apply doesn't trip alias
      collisions or race on shared state
- [ ] Run `just generate` one more time; commit `docs/resources/bucket.md`
- [ ] CI green on the PR: `lint`, `test-go`, `security`, `build`,
      `generate`, `acceptance` (TF 1.13 + 1.14) all pass
- [ ] Commit final docs/test additions as
      `feat: garage_bucket acceptance polish + docs (IMPL-0002 Phase 8)`

#### Success Criteria

- `docs/resources/bucket.md` exists and reflects the schema accurately
- `examples/resources/garage_bucket/resource.tf` renders cleanly in docs
- All `TestAccGarageBucket_*` tests pass; total acceptance-test wall time
  per matrix entry under 90s (10 tests × ~10s container start)
- CI `acceptance` job green on the PR
- `just generate` idempotent (zero diff on re-run)

---

## File Changes

| File                                                          | Action | Notes                                                          |
|---------------------------------------------------------------|--------|----------------------------------------------------------------|
| `internal/client/client.go`                                   | Modify | Phase 1 — add 6 bucket methods                                 |
| `internal/client/client_test.go`                              | Modify | Phase 1 — unit tests for the new methods                       |
| `internal/resources/bucket/resource.go`                       | Create | Phase 3 — Schema/Metadata/Configure + lifecycle (Phases 4-7)   |
| `internal/resources/bucket/model.go`                          | Create | Phase 3 — Model struct + conversion helpers                    |
| `internal/resources/bucket/model_test.go`                     | Create | Phase 3 — round-trip tests                                     |
| `internal/resources/bucket/resource_test.go`                  | Create | Phase 4 — acceptance test suite (grows phase by phase)         |
| `internal/resources/bucket/livecheck_test.go`                 | Create | Phase 2 — build-tagged probe tests                             |
| `internal/resources/bucket/s3empty.go`                        | Create | Phase 6 — `aws-sdk-go-v2`-backed bucket-emptying helper        |
| `internal/provider/provider.go`                               | Modify | Phase 4 — register the resource; Phase 6 — add `s3_*` attrs    |
| `go.mod` / `go.sum`                                           | Modify | Phase 6 — add `aws-sdk-go-v2` and its `config`/`credentials`/`service/s3` subpackages |
| `examples/resources/garage_bucket/resource.tf`                | Create | Phase 8 — usage example                                        |
| `examples/resources/garage_bucket/import.sh`                  | Create | Phase 8 — import command-line example                          |
| `docs/resources/bucket.md`                                    | Create | Phase 8 — generated by `tfplugindocs`                          |
| `docs/design/0002-*.md`                                       | Modify | Phase 2 — replace verification placeholders with findings      |
| `docs/impl/0002-*.md`                                         | Modify | Each phase — check off completed tasks                         |

## Testing Plan

| Layer         | Where                                                     | When     |
|---------------|-----------------------------------------------------------|----------|
| Unit          | `internal/client/client_test.go` (6 new method tests)     | Phase 1  |
| Unit          | `internal/resources/bucket/model_test.go`                 | Phase 3  |
| Live probe    | `internal/resources/bucket/livecheck_test.go` (tagged)    | Phase 2  |
| Acceptance    | `internal/resources/bucket/resource_test.go`              | 4-8      |
| CI acceptance | matrix over TF 1.13 + 1.14 (already wired in Phase 1)     | Phase 8  |
| Docs drift    | `generate` job in CI (already present)                    | Phase 8  |

## Dependencies

- **Phase 1 of RFC-0001 (DESIGN-0001 / IMPL-0001)** — provides the client
  wrapper conventions, acctest fixture, and provider scaffold
- **External:** Docker daemon for acceptance tests (local + CI)
- **External:** `github.com/aws/aws-sdk-go-v2` (`config`, `credentials`,
  `service/s3`) — Phase 6 `force_destroy` bucket-emptying via the S3
  data plane
- **Garage upstream:** API behavior at v2.3.0 must match the spec; any
  divergence surfaces in Phase 2's live verification

## Decisions

All eight open questions raised at impl-planning time have been resolved
and folded into the phase tasks above. They're recorded here for audit
and to give future-readers the reasoning trail.

1. **[Phase 6] S3 client for `force_destroy`.** Use the official
   `github.com/aws/aws-sdk-go-v2` SDK (`service/s3`, `config`,
   `credentials`). Apache-2.0 license, zero vendor-relicensing risk
   (AWS isn't going closed-source), and Garage commits to S3 API
   compatibility — so the canonical AWS client is the highest-fidelity
   exerciser of Garage's S3 data plane. `minio-go` is still
   Apache-2.0 in 2026 but MinIO Inc.'s server relicensing trajectory
   (AGPLv3 since 2021, aggressive commercial steering) makes AWS the
   safer long-term bet. Hand-rolled SigV4 rejected — 200 LoC of crypto
   signing we don't want to own.
   *Source: owner call; license check (gh api repos/minio/minio-go
   confirmed Apache-2.0; gh api repos/minio/minio confirmed AGPLv3).*

2. **[Phase 3] `UseStateForUnknown` plan modifier on `id`.** Apply it.
   Standard pattern that keeps `terraform plan` from showing
   `"(known after apply)"` on the bucket ID once the resource has been
   created. Matches HashiCorp Plugin Framework conventions.
   *Source: owner call.*

3. **[Phase 6] S3 credentials for `force_destroy`.** Add `s3_endpoint`,
   `s3_access_key`, `s3_secret_key` as optional provider-level
   attributes with env fallback (`GARAGE_S3_ENDPOINT`,
   `GARAGE_S3_ACCESS_KEY`, `GARAGE_S3_SECRET_KEY`). Symmetrical to the
   admin token attribute, opt-in (only consumed when a bucket actually
   needs emptying at destroy time). Per-resource attrs rejected —
   duplicates credentials across bucket resources for no scoping benefit.
   *Source: owner call.*

4. **[Phase 3, Phase 4] Plan churn on `bytes` / `objects` /
   `unfinished_multipart_uploads`.** Accept the noise. These computed
   attrs mutate when users write to buckets via S3; surfacing the drift
   is informational and correct. Same trade-off as the AWS provider's
   computed S3 metrics.
   *Source: owner call.*

5. **[Phase 8] PR strategy.** Single PR for all eight phases, same as
   IMPL-0001. Split into 1-4 then 5-8 only if review feedback during
   the work suggests it. Single PR preserves momentum and keeps the
   change set reviewable as a coherent unit.
   *Source: owner call.*

6. **[Phase 2] Live-API verification location.** Build-tagged
   `livecheck_test.go` (`//go:build garageprobe`). Lives in the repo,
   never runs by default, on-demand `go test -tags=garageprobe`. The
   tests double as durable documentation of observed Garage behavior
   and re-run cleanly when bumping the Garage version pin.
   *Source: owner call.*

7. **[Phase 6] `force_destroy` toggle plan diff.** Use the framework's
   default state-only behavior. Flipping `force_destroy = true → false`
   on an otherwise-unchanged bucket shows as
   `~ force_destroy = true -> false`, applies with zero API calls, and
   updates state. No custom plan modifier.
   *Source: owner call; framework default.*

8. **[Phase 7] Bucket ID validation on import.** Accept any non-empty
   string. `Read` surfaces "not found" cleanly for malformed IDs.
   Format validation is brittle (Garage may change ID shape across
   versions); the round-trip via `Read` is already authoritative.
   *Source: owner call.*

## Open Questions

None — all initial open questions resolved into the Decisions section
above. Four behavior verifications remain as in-phase tasks rather than
upfront blockers:

- **[Phase 2]** Last-alias removal behavior — refuse / no-alias bucket
  reachable by ID / delete the bucket entirely?
- **[Phase 2]** `AddBucketAlias` idempotency when called twice with the
  same alias for the same bucket
- **[Phase 2]** Exact error response shape from `DeleteBucket` on a
  non-empty bucket (spec text says it refuses; captures the error body
  for actionable diagnostics)
- **[Phase 2]** Error response shape from `AddBucketAlias` when the
  alias is already owned by a different bucket

## References

- [DESIGN-0002: Phase 2 implementation — garage_bucket resource](../design/0002-phase-2-implementation-garagebucket-resource.md)
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 2 entry
- [IMPL-0001: Phase 1 implementation](0001-phase-1-provider-scaffold-openapi-client-smoke-test.md)
  — conventions reused (commit shape, file headers, fixture model, retry
  policy, depguard rule, MarkdownDescription style)
- [ADR-0002: Use terraform-plugin-framework over SDKv2](../adr/0002-use-terraform-plugin-framework-over-sdkv2.md)
- [ADR-0003: OpenAPI-generated client via oapi-codegen](../adr/0003-openapi-generated-client-via-oapi-codegen.md)
- [ADR-0005: testcontainers-go for acceptance tests](../adr/0005-testcontainers-go-for-acceptance-tests.md)
- [`internal/client/openapi/garage-admin-v2.json`](../../internal/client/openapi/garage-admin-v2.json)
  — vendored spec; bucket endpoint definitions
- [aws-sdk-go-v2 S3 service documentation](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3)
  — Phase 6 `force_destroy` implementation

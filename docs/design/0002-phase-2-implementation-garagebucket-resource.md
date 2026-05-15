---
id: DESIGN-0002
title: "Phase 2 implementation: garage_bucket resource"
status: Draft
author: Donald Gifford
created: 2026-05-15
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0002: Phase 2 implementation: garage_bucket resource

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-15

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Resource lifecycle mapping](#resource-lifecycle-mapping)
  - [Schema](#schema)
  - [Aliases inline vs separate resource](#aliases-inline-vs-separate-resource)
  - [Quota semantics](#quota-semantics)
  - [Create flow](#create-flow)
  - [Update flow](#update-flow)
  - [Delete flow](#delete-flow)
  - [Import](#import)
  - [Error handling and idempotency](#error-handling-and-idempotency)
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

Phase 2 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md) introduces the
first resource in the provider: `garage_bucket`. It models a Garage bucket as a
Terraform-managed resource with full CRUD, inline global aliases, and quota
attributes (`max_size`, `max_objects`). Phase 1 (DESIGN-0001 / IMPL-0001)
delivered the provider scaffold, client wrapper, and acceptance fixture; this
phase consumes all three to land the first stateful primitive users can
declare in HCL.

No other v0.1 resources (`garage_key`, `garage_bucket_key`, `garage_bucket_alias`)
land here — they ship in Phases 3-5 of RFC-0001.

## Goals and Non-Goals

### Goals

- `garage_bucket` resource with Create / Read / Update / Delete / Import wired
  against the Garage admin v2 API
- Schema mirrors the spec's `GetBucketInfoResponse` for read-only attrs and
  exposes managed attrs (`global_aliases`, `max_size`, `max_objects`) as
  Optional in the HCL surface
- Inline `global_aliases` set on the bucket resource (no separate alias
  resource yet)
- Quotas via `UpdateBucket` with explicit nullable semantics (config-set `null`
  → "remove the quota", not "leave unchanged")
- `force_destroy` attribute to opt into deleting buckets that still contain
  objects (mirrors AWS S3 provider convention; default `false`)
- Acceptance tests covering: create, read drift, alias add/remove, quota
  set/clear, import, destroy (empty + force-destroy), `t.Parallel()` from day 1
- Generated docs (`docs/resources/bucket.md`) via `tfplugindocs`

### Non-Goals

- Local aliases (per-key) — Phase 5 of RFC-0001 (`garage_bucket_alias` /
  `garage_bucket_key` overlap)
- Separate `garage_bucket_alias` resource for global aliases owned by a
  different team — Phase 5 of RFC-0001
- CORS rules, lifecycle rules, website config — out of Phase 2 scope. The
  Garage `UpdateBucket` body supports these fields; we ignore them in the
  schema and leave the API defaults untouched on Update. Adding them is a
  later phase
- Bucket key permissions (`garage_bucket_key`) — Phase 4 of RFC-0001
- Cross-cluster replication, multi-region semantics — not modeled in Garage's
  admin v2 API at all

## Background

After Phase 1 the repo has:

- `internal/client/client.go` with `Client.GetClusterStatus` and the wrapper
  conventions (bearer auth, retry-on-5xx for idempotent verbs, typed error
  sentinels, `tflog` tracing)
- `internal/provider/provider.go` registering the data sources slot but with
  an empty `Resources()` slice
- `internal/acctest/` fixture starting `dxflrs/garage:v2.3.0` per-test
- The full generated OpenAPI client in `internal/client/openapi/generated.go`,
  including every bucket method we'll consume

Garage's bucket model, summarized from the spec:

| Concept           | Type / shape                                       | Owned by          |
|-------------------|----------------------------------------------------|-------------------|
| Bucket ID         | Opaque hex string, immutable, primary key          | Garage            |
| Created           | RFC 3339 timestamp                                 | Garage (computed) |
| Global aliases    | Set of cluster-unique strings (e.g. DNS names)     | User (managed)    |
| Local aliases     | Per-access-key aliases — different namespace       | User (Phase 5)    |
| Quotas            | `{maxSize, maxObjects}`, each individually nullable | User (managed)   |
| CORS / lifecycle / website | Inline structs                            | User (deferred)   |
| Usage stats       | `bytes`, `objects`, `unfinishedMultipartUploads`   | Garage (computed) |

The relevant admin API endpoints:

| Operation             | HTTP            | Returns                  |
|-----------------------|-----------------|--------------------------|
| `CreateBucket`        | POST            | `GetBucketInfoResponse`  |
| `GetBucketInfo`       | GET `?id=` or `?globalAlias=` | `GetBucketInfoResponse` |
| `UpdateBucket`        | POST `?id=`     | `GetBucketInfoResponse`  |
| `DeleteBucket`        | DELETE `?id=`   | empty                    |
| `AddBucketAlias`      | POST            | `GetBucketInfoResponse`  |
| `RemoveBucketAlias`   | POST            | `GetBucketInfoResponse`  |
| `ListBuckets`         | GET             | `[]ListBucketsResponseItem` |

`CreateBucket` accepts an optional `globalAlias` (single string) or a single
`localAlias`; multiple aliases require follow-up `AddBucketAlias` calls.

## Detailed Design

### Resource lifecycle mapping

Each Terraform lifecycle method translates to one or more admin API calls:

| TF method          | Garage call(s)                                                                       |
|--------------------|--------------------------------------------------------------------------------------|
| Create             | `CreateBucket` (no alias inline), then `AddBucketAlias` for each alias, then `UpdateBucket` if quotas set |
| Read               | `GetBucketInfo?id=<id>`                                                              |
| Update             | Diff aliases → `AddBucketAlias` / `RemoveBucketAlias`; diff quotas → `UpdateBucket`  |
| Delete             | If `force_destroy=false` and bucket has objects, fail with diag; else `DeleteBucket` |
| ImportState        | Parse import ID → set `id` → trigger Read                                            |

Create is intentionally split into three steps rather than packing the first
alias into the `CreateBucket` body. Reasons:
1. Uniform handling regardless of `len(global_aliases)` — zero, one, or many
   aliases all go through the same code path
2. Avoids special-casing "the first alias is in the create response, the
   rest are added"
3. Costs one extra round-trip in the single-alias case (3-5ms LAN); acceptable

### Schema

HCL surface:

```hcl
resource "garage_bucket" "example" {
  global_aliases = ["data.example.com", "data-prod"] # Optional, default []

  max_size    = 10737418240   # Optional, bytes; null = no size quota
  max_objects = 1000000       # Optional; null = no object-count quota

  force_destroy = false       # Optional, default false
}
```

Computed (read-only) attributes:

- `id` — bucket UUID assigned by Garage
- `created` — RFC 3339 timestamp
- `bytes` — current usage in bytes
- `objects` — current object count
- `unfinished_multipart_uploads` — count

Notably **not** in v0.1 schema:
- `cors_rules`, `lifecycle_rules`, `website_access`, `website_config` —
  deferred; UpdateBucket will not touch these fields (we send them as
  `nil` in the request body, which Garage interprets as "leave unchanged")
- `local_aliases`, `keys` — Phase 4 / 5

### Aliases inline vs separate resource

Phase 2 keeps global aliases **inline** on the bucket resource. This is the
ergonomic default — 90% of HCL configs declare a bucket and its aliases as a
single logical unit.

Phase 5 of RFC-0001 introduces a separate `garage_bucket_alias` resource for
the niche where aliases are managed independently of the bucket (e.g. the
bucket lives in one Terraform workspace and aliases are managed by another).
When both forms exist, the user picks one per bucket — mixing inline and
external aliases on the same bucket is unsupported. Phase 5's DESIGN doc will
specify the conflict-detection strategy.

### Quota semantics

Garage stores quotas as `{maxSize: int64|null, maxObjects: int64|null}`.
Three observable states per quota field:

| HCL                      | Plan      | Effect on Garage                            |
|--------------------------|-----------|---------------------------------------------|
| `max_size = 1000`        | known     | Set quota to 1000 bytes                     |
| `max_size = null`        | known-null | Clear the quota (send `nil` in the API body) |
| (attribute omitted)      | null/unknown | Same as above — clear quota             |

We do **not** offer a "leave existing quota untouched" mode. Terraform's
declarative model requires that state reflects config: an omitted attribute
means "I don't want this set". Users who want sticky quotas managed
out-of-band should not declare `max_size` / `max_objects` on the resource
and accept that drift in the admin UI surfaces as plan diffs.

A separate concern: Garage's `UpdateBucket` body has `quotas *ApiBucketQuotas`
(itself nullable) and inside that `MaxSize *int64`, `MaxObjects *int64`. Three
levels of pointer. The wrapper translates:

- Both quotas omitted in HCL → send `quotas: { maxSize: nil, maxObjects: nil }`
- One set, one omitted → send both, with the omitted one as nil
- Both set → send both with literal values

### Create flow

```
1. POST /v2/CreateBucket { }                      // empty request body
   ← 200, body contains the new bucket ID
2. For each global_alias in config:
   POST /v2/AddBucketAlias { bucketId, globalAlias }
3. If max_size != null || max_objects != null:
   POST /v2/UpdateBucket?id=<id> { quotas: {...} }
4. Read the final state via GetBucketInfo and write to TF state
```

Failure modes and rollback:
- Step 2 fails (e.g. alias collision): attempt `DeleteBucket` to clean up, then
  return the error. The bucket has no objects yet so the delete is safe.
- Step 3 fails (e.g. quota validation): same rollback path
- Step 4 fails (network blip): the bucket was created successfully — return
  the error but the resource is in an inconsistent state. Documented behavior;
  Terraform's retry on next apply will pick up the partial work via Read

### Update flow

Schema diff is computed by the framework; the resource's `Update()` translates
the diff into the smallest set of admin calls:

```
aliasesToAdd    = configAliases - stateAliases
aliasesToRemove = stateAliases  - configAliases

for alias in aliasesToAdd:
    POST /v2/AddBucketAlias { bucketId, globalAlias: alias }

for alias in aliasesToRemove:
    POST /v2/RemoveBucketAlias { bucketId, globalAlias: alias }

if quotas changed:
    POST /v2/UpdateBucket?id=<id> { quotas: configQuotas }

POST /v2/GetBucketInfo?id=<id>  // refresh state
```

Order: adds before removes. This avoids transiently leaving the bucket with
zero aliases when a user renames "a" → "b" — Garage refuses removing the last
alias if it's the only handle other than the bucket ID, depending on cluster
config. Adding the new alias first sidesteps that edge case. Worth confirming
against the actual Garage behavior — see open questions.

### Delete flow

```
1. GET /v2/GetBucketInfo?id=<id>
2. If !force_destroy && info.objects > 0:
   return diag.Error("bucket is not empty; set force_destroy=true to delete")
3. DELETE /v2/DeleteBucket?id=<id>
```

`force_destroy=true` deletes regardless of contents. The Garage admin API
will presumably handle the actual emptying — verify in implementation.

### Import

`terraform import garage_bucket.example <bucket_id>`

`ImportState` sets the `id` attribute on the resource and triggers `Read()`.
The bucket's global aliases, quotas, and computed attributes flow in from
`GetBucketInfo`. `force_destroy` is a TF-only attribute (not stored on
Garage's side) and defaults to `false` on import — users who want to import
a bucket with `force_destroy=true` must follow up with a manual config edit
or `terraform apply` after import.

Open question: support `terraform import garage_bucket.example alias:<name>`
to let users import by global alias? Useful but adds parsing complexity to
`ImportState`. Default no; revisit in Phase 5 if there's demand.

### Error handling and idempotency

The Phase 1 client wrapper translates HTTP status codes into typed sentinels
(`ErrNotFound`, `ErrUnauthorized`, etc.). Phase 2 extends this surface with
bucket-specific operations but doesn't add new error sentinels — existing
ones cover the cases:

| Garage behavior                            | Sentinel              | Resource handling                       |
|--------------------------------------------|-----------------------|------------------------------------------|
| GetBucketInfo on deleted bucket            | `ErrNotFound`         | Read: remove from state (drift cleanup)  |
| CreateBucket with duplicate global alias   | likely 4xx (probably 409 or 400) | Create: bubble up as plan error |
| DeleteBucket on non-empty without force    | likely 4xx            | Delete: surface diag with `force_destroy` hint |
| AddBucketAlias of existing alias on same bucket | likely 2xx (idempotent) — verify | No-op |
| AddBucketAlias of alias owned by another bucket | likely 4xx          | Update: surface diag                     |

**Implementation note:** before writing the Update path, write a small
exploratory test in `internal/client/client_test.go` (or as part of the
acceptance suite) to confirm the actual response codes for the question-marked
rows above. Document the findings inline.

### Client wrapper extensions

`internal/client/client.go` grows new methods:

```go
func (c *Client) CreateBucket(ctx context.Context) (*openapi.GetBucketInfoResponse, error)
func (c *Client) GetBucket(ctx context.Context, id string) (*openapi.GetBucketInfoResponse, error)
func (c *Client) UpdateBucket(ctx context.Context, id string, quotas *openapi.ApiBucketQuotas) (*openapi.GetBucketInfoResponse, error)
func (c *Client) DeleteBucket(ctx context.Context, id string) error
func (c *Client) AddBucketAlias(ctx context.Context, bucketID, globalAlias string) error
func (c *Client) RemoveBucketAlias(ctx context.Context, bucketID, globalAlias string) error
```

Each method:
- Takes `context.Context` first
- Returns typed Garage response structs (not the oapi-codegen `HTTPResponse` wrappers)
- Maps non-2xx → `*APIError` via the existing `statusToError` helper
- Emits one `tflog.Trace` line on request and one on response
- Inherits retry-on-5xx for GET; POST/DELETE pass through (no retry, per
  IMPL-0001 #8)

The wrapper does **not** know about Terraform types — it speaks the OpenAPI
domain. Translation between `openapi.*` types and `types.String` /
`types.Int64` happens in the resource layer.

### Package layout

```
internal/
  client/
    client.go         # extends with the 6 bucket methods above
    client_test.go    # adds bucket-flow unit tests against httptest
  resources/
    bucket/
      resource.go      # `Resource` struct, schema, lifecycle
      model.go         # tfsdk struct + helpers for ApiBucketQuotas ↔ Model
      resource_test.go # acceptance tests (TF_ACC gated)
```

The `internal/resources/bucket/` subpackage follows the same shape as
`internal/datasources/clusterinfo/`: `Resource` struct with `Metadata`,
`Schema`, `Configure`, `Create`, `Read`, `Update`, `Delete`, `ImportState`
methods, plus a separate `model.go` for the schema struct and conversion
helpers.

Package name is `bucket` (single word, no underscore — staticcheck ST1003
preference, same call we made for `clusterinfo`).

## API / Interface Changes

New Terraform surface:

```hcl
resource "garage_bucket" "example" {
  global_aliases = ["my-bucket"]
  max_size       = 1073741824
  max_objects    = 1000
  force_destroy  = false
}
```

Computed-only outputs:

```hcl
output "bucket_id" {
  value = garage_bucket.example.id
}

output "bucket_created" {
  value = garage_bucket.example.created
}
```

Provider schema is unchanged from Phase 1.

## Data Model

```go
type Model struct {
    ID                         types.String `tfsdk:"id"`
    GlobalAliases              types.Set    `tfsdk:"global_aliases"` // Set[String]
    MaxSize                    types.Int64  `tfsdk:"max_size"`
    MaxObjects                 types.Int64  `tfsdk:"max_objects"`
    ForceDestroy               types.Bool   `tfsdk:"force_destroy"`

    Created                    types.String `tfsdk:"created"`
    Bytes                      types.Int64  `tfsdk:"bytes"`
    Objects                    types.Int64  `tfsdk:"objects"`
    UnfinishedMultipartUploads types.Int64  `tfsdk:"unfinished_multipart_uploads"`
}
```

`global_aliases` is a `types.Set[String]` rather than a `types.List[String]`:
order is semantically irrelevant to Garage, and Terraform-side set semantics
mean re-ordering in HCL produces a no-op plan instead of a bogus diff.

## Testing Strategy

| Layer       | What it covers                                          | Location                               |
|-------------|---------------------------------------------------------|----------------------------------------|
| Unit        | Wrapper methods: success paths, error mapping, retry    | `internal/client/client_test.go`       |
| Unit        | Schema validation, plan-time validators                 | `internal/resources/bucket/*_test.go`  |
| Acceptance  | Full create / read / update / delete cycle              | `internal/resources/bucket/resource_test.go` |
| Acceptance  | Alias add / remove / reorder (set semantics)            | same                                   |
| Acceptance  | Quota set / clear (null vs literal)                     | same                                   |
| Acceptance  | Import by ID                                            | same                                   |
| Acceptance  | Force destroy on a non-empty bucket                     | same                                   |
| Acceptance  | Reject destroy on non-empty without `force_destroy`     | same                                   |
| Acceptance  | Drift detection: alias removed via admin UI surfaces in plan | same                              |

Acceptance tests run `t.Parallel()` from the start, mirroring the Phase 1
pattern. Each test gets its own Garage container via `acctest.Start(t)`.

Putting an object into a bucket for the force-destroy / non-empty tests is a
mild complication — the admin API doesn't have a "create object" endpoint
(that's the S3 data plane, not the admin plane). Two options:

1. Use the minio-go S3 client from the test (depends on the access/secret
   keys the fixture already provisions via `--default-access-key`)
2. Skip the non-empty force-destroy tests in Phase 2 and gate them on Phase 4
   when `garage_bucket_key` exists to mint S3 credentials in Terraform

Recommendation: option 1, scoped to the fixture (i.e. the helper to put a
test object lives in `internal/acctest/`, not in production code). Adds the
minio-go S3 client as a test-only dep. Roughly 10 lines of helper code.

## Migration / Rollout Plan

This is a net-new resource; no migration is needed.

Rollout sequence:
1. Land DESIGN-0002 (this doc), get review
2. Author IMPL-0002 with the phase breakdown (single PR or split, TBD by
   IMPL doc — likely 4-5 phases: ADR-0006 if needed, client wrapper
   extensions + tests, resource skeleton + Create, full CRUD + acceptance,
   docs / examples)
3. Land via PR with `feat/design-0002-phase-2-bucket-resource` branch
4. Bump version with `minor` label on PR (post-merge) — new resource is a
   minor in semver pre-1.0

## Decisions

All nine open questions raised at design time have been resolved. Items 2,
3, 4, and 5 carry an in-implementation verification step — they're tasks
in IMPL-0002, not blockers for this design.

1. **CreateBucket inline alias.** Step 1 of Create sends an empty body and
   the alias loop runs unconditionally for every alias. Uniform code path
   regardless of alias count beats saving one round-trip in the
   single-alias case. Re-evaluate if benchmarks ever show the extra hop is
   a meaningful cost.

2. **Alias rename order.** Adds-before-removes is the working hypothesis
   for sidestepping any "last alias on the bucket" guard Garage may
   enforce. Verify the actual behavior during IMPL-0002 (a 5-line
   exploratory test against the live admin API). If Garage doesn't care,
   the ordering is harmless; if it does, we're already on the safe path.

3. **AddBucketAlias of existing alias on the same bucket.** Treat as
   idempotent in the wrapper (2xx → no-op). If implementation reveals
   Garage returns 4xx for the same-bucket case, wrap it as a benign error
   and swallow it in the Update path. Decision is reversible — the wrapper
   absorbs the difference either way.

4. **AddBucketAlias of alias owned by another bucket.** No dedicated
   `ErrAliasConflict` sentinel in Phase 2. The generic `APIError` with the
   Garage error body in `.Message` is sufficient signal for the resource
   to surface a clean diag ("alias 'foo' is already in use by bucket 'bar'").
   Add a typed sentinel later if users repeatedly need to switch on this
   condition.

5. **`force_destroy` mechanics.** Phase 2 assumes Garage's `DeleteBucket`
   either empties the bucket itself or accepts a force/recursive flag.
   Verify in IMPL-0002 against the live API. Fallback if not: add an
   `internal/acctest/`-scoped helper that empties via the S3 data plane
   using minio-go and the fixture's default access/secret keys, then
   `DeleteBucket`. Either path satisfies the resource semantics; only the
   implementation differs.

6. **CORS, lifecycle, website attributes deferred.** Confirmed deferred.
   Phase 2's schema does not expose them; UpdateBucket sends them as `nil`
   in the request body, which Garage interprets as "leave unchanged".
   Adding them is a later phase (Phase 6 or 7 of the RFC, TBD) and won't
   be breaking — they're additive optional attributes.

7. **`max_size = 0` / `max_objects = 0` interpretation.** Literal-zero
   semantics: `0` means "zero quota, bucket is read-only", `null` means
   "no quota". Documented in the resource's MarkdownDescription. Matches
   Garage's own `*int64` typing where `nil` is the absence sentinel and
   `0` is a legitimate value.

8. **Import by global alias.** Bare bucket ID only. The canonical
   identifier in Garage's data model is the ID; aliases are mutable.
   `ImportState` parses a single hex bucket ID. Revisit if users open
   issues asking for alias-based import.

9. **Inline-vs-external alias conflict (Phase 5).** Out of scope for
   Phase 2. Flag in DESIGN-0005's Open Questions when that doc is
   authored: external `garage_bucket_alias` must refuse to manage an alias
   already declared inline on a `garage_bucket` resource in the same
   state. Mechanism (plan-time inspection vs runtime check) is a Phase 5
   decision.

## Open Questions

None — all initial open questions resolved into the Decisions section above.
Four verifications remain as in-implementation tasks in IMPL-0002 rather
than design blockers:

- **[IMPL-0002]** Does Garage refuse to remove the last global alias?
- **[IMPL-0002]** Is `AddBucketAlias` idempotent for an alias already on
  the same bucket?
- **[IMPL-0002]** Does `DeleteBucket` empty non-empty buckets, or do we
  need to enumerate-and-delete via the S3 data plane?
- **[IMPL-0002]** What HTTP status code does Garage emit for
  `AddBucketAlias` against an alias owned by another bucket? (Affects
  error-mapping diag clarity, not control flow.)

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 2 entry
- [DESIGN-0001: Phase 1 implementation](0001-phase-1-implementation-provider-scaffold-and-openapi-client.md)
  — patterns this design extends
- [IMPL-0001: Phase 1 implementation](../impl/0001-phase-1-provider-scaffold-openapi-client-smoke-test.md)
  — operational follow-through that's reused as-is here (lifecycle, fixture,
  client wrapper conventions, retry policy)
- [ADR-0002: Use terraform-plugin-framework over SDKv2](../adr/0002-use-terraform-plugin-framework-over-sdkv2.md)
  — schema validators, sensitive masking, ImportState API
- [ADR-0003: OpenAPI-generated client via oapi-codegen](../adr/0003-openapi-generated-client-via-oapi-codegen.md)
  — the wrapper boundary that hides oapi-codegen types from the resource layer
- [ADR-0005: testcontainers-go for acceptance tests](../adr/0005-testcontainers-go-for-acceptance-tests.md)
  — per-test container model
- [Garage admin v2 API: bucket endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [AWS provider `aws_s3_bucket` resource](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/s3_bucket)
  — `force_destroy` semantics modeled on this

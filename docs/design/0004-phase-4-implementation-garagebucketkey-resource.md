---
id: DESIGN-0004
title: "Phase 4 implementation: garage_bucket_key resource"
status: Draft
author: Donald Gifford
created: 2026-05-17
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0004: Phase 4 implementation: garage_bucket_key resource

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
  - [Resource shape](#resource-shape)
  - [Identity and import](#identity-and-import)
  - [Lifecycle mapping](#lifecycle-mapping)
  - [Idempotency posture](#idempotency-posture)
  - [Cross-resource interactions](#cross-resource-interactions)
- [Open Questions](#open-questions)
- [References](#references)
<!--toc:end-->

## Overview

Phase 4 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
introduces `garage_bucket_key`: a Terraform-managed permission edge
between a `garage_bucket` and a `garage_key`, with the
`(owner, read, write)` triple as the managed payload. The resource maps
1:1 to a row in Garage's bucket-key permission table; create / update /
delete translate to `AllowBucketKey` / `DenyBucketKey` admin calls.

## Status

**Sketch.** This doc captures the cross-cutting constraints from
DESIGN-0003 (`garage_key`) so reviewers can spot inconsistencies, but
the full Detailed Design / Data Model / Testing Strategy sections are
deferred until DESIGN-0003 is approved and IMPL-0003 starts. Filled in
as part of finishing IMPL-0003 — see DESIGN-0003 §Migration / Rollout
Plan.

## Goals and Non-Goals

### Goals

- `garage_bucket_key` resource with Create / Read / Update / Delete /
  Import wired against `AllowBucketKey` and `DenyBucketKey`
- Three independent boolean attributes: `owner`, `read`, `write` —
  verified independent in IMPL-0002 Phase 6 (setting `owner=true` alone
  does not grant data-plane Read/Write; all three are toggled
  independently)
- Drift detection: external permission flips surface in `plan`
- Acceptance tests covering permission state transitions (the 8-cell
  truth table is too much; pick the meaningful subsets)

### Non-Goals

- Per-key `allow_create_bucket` — that's `garage_key.allow_create_bucket`
  (Phase 3), set via `UpdateKey`. Not part of the bucket-key edge
- Inline `permissions` block on `garage_bucket` or `garage_key` — the
  edge is its own resource so the (n × m) relationship doesn't blow up
  one side's schema
- Bulk grant/revoke — not modeled by Garage's API (each call targets
  one (bucket, key) pair)

## Background

After Phase 3 the repo has:

- `internal/client/client.go` with `AllowBucketKey` (already added in
  IMPL-0002 Phase 6 for test grants) and five key methods from Phase 3
- `garage_key` resource shipping `secret_source` modes per ADR-0001
- The Phase 6 finding that `ApiBucketKeyPerm.{Owner, Read, Write}` are
  three independent booleans on the wire (not a hierarchy) —
  `internal/resources/bucket/resource_test.go` has the `grantBucketAccess`
  helper as live proof

The new Garage admin ops Phase 4 needs:

- `AllowBucketKey` — already wired
- `DenyBucketKey` — adds in Phase 4. Same body shape
  (`BucketKeyPermChangeRequest{accessKeyId, bucketId, permissions}`),
  inverts flags
- `GetBucket` and `GetKeyInfo` for drift detection — both already wired

## Detailed Design — sketch

### Resource shape

```hcl
resource "garage_bucket_key" "loki" {
  bucket_id     = garage_bucket.images.id
  access_key_id = garage_key.loki.access_key_id

  owner = false
  read  = true
  write = true
}
```

Schema (sketch):

```go
"id":            schema.StringAttribute{Computed, UseStateForUnknown}
"bucket_id":     schema.StringAttribute{Required, RequiresReplace}
"access_key_id": schema.StringAttribute{Required, RequiresReplace}
"owner":         schema.BoolAttribute{Optional, Computed, Default(false)}
"read":          schema.BoolAttribute{Optional, Computed, Default(false)}
"write":         schema.BoolAttribute{Optional, Computed, Default(false)}
```

`bucket_id` and `access_key_id` are `RequiresReplace` because a change to
either is "different edge entirely" — the framework should destroy +
create rather than try to migrate. Matches AWS provider patterns for
edge resources like `aws_iam_user_policy_attachment`.

### Identity and import

`id` is synthetic: `<bucket_id>/<access_key_id>` (slash-separated,
matches the AWS IAM convention).

Import: `terraform import garage_bucket_key.foo <bucket_id>/<access_key_id>`.
`ImportState` parses on `/`, populates both attrs, then `Read` fetches
the permission row via `GetBucket` (the `KeyInfoBucketResponse.Permissions`
list).

### Lifecycle mapping

| TF lifecycle | Garage admin operation                                          |
|--------------|-----------------------------------------------------------------|
| Create       | `AllowBucketKey({bucketId, accessKeyId, permissions: {true flags}})` then if any flags are false: `DenyBucketKey({...})` — see Open Question 1 |
| Read         | `GetBucket(bucket_id)` → search `Keys[]` for `accessKeyId` → extract `Permissions` |
| Update       | Diff the truth table; `AllowBucketKey` for additions, `DenyBucketKey` for removals |
| Delete       | `DenyBucketKey({bucketId, accessKeyId, permissions: {owner: true, read: true, write: true}})` — strip all perms |

Note: GetBucket-then-search beats the cleaner `GetKeyInfo` because
`GetBucket.Keys[]` is the authoritative bucket-side view. `GetKeyInfo`
also returns per-bucket permissions but its denormalization could
theoretically drift; verifying which one is source-of-truth is an
IMPL-0004 live-probe task.

### Idempotency posture

`AllowBucketKey` was verified idempotent in IMPL-0002 Phase 6 (the
`grantBucketAccess` helper retried with the same flags and saw no
errors). Expect `DenyBucketKey` to behave the same. Retry policy: treat
both as idempotent verbs (eligible for the existing retry-on-5xx
helper).

### Cross-resource interactions

**With `garage_key`:** `garage_key` deliberately omits the `buckets`
attribute (DESIGN-0003 Decision 3) so the two resources don't fight over
the same data. The edge lives here.

**With `garage_bucket`:** `garage_bucket.global_aliases` is unrelated —
that's a name-mapping; this is permissions. No overlap.

**Cascading deletes:** if the user destroys a `garage_bucket` or
`garage_key` that has a referenced `garage_bucket_key`, Terraform's
dependency graph deletes the edge resource first. If the user
out-of-band deletes the bucket or key (or its `id` changes), the next
`Read` on the edge sees `ErrNotFound` from `GetBucket` or the missing
key in `GetBucket.Keys[]` and removes the edge from state — same
pattern as `garage_bucket` does for the bucket itself.

## Open Questions

To resolve as part of finishing DESIGN-0004 alongside IMPL-0003:

1. **Single AllowBucketKey vs Allow+Deny pair for Create.** Garage's
   `AllowBucketKey` accepts the full `(owner, read, write)` triple in
   one call; setting any flag to `false` *might* still toggle it off.
   Verify against live Garage: does
   `AllowBucketKey({owner: false, read: true, write: true})` correctly
   land at `(false, true, true)`, or does it union with whatever's
   already set on the edge? If the latter, Create needs a `DenyBucketKey`
   follow-up to clear the false flags. Either way, capture in IMPL-0004
   Phase 2 livecheck.

2. **`GetBucket.Keys[]` vs `GetKeyInfo.Buckets[]` as the read path.**
   Both surface the same edge from opposite sides. Verify they agree on
   non-trivial state (set perms via `GetBucket`-side, read via
   `GetKeyInfo`-side and back). If they disagree, the design needs to
   pick one as source-of-truth and document the drift case.

3. **Synthetic id format.** `<bucket_id>/<access_key_id>` is the working
   proposal. Verify both ids never contain `/` (Garage bucket ids are
   64-char hex; access_key_ids are AWS-style alphanumeric — both safe).
   If we're worried about future Garage versions changing the id
   format, switch to a different separator or to a JSON-encoded id.

4. **Drift signal on key/bucket deletion.** When the referenced bucket
   or key is deleted out-of-band, `Read` returns `ErrNotFound`. The
   edge should remove itself from state (standard framework pattern),
   but verify this doesn't produce a misleading plan ("resource will be
   destroyed" when it's already gone). Compare against how the bucket
   resource handles this.

5. **Permission downgrade race.** If user A updates the edge to
   `(read=true, write=false)` while user B is hitting the bucket as
   that key, are in-flight writes denied? Probably yes (admin API is
   strongly consistent), but worth a livecheck. Not blocking — just a
   doc note.

6. **Acceptance test matrix breadth.** The full 8-cell truth table for
   `(owner, read, write)` is tedious. Pick:
   - read-only → read+write
   - owner alone (Phase 6 of IMPL-0002 showed this *doesn't* grant
     data-plane access — interesting edge case worth a test)
   - read+write → revoke all
   - drift: external flip of one flag

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 4 entry
- [DESIGN-0003: Phase 3 garage_key resource](0003-phase-3-implementation-garagekey-resource.md)
  — the resource this edge references; Decision 3 documents the
  deliberate `buckets` omission
- [DESIGN-0002: Phase 2 garage_bucket resource](0002-phase-2-implementation-garagebucket-resource.md)
  — bucket lifecycle the edge depends on; Phase 6 of IMPL-0002 contains
  the live evidence that owner/read/write are independent flags
- [IMPL-0002: Phase 2 implementation plan](../impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — `AllowBucketKey` wrapper method already exists from Phase 6
- [Garage admin v2 API: bucket-key endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)
- [AWS provider `aws_iam_user_policy_attachment` resource](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/iam_user_policy_attachment)
  — edge-resource patterns (synthetic id, RequiresReplace on tuple fields)

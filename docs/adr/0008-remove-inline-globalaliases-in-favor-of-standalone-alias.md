---
id: ADR-0008
title: "Remove inline global_aliases in favor of standalone alias resources"
status: Accepted
author: Donald Gifford
created: 2026-07-21
---
<!-- markdownlint-disable-file MD025 MD041 -->

# 0008. Remove inline global_aliases in favor of standalone alias resources

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
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

A Garage bucket is identified by an opaque 64-char hex id; an *alias* is a
human-readable name that maps to that id so S3 clients can address the bucket
by name (`s3://images/…`) instead of the hex. Garage models two alias flavors
through the same admin endpoint, discriminated by the request body:

- **global** (`BucketAliasEnum0{bucketId, globalAlias}`) — a cluster-wide name
  any key resolves; the "normal" bucket name
- **local** (`BucketAliasEnum1{accessKeyId, bucketId, localAlias}`) — a name
  scoped to one access key; per-key naming

Phase 2 ([DESIGN-0002](../design/0002-phase-2-implementation-garagebucket-resource.md))
shipped global aliases as an **inline `global_aliases` set** on the
`garage_bucket` resource, and deferred local aliases and any standalone alias
resource to Phase 5. Phase 5 ([DESIGN-0005](../design/0005-phase-5-implementation-garagebucketalias-resource.md))
must add local aliases (the surface currently inaccessible from Terraform) and
decide how global and local aliases are exposed.

The original Phase 5 sketch considered keeping the inline `global_aliases` set
*and* adding standalone alias resource(s) — mirroring the legacy AWS-provider
pattern of an inline attribute on the parent plus a separate edge resource for
the same relationship (e.g. `aws_security_group` inline `ingress` vs
`aws_security_group_rule`; inline IAM policies vs `aws_iam_*_policy_attachment`).
That pattern is well known for a foot-gun: two owners of the same object fight
on every plan, and the AWS docs warn against using both at once.

The conflict is not something the provider can catch:

- **Terraform cannot cross-validate at plan time.** A resource sees only its
  own configuration during planning; a standalone `garage_bucket_global_alias`
  cannot read a `garage_bucket`'s `global_aliases` to detect a duplicate.
- **Garage does not surface the conflict at apply time either.** A same-bucket
  duplicate alias is an idempotent no-op (HTTP 2xx). So the collision stays
  invisible until one side later removes the alias, at which point it manifests
  as permanent drift.

Meanwhile the wider provider ecosystem has moved *away* from inline-plus-
standalone toward a single canonical resource per relationship (e.g. AWS
deprecated inline security-group rules in favor of
`aws_vpc_security_group_ingress_rule`) precisely to eliminate this class of
conflict.

## Decision

**Remove the inline `global_aliases` attribute from `garage_bucket`, and manage
all aliases through two standalone resources:**

- `garage_bucket_global_alias` — `bucket_id` + `alias`
- `garage_bucket_local_alias` — `bucket_id` + `access_key_id` + `alias`

Every alias therefore has exactly one owning resource. The two resources
occupy different namespaces (cluster-wide vs per-key) and cannot collide with
each other; with the inline attribute gone, there is no inline-vs-standalone
collision either. The conflict is eliminated *structurally* rather than
detected — which is the only workable option, since detection is not possible
in Terraform.

Rationale, in order of weight:

1. **Single source of truth.** One owner per alias removes the drift foot-gun
   entirely. This is the property the inline-plus-standalone approach cannot
   provide.
2. **Matches the modern canonical provider pattern.** One resource per
   relationship, no inline duplication — the direction the ecosystem has
   converged on.
3. **Two explicit resources over one unified resource.** A single
   `garage_bucket_alias` with an *optional* `access_key_id` was considered and
   rejected: two resources let each schema be exactly what it needs (global has
   no key; local requires one), and give unambiguous import ids — a global id
   is `<bucket_id>/<alias>` (split on the first `/`) and a local id is
   `<bucket_id>/<access_key_id>/<alias>` (split on the first two), so aliases
   may contain `/` with no special handling.

Because inline aliases are removed, `garage_bucket_global_alias` is **required**
to give a bucket a resolvable name; it is not an optional convenience.

This is a **breaking change** to a resource shipped in Phase 2. It is
acceptable because the provider is pre-1.0 (semver permits breaking changes on
0.x), and it ships with a migration path: for each entry previously in a
bucket's `global_aliases`, add a `garage_bucket_global_alias` resource and
`terraform import <bucket_id>/<alias>` it (the alias already exists in Garage,
so the import is a no-op read), then drop `global_aliases` from the bucket
config. The two new resources land in the same change as the removal so no
release exists in which a bucket cannot be named.

One Garage behavior shapes the delete path: Garage **refuses to remove a
bucket's last alias** (HTTP 400, "…please delete it instead of just
unaliasing" — [IMPL-0002](../impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
§Decisions #2). With aliases as separate resources, `terraform destroy` tears
down the alias before the bucket, which would deadlock. `garage_bucket_global_alias`
`Delete` therefore handles the last-alias 400 leniently: on that error it reads
the bucket; if the bucket is gone the alias is already gone (success), and if
the bucket still exists it warns and removes the alias from state, relying on
the bucket's own `DeleteBucket` to clean the alias.

## Consequences

### Positive

- **No duplicate-alias drift.** Single ownership makes the conflict impossible
  instead of merely discouraged.
- **Local aliases become manageable.** `garage_bucket_local_alias` exposes
  per-key naming, previously inaccessible from Terraform.
- **Unambiguous, `/`-tolerant import ids.** The fixed leading-segment count per
  resource type makes id parsing deterministic without restricting alias names.
- **Aligned with current provider conventions**, easing review and reuse.

### Negative

- **Breaking change for existing users** of the Phase 2 `global_aliases`
  attribute; they must run the import-based migration.
- **More resource blocks for the common case.** Naming a bucket now requires a
  separate `garage_bucket_global_alias` block rather than a one-line inline set.
- **A residual last-alias edge case.** Removing a bucket's *sole* global alias
  while keeping the bucket leaves the alias in Garage (the lenient `Delete`
  removes it from state and warns); this is rare and does not affect
  `terraform destroy` of a bucket together with its aliases.

### Neutral

- The client wrapper methods `AddBucketAlias` / `RemoveBucketAlias` are
  retained — the new resources consume them; only the bucket resource's use of
  them is removed.
- The version bump for the release carrying this change reflects the break
  (pre-1.0 convention).

## Alternatives Considered

- **Inline global + standalone global + standalone local** (the legacy
  AWS-style hybrid). Rejected: inline-global and standalone-global own the same
  cluster-wide namespace and re-introduce the exact duplicate-alias conflict,
  which Terraform cannot validate at plan time.
- **Keep inline global, add standalone local only** (pragmatic, non-breaking).
  Conflict-free and keeps Phase 2 intact, but leaves two different mechanisms
  for aliases and no way to manage a global alias independently (the cross-team
  case). Rejected in favor of a uniform single-source-of-truth model.
- **One unified `garage_bucket_alias` with an optional `access_key_id`.**
  Also single-source-of-truth, but the flavor is implicit, the schema carries
  an optional field that changes behavior, and the 2-vs-3-segment id needs the
  alias to exclude `/`. Rejected for the clearer two-resource split.

## References

- [DESIGN-0005: Phase 5 — standalone bucket alias resources (global + local)](../design/0005-phase-5-implementation-garagebucketalias-resource.md)
- [IMPL-0005: Phase 5 implementation plan](../impl/0005-phase-5-garagebucketalias-resource-local-aliases-crud.md)
  — §Phase 6 executes this removal
- [DESIGN-0002: Phase 2 — garage_bucket resource](../design/0002-phase-2-implementation-garagebucket-resource.md)
  — introduced the inline `global_aliases` attribute this ADR removes
- [IMPL-0002: Phase 2 implementation plan](../impl/0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md)
  — §Decisions #2 captures the last-alias-removal refusal
- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 5 entry
- [HashiCorp: migrating to `aws_vpc_security_group_ingress_rule`](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/vpc_security_group_ingress_rule)
  — precedent for retiring an inline attribute in favor of a standalone resource

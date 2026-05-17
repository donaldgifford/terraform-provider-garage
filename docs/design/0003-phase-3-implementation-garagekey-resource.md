---
id: DESIGN-0003
title: "Phase 3 implementation: garage_key resource"
status: Draft
author: Donald Gifford
created: 2026-05-17
---
<!-- markdownlint-disable-file MD025 MD041 -->

# DESIGN 0003: Phase 3 implementation: garage_key resource

**Status:** Draft
**Author:** Donald Gifford
**Date:** 2026-05-17

<!--toc:start-->
- [Overview](#overview)
- [Goals and Non-Goals](#goals-and-non-goals)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Background](#background)
- [Detailed Design](#detailed-design)
  - [Resource lifecycle mapping](#resource-lifecycle-mapping)
  - [Schema](#schema)
  - [The two secret_source modes](#the-two-secret_source-modes)
  - [Cross-attribute validators](#cross-attribute-validators)
  - [ValidateConfig warning](#validateconfig-warning)
  - [Create flow](#create-flow)
  - [Read flow](#read-flow)
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

Phase 3 of [RFC-0001](../rfc/0001-garage-terraformopentofu-provider.md)
introduces `garage_key`: a Terraform-managed S3 access key in Garage with
two explicit credential-handling modes per [ADR-0001](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md).
This is the load-bearing resource of the provider — it's the differentiator
from the four existing community providers, none of which offer an opt-out
of the secret-in-state default. Phase 4 (`garage_bucket_key`) and Phase 5
(`garage_bucket_alias`) both reference a `garage_key.id`, so the schema
and import semantics of this resource constrain both follow-on phases.

## Goals and Non-Goals

### Goals

- `garage_key` resource with Create / Read / Update / Delete / Import wired
  against the Garage admin v2 API
- Both `secret_source` modes per ADR-0001:
  - `"garage"` — admin API generates the secret; provider stores it as
    `Computed + Sensitive` in state
  - `"external"` — user supplies the secret via the write-only
    `secret_access_key_wo` attribute; provider never persists it
- Cross-attribute validators enforcing mode-specific schema (per ADR-0001)
- `ValidateConfig`-level warning diagnostic when `secret_source = "garage"`
  is used, surfacing the in-state credential trade-off without blocking
- Rotation of the external secret via `secret_access_key_wo_version` bump
  (mode change or version bump triggers `RequiresReplace`)
- Per-key `allow_create_bucket` permission (Garage's `KeyPerm.CreateBucket`,
  managed via `UpdateKey.Allow` / `UpdateKey.Deny`)
- Acceptance tests for both modes, including the write-only path with
  `ephemeral "random_password"` (per RFC-0001 Phase 3)
- Generated docs (`docs/resources/key.md`) via `tfplugindocs`

### Non-Goals

- Per-bucket permission edges (`garage_bucket_key`) — Phase 4 of RFC-0001
  owns the `(key_id, bucket_id, owner/read/write)` triple as a separate
  resource. `garage_key` does not expose a `buckets` attribute even though
  `GetKeyInfoResponse.Buckets` returns one — that's Phase 4 territory and
  would conflict on `terraform plan` if both resources tried to manage it
- Local (per-key) bucket aliases — Phase 5 of RFC-0001
  (`garage_bucket_alias`)
- Key expiration (`expiration`, `never_expires` on `UpdateKey`) — deferred
  to a later phase. Garage's default is "never expires" and v0.1 mirrors
  that default. Adding the attribute later is non-breaking
- AdminToken-style API tokens — RFC-0001 §Phase 8 explicitly out of scope
- Server-side rotation of `secret_source = "garage"` keys — `UpdateKey`
  does not support in-place secret regeneration; the only path is replace
  (destroy + create). Documented but not given a dedicated mechanism

## Background

After Phase 2 the repo has:

- `internal/client/client.go` with seven admin operations
  (`CreateBucket`, `GetBucket`, `UpdateBucket`, `DeleteBucket`,
  `AddBucketAlias`, `RemoveBucketAlias`, `AllowBucketKey`) and the wrapper
  conventions: bearer auth, retry-on-5xx for idempotent verbs, typed
  error sentinels (`ErrNotFound`, `APIError`), `tflog` tracing,
  per-method `op` constants for error wrapping
- `internal/provider/provider.go` with the `ProviderData` struct bundling
  the admin client + the three optional S3 fields; plumbed to both
  `ResourceData` and `DataSourceData`
- `internal/resources/bucket/` shipping the `garage_bucket` resource
  (CRUD + ImportState + force_destroy + drift detection + parallel safety)
- `internal/acctest/` with `Start(t)` returning admin endpoint, token,
  and the three S3 fields — Phase 3 reuses it as-is (no fixture changes
  needed since keys don't touch the S3 data plane)

The Garage admin v2 API surface relevant to Phase 3:

| Operation       | Path                                | Method | Notes                                          |
|-----------------|-------------------------------------|--------|------------------------------------------------|
| `CreateKey`     | `/v2/CreateKey`                     | POST   | Garage generates `accessKeyId` and `secretAccessKey`. Body = `UpdateKeyRequestBody` |
| `ImportKey`     | `/v2/ImportKey`                     | POST   | Imports a key by id+secret. Body = `ImportKeyRequest{accessKeyId, secretAccessKey, name}` |
| `GetKeyInfo`    | `/v2/GetKeyInfo?id=<id>`            | GET    | Returns `GetKeyInfoResponse`. `SecretAccessKey` field omitted unless `?showSecretKey=true` is set — and even then, only at creation |
| `UpdateKey`     | `/v2/UpdateKey?id=<id>`             | POST   | Body = `UpdateKeyRequestBody{allow, deny, expiration, name, neverExpires}`. Returns the refreshed info |
| `DeleteKey`     | `/v2/DeleteKey?id=<id>`             | POST   | RPC-style POST (same quirk as DeleteBucket — see IMPL-0002) |
| `ListKeys`      | `/v2/ListKeys`                      | GET    | Returns `[]ListKeysResponseItem{id, name}` — not needed for v0.1 but useful for future "import by name" |

Critical schema callouts:

- `GetKeyInfoResponse.SecretAccessKey *string` — only populated at create time.
  Subsequent reads return `nil`. The resource must treat the secret as
  write-once-readable.
- `GetKeyInfoResponse.Buckets []KeyInfoBucketResponse` — informational; the
  edges themselves are managed by `garage_bucket_key` in Phase 4. This
  resource ignores the field on Read.
- `KeyPerm.CreateBucket *bool` — the only per-key permission Garage models.
  Set via `UpdateKey.Allow.CreateBucket = &true` or
  `UpdateKey.Deny.CreateBucket = &true` (the two are independent toggles;
  Allow grants, Deny revokes).
- `ImportKeyRequest` requires **both** `AccessKeyId` and `SecretAccessKey`.
  The OpenAPI spec does not expose a "create with user-supplied secret"
  shape — see Decision 1 below for how external mode reconciles this with
  ADR-0001's flow description.

## Detailed Design

### Resource lifecycle mapping

| TF lifecycle | Garage admin operations                                                                 |
|--------------|-----------------------------------------------------------------------------------------|
| Create       | `CreateKey` (garage mode) OR `ImportKey` (external mode); then `UpdateKey` if `allow_create_bucket = true` |
| Read         | `GetKeyInfo?id=<access_key_id>`                                                         |
| Update       | `UpdateKey` for `name` and `allow_create_bucket`                                        |
| Delete       | `DeleteKey?id=<access_key_id>`                                                          |
| Import       | Bare `access_key_id` → `GetKeyInfo` to populate state                                   |

Mode changes and write-only secret-version bumps trigger `RequiresReplace`
(Plan modifier on `secret_source` + the framework's built-in `_wo_version`
diff trigger).

### Schema

Pinned by [ADR-0001](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md);
reproduced here with the v0.1 additions:

```go
"id": schema.StringAttribute{
    Computed:    true,
    Description: "The access_key_id, used as the Terraform resource ID.",
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(),
    },
},
"access_key_id": schema.StringAttribute{
    Computed:    true,
    Description: "S3 access key identifier assigned by Garage on create.",
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(),
    },
},
"name": schema.StringAttribute{
    Required:    true,
    Description: "Human-readable name. Garage allows duplicates; uniqueness is the user's responsibility.",
},
"secret_source": schema.StringAttribute{
    Required:    true,
    Description: "\"garage\" auto-generates the secret (persisted in state). \"external\" requires the secret via the write-only secret_access_key_wo attribute (never in state). No default — must be explicit.",
    Validators: []validator.String{
        stringvalidator.OneOf("garage", "external"),
    },
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.RequiresReplace(),
    },
},
"secret_access_key": schema.StringAttribute{
    Computed:    true,
    Sensitive:   true,
    Description: "The S3 secret access key. Populated only when secret_source = \"garage\". Null when secret_source = \"external\".",
},
"secret_access_key_wo": schema.StringAttribute{
    Optional:    true,
    WriteOnly:   true,
    Sensitive:   true,
    Description: "Externally-supplied S3 secret access key. Required when secret_source = \"external\".",
},
"secret_access_key_wo_version": schema.Int64Attribute{
    Optional:    true,
    Description: "Version counter for the write-only secret. Increment to trigger rotation (forces replace).",
},
"allow_create_bucket": schema.BoolAttribute{
    Optional:    true,
    Computed:    true,
    Description: "Whether this key may call CreateBucket. Mirrors Garage's KeyPerm.CreateBucket flag.",
    Default:     booldefault.StaticBool(false),
},
"created": schema.StringAttribute{
    Computed:    true,
    Description: "RFC 3339 timestamp of key creation.",
},
"expired": schema.BoolAttribute{
    Computed:    true,
    Description: "Whether the key has expired. v0.1 does not manage expiration; this surfaces drift if expiration is set out-of-band.",
},
```

Notes:

- `id` and `access_key_id` carry the same value. `id` exists because the
  framework requires a stringly-typed `id` for `ImportState` /
  `resource.UniqueId()`. We keep them as two distinct schema attrs so
  HCL references like `garage_key.foo.access_key_id` read more
  meaningfully than `garage_key.foo.id` in user code.
- `allow_create_bucket` is `Optional + Computed` (not just `Optional`)
  so the framework accepts the `Default: false` and so admin-side flips
  surface as drift rather than as a re-plan.

### The two secret_source modes

**Mode 1: `secret_source = "garage"`**

```hcl
resource "garage_key" "loki" {
  name          = "loki"
  secret_source = "garage"
}
```

Create flow:

1. `CreateKey({name, allow})` → Garage returns
   `{accessKeyId, secretAccessKey, ...}`
2. Provider stores `access_key_id`, `secret_access_key` in state
   (Sensitive)
3. `ValidateConfig` emits a persistent warning diagnostic the first time
   the user `plan`s this resource

**Mode 2: `secret_source = "external"`**

```hcl
ephemeral "random_password" "loki_secret" {
  length = 40
}

resource "garage_key" "loki" {
  name                         = "loki"
  secret_source                = "external"
  secret_access_key_wo         = ephemeral.random_password.loki_secret.result
  secret_access_key_wo_version = 1
}
```

Create flow (see Decision 1 for the resolution of the ImportKey signature
discrepancy):

1. `CreateKey({name, allow})` → Garage returns
   `{accessKeyId, secretAccessKey: <discarded>, ...}` to get a stable
   `accessKeyId`
2. `ImportKey({accessKeyId: <from step 1>, secretAccessKey: <wo>, name})`
   → Garage overwrites the auto-generated secret with the user-supplied
   one. Provider treats `ErrAlreadyExists` (if returned) as success
3. Provider stores `access_key_id` in state. `secret_access_key`
   remains null

No warning diag in external mode — the secret never enters state.

### Cross-attribute validators

Implemented via `resource.ConfigValidators` returning a slice of
`resourcevalidator` instances:

- `resourcevalidator.RequiredTogether(secret_access_key_wo, secret_access_key_wo_version)`
- A custom validator: `secret_source = "external"` ↔ `secret_access_key_wo`
  is set; `secret_source = "garage"` ↔ `secret_access_key_wo` is null

Validation fires at `plan` time, surfacing errors before `apply` runs.
Mirror the bucket resource's pattern: validators in `resource.go`'s
`ConfigValidators` method, error messages name both attributes for
clarity.

### ValidateConfig warning

```go
func (r *Resource) ValidateConfig(
    ctx context.Context,
    req resource.ValidateConfigRequest,
    resp *resource.ValidateConfigResponse,
) {
    var cfg Model
    resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
    if resp.Diagnostics.HasError() {
        return
    }
    if cfg.SecretSource.ValueString() == "garage" {
        resp.Diagnostics.AddAttributeWarning(
            path.Root("secret_source"),
            "Secret will be stored in Terraform state",
            "secret_source = \"garage\" causes Garage to generate the access secret, which is then "+
                "persisted to Terraform state as a Sensitive value. Ensure your state backend is "+
                "encrypted and access-controlled. Use secret_source = \"external\" with the write-only "+
                "secret_access_key_wo attribute (Terraform >= 1.11 / OpenTofu >= 1.11) to keep the "+
                "secret out of state entirely.",
        )
    }
}
```

This is `ValidateConfig` (not `ValidateResource` or a validator on the
attribute itself) because the warning is contextual — it depends on the
*value* of `secret_source`, which a per-attribute validator can't reach
without complicating the schema.

### Create flow

```
parse plan
├── if secret_source = "garage":
│   ├── CreateKey({name, allow: {create_bucket}})
│   ├── store access_key_id, secret_access_key (sensitive) in state
│   └── if allow_create_bucket = true && CreateKey doesn't accept allow inline:
│       └── UpdateKey({allow: {create_bucket: true}})  // see Decision 2
└── if secret_source = "external":
    ├── CreateKey({name})  → gets accessKeyId, discards secret
    ├── ImportKey({accessKeyId, secretAccessKey: <wo>, name})  → overwrites
    ├── store access_key_id in state (secret stays null)
    └── if allow_create_bucket = true: UpdateKey(...)
```

Rollback: if step 2 of external mode (ImportKey) fails after step 1
(CreateKey) succeeded, the resource is in an indeterminate state — the
key exists in Garage with a Garage-generated secret. We call `DeleteKey`
on the partially-created key id and log a `tflog.Warn`, mirroring the
bucket Create rollback pattern from IMPL-0002 Phase 4.

### Read flow

`GetKeyInfo(id)` → populate `name`, `access_key_id`, `created`, `expired`,
`allow_create_bucket` (from `Permissions.CreateBucket`). `secret_access_key`
is **preserved from prior state** (`GetKeyInfo` doesn't return it after
create), or null if the prior state had it null (external mode or
post-import).

Drift surfaces normally for `name`, `allow_create_bucket`, `expired`.
Drift on `secret_access_key` is impossible to detect server-side (Garage
won't tell us the current value); the only signal is failed S3 calls,
which is out of band.

### Update flow

Updates land via `UpdateKey({name, allow, deny})`:

- `name` change → `UpdateKey({name: <new>})`
- `allow_create_bucket` flip from false→true → `UpdateKey({allow: {createBucket: true}})`
- `allow_create_bucket` flip from true→false → `UpdateKey({deny: {createBucket: true}})`

Updates to `secret_access_key_wo_version` trigger `RequiresReplace` (the
framework handles this automatically given `WriteOnly: true`'s version
semantics). Same for `secret_source` (explicit `RequiresReplace` plan
modifier).

### Delete flow

`DeleteKey(id)` — single admin call. No S3-side cleanup (keys don't own
data; revoking a key just denies future S3 requests authenticated with
it).

No `force_destroy` equivalent: there's no analog to "bucket has objects"
that should block key deletion. If the key is referenced by a
`garage_bucket_key` permission edge, the AllowBucketKey row goes orphan
on the Garage side; Phase 4 must surface this via Read drift on the
edge resource. Documented in DESIGN-0004.

### Import

`terraform import garage_key.foo <access_key_id>`:

1. `ImportStatePassthroughID(path.Root("id"), req, resp)` to seed `id`
2. Framework calls `Read`, which calls `GetKeyInfo` → populates the rest
3. `secret_access_key` is null in state (unrecoverable)
4. `secret_access_key_wo` is null in config

The next `plan` after import necessarily produces a diff: either the
user's HCL declares `secret_source = "garage"` (in which case the secret
is missing from state and the resource needs replacement to regenerate),
or `secret_source = "external"` (in which case the user provides
`secret_access_key_wo` and `_wo_version`, triggering replace per the
framework's write-only semantics). The post-import replace is by design,
not a bug — the imported state can't carry credentials, so the next
apply has to produce them.

We document this loudly in the resource's `MarkdownDescription`.

### Error handling and idempotency

| Operation     | Failure mode                          | Wrapper response                                       |
|---------------|---------------------------------------|--------------------------------------------------------|
| `CreateKey`   | name uniqueness (Garage allows dupes) | n/a — no conflict at this layer                        |
| `CreateKey`   | 5xx                                   | Retry per idempotent-verb policy (verify in livecheck — POST CreateKey may have side effects if it returns 500 *after* committing) |
| `ImportKey`   | `accessKeyId` already exists with different secret | Map to `APIError{Code: 400}` and surface to diag; livecheck confirms message shape |
| `ImportKey`   | `accessKeyId` already exists with the same secret  | Treat as idempotent success (need livecheck verification — see Verification A below) |
| `GetKeyInfo`  | 404                                   | `ErrNotFound` → resource removed from state            |
| `UpdateKey`   | 404                                   | `ErrNotFound` → resource removed from state            |
| `DeleteKey`   | 404                                   | `ErrNotFound` → treated as success (already gone)      |

Live verifications (build-tagged probes in `internal/client/livecheck_test.go`,
following the IMPL-0002 Phase 2 pattern):

- **Verification A — `ImportKey` idempotency on identical (id, secret).**
  Expected: 2xx no-op, mirroring `AddBucketAlias`. Needed so the
  external-mode rollback path (CreateKey OK, ImportKey 5xx retry) can
  retry safely.
- **Verification B — `ImportKey` with different secret for an existing
  id.** Expected: HTTP 400; capture the exact message for the resource's
  diag. If Garage instead overwrites silently, that's a significant API
  surprise that changes our model.
- **Verification C — `CreateKey` 5xx replay.** If POST CreateKey returns
  5xx after Garage has internally committed the key, a retry would
  produce a second key with the same name (no uniqueness constraint).
  Expected: probably we can't distinguish this case from a true 5xx; the
  retry policy should treat CreateKey as **non-idempotent** and not
  retry, matching how the bucket wrapper treats CreateBucket. Verify and
  document.
- **Verification D — `DeleteKey` HTTP method.** Likely POST per the
  IMPL-0002 Phase 1 finding that `DeleteBucket` is RPC-style POST. The
  client wrapper must use whatever method the spec actually emits.

### Client wrapper extensions

Five new methods on `*client.Client`, all following the IMPL-0002
Phase 1 patterns (`op` constant, `fmt.Errorf("%s: ...", op)`,
`tflog.Trace` on entry + exit, status-code switch + `APIError` for
unknown 4xx/5xx, retry policy via existing idempotent-verb helper):

```go
func (c *Client) CreateKey(ctx context.Context, req openapi.CreateKeyRequest) (*openapi.GetKeyInfoResponse, error)
func (c *Client) ImportKey(ctx context.Context, req openapi.ImportKeyRequest) (*openapi.GetKeyInfoResponse, error)
func (c *Client) GetKeyInfo(ctx context.Context, id string) (*openapi.GetKeyInfoResponse, error)
func (c *Client) UpdateKey(ctx context.Context, id string, req openapi.UpdateKeyRequestBody) (*openapi.GetKeyInfoResponse, error)
func (c *Client) DeleteKey(ctx context.Context, id string) error
```

`CreateKey` is **not** idempotent (per Verification C above) — the retry
policy lookup table grows a `CreateKey: false` entry alongside the
existing `CreateBucket: false`.

`ListKeys` is deliberately omitted from v0.1; revisit when we need it
(e.g. for an "import by name" UX or a `garage_keys` data source).

### Package layout

```
internal/
  client/
    client.go              # extends with the 5 key methods above
    client_test.go         # unit tests for the new methods
    livecheck_test.go      # extends with the 4 verifications above
  resources/
    bucket/                # unchanged from Phase 2
    key/
      resource.go          # `Resource` struct, schema, lifecycle
      model.go             # tfsdk struct + helpers
      validators.go        # cross-attribute config validators
      resource_test.go     # acceptance tests (TF_ACC gated)
```

Package name `key` (single word, ST1003-clean — same pattern as `bucket`
and `clusterinfo`).

`validators.go` is split out from `resource.go` to keep the cross-attr
validator definitions readable; they're verbose enough that inlining
hurts the lifecycle methods' scanability.

## API / Interface Changes

New Terraform surface:

```hcl
# Mode 1: garage-generated
resource "garage_key" "loki" {
  name                = "loki"
  secret_source       = "garage"
  allow_create_bucket = false
}

# Mode 2: externally-supplied via ephemeral value
ephemeral "random_password" "loki_secret" {
  length = 40
}

resource "garage_key" "loki" {
  name                         = "loki"
  secret_source                = "external"
  secret_access_key_wo         = ephemeral.random_password.loki_secret.result
  secret_access_key_wo_version = 1
  allow_create_bucket          = true
}
```

Computed-only outputs:

```hcl
output "loki_access_key_id" {
  value = garage_key.loki.access_key_id
}

output "loki_secret" {
  # Only meaningful when secret_source = "garage"
  value     = garage_key.loki.secret_access_key
  sensitive = true
}
```

Provider schema unchanged from Phase 2. The S3 attrs added in Phase 6 of
IMPL-0002 are unused by `garage_key` (keys don't touch the S3 data
plane).

## Data Model

```go
type Model struct {
    ID                       types.String `tfsdk:"id"`
    AccessKeyID              types.String `tfsdk:"access_key_id"`
    Name                     types.String `tfsdk:"name"`
    SecretSource             types.String `tfsdk:"secret_source"`
    SecretAccessKey          types.String `tfsdk:"secret_access_key"`
    SecretAccessKeyWO        types.String `tfsdk:"secret_access_key_wo"`
    SecretAccessKeyWOVersion types.Int64  `tfsdk:"secret_access_key_wo_version"`
    AllowCreateBucket        types.Bool   `tfsdk:"allow_create_bucket"`
    Created                  types.String `tfsdk:"created"`
    Expired                  types.Bool   `tfsdk:"expired"`
}
```

Helpers in `model.go`:

- `applyKeyInfoToModel(*openapi.GetKeyInfoResponse, *Model) diag.Diagnostics`
  — populates all fields except `SecretAccessKeyWO` (write-only, never
  read back) and `SecretAccessKey` (preserved from prior state)
- `permFromModel(*Model) (allow, deny *openapi.KeyPerm)` — derives the
  `UpdateKey` allow/deny pair from the desired `AllowCreateBucket` state
  vs the current Garage state

## Testing Strategy

| Layer       | What it covers                                                  | Location                            |
|-------------|-----------------------------------------------------------------|-------------------------------------|
| Unit        | Wrapper methods: happy paths, error mapping, retry policy       | `internal/client/client_test.go`    |
| Unit        | Schema validation, cross-attribute validators, ValidateConfig   | `internal/resources/key/*_test.go`  |
| Live probe  | The four verifications above                                    | `internal/client/livecheck_test.go` |
| Acceptance  | Garage mode: create / read / update / delete                    | `internal/resources/key/resource_test.go` |
| Acceptance  | External mode: create / rotate / delete (with `ephemeral random_password`) | same                     |
| Acceptance  | Mode change: garage → external triggers replace                 | same                                |
| Acceptance  | `allow_create_bucket` flip via UpdateKey                        | same                                |
| Acceptance  | Rename via UpdateKey                                            | same                                |
| Acceptance  | Drift: admin-side rename surfaces in plan                       | same                                |
| Acceptance  | Import by access_key_id                                         | same                                |
| Acceptance  | `ValidateConfig` warning diag present on garage mode            | same                                |
| Acceptance  | Parallel safety: `count = 3` with distinct names                | same                                |

Acceptance tests run `t.Parallel()` from the start. Each test gets its
own Garage container via `acctest.Start(t)` — no fixture changes needed.

The `ephemeral "random_password"` external-mode test uses the
`hashicorp/random` provider's ephemeral resource, which is in core
`terraform-plugin-testing` (no extra deps).

## Migration / Rollout Plan

Net-new resource; no migration. Rollout sequence:

1. Land DESIGN-0003 (this doc) + sketches of DESIGN-0004 / DESIGN-0005 for
   the cross-cutting concerns. Get review on the design
2. Author IMPL-0003 with the phase breakdown — likely mirrors IMPL-0002's
   8-phase shape:
   - Phase 1: client wrapper extensions (5 methods) + unit tests
   - Phase 2: livecheck probes to resolve Verifications A–D
   - Phase 3: resource package scaffold (Metadata, Schema, Configure)
   - Phase 4: Create + Read for `secret_source = "garage"` (the simpler
     mode) + initial acceptance tests
   - Phase 5: Update + Delete; drift tests
   - Phase 6: `secret_source = "external"` mode (CreateKey + ImportKey
     dance) + cross-attribute validators + ValidateConfig warning
   - Phase 7: ImportState
   - Phase 8: examples, generated docs, polish acceptance tests
3. Land via PR on `feat/design-0003-phase-3-key-resource`
4. Bump version with `minor` label (new resource is minor in semver
   pre-1.0)

ADR-0001 status update: flip from `Proposed` to `Accepted` at the end of
Phase 6 of IMPL-0003 (once both modes are demonstrated working in
acceptance tests).

## Decisions

1. **ImportKey signature reconciliation with ADR-0001.** ADR-0001's
   "external" flow says "Provider calls `CreateKey` with the
   user-supplied `secret_access_key_wo` value," but the OpenAPI spec
   shows `CreateKey` body = `UpdateKeyRequestBody` with no secret field —
   only `ImportKey` accepts a user-supplied secret, and it requires
   *both* `accessKeyId` and `secretAccessKey`. Resolution: external mode
   uses a two-step Create — `CreateKey` to mint a stable `accessKeyId`
   (Garage's generated secret is immediately discarded), then
   `ImportKey` to overwrite the secret with the user's value. The user
   never supplies the access key id; Garage assigns it. This needs an
   ADR-0001 status update at impl time to capture the actual flow. The
   external-mode security property is preserved: the user-supplied
   secret is the only secret that ever ends up active, and it's never
   in state.

2. **`allow_create_bucket` set via Create body vs follow-up UpdateKey.**
   `CreateKey`'s body (`UpdateKeyRequestBody`) does accept `Allow`,
   so we can set it inline in Create. Uniform code path beats saving a
   round-trip — IMPL-0002 made the same call for bucket aliases. If
   benchmarks ever show the extra hop is meaningful, revisit. For v0.1
   we keep Create simple and call `UpdateKey` post-Create only on the
   external-mode CreateKey+ImportKey path (where the second call would
   need to fix permissions anyway).

3. **Computed `buckets` attribute deliberately omitted.** Garage's
   `GetKeyInfoResponse.Buckets` returns the buckets a key has access to,
   but exposing it on `garage_key` would conflict with `garage_bucket_key`
   in Phase 4 (both resources claiming the same edge data). Phase 4 owns
   the edge; this resource ignores `Buckets` on Read. Documented in the
   resource's `MarkdownDescription`.

4. **Key expiration deferred.** RFC-0001 Phase 3's task list doesn't
   include expiration; `UpdateKey` supports `expiration` and
   `neverExpires` but we leave them out of v0.1. Garage's default is
   "never expires" so omitting the attribute matches the user's likely
   intent. Adding it later is additive (Optional attr, no schema break).
   `expired` *is* exposed as Computed so out-of-band expiration is
   visible as drift.

5. **No `force_destroy` equivalent.** Keys don't own data — deleting one
   is a permissions revocation, not a destructive cleanup. The only
   concern is dangling `garage_bucket_key` rows (Phase 4's resource),
   which the Phase 4 design handles via Read-time drift detection.
   DESIGN-0004 sketch flags this for finalization.

6. **`name` not used as the resource ID.** Garage allows duplicate names.
   The canonical id is the `access_key_id`. `name` is a settable
   attribute, not part of the identity tuple. Matches the bucket model
   (id is canonical, aliases are mutable).

7. **`secret_source` is required, no default.** ADR-0001 already pins
   this — quoting: "The user must make an explicit choice or the
   configuration fails validation." Documented in the schema description
   plus enforced by the `Required` + `OneOf` validators.

8. **`RequiresReplace` on mode change.** ADR-0001 prescribes this. The
   underlying Garage key parameters change (e.g. Garage-generated secret
   vs imported secret), and there's no `UpdateKey`-level pathway to
   swap a secret in place. Plan modifier on `secret_source`.

## Open Questions

Four in-implementation verifications (see [Error handling and
idempotency](#error-handling-and-idempotency)) need live confirmation in
IMPL-0003 Phase 2 before the resource lifecycle can be finalized:

- 🔜 **Verification A** — `ImportKey` idempotency on identical (id,
  secret). Determines whether the external-mode rollback path can retry
  ImportKey blindly.
- 🔜 **Verification B** — `ImportKey` with a different secret for an
  existing id. Determines the error shape we surface to users and
  whether `ErrAliasConflict`-style sentinel is warranted.
- 🔜 **Verification C** — `CreateKey` 5xx replay. Confirms our planned
  "treat CreateKey as non-idempotent" policy is correct.
- 🔜 **Verification D** — `DeleteKey` HTTP method (likely POST per the
  DeleteBucket precedent). Trivial to confirm but blocks wrapper
  signing.

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraformopentofu-provider.md)
  — §Phases, Phase 3 entry
- [ADR-0001: garage_key secret handling: explicit secret_source modes](../adr/0001-garagekey-secret-handling-explicit-secretsource-modes.md)
  — the schema and mode semantics this design implements
- [DESIGN-0001: Phase 1 implementation](0001-phase-1-implementation-provider-scaffold-and-openapi-client.md)
  — provider scaffold, client wrapper conventions
- [DESIGN-0002: Phase 2 implementation](0002-phase-2-implementation-garagebucket-resource.md)
  — the patterns this design extends (livecheck probes, rollback
  semantics, `op` constants, `tflog` tracing)
- [DESIGN-0004 (sketch): Phase 4 garage_bucket_key resource](0004-phase-4-implementation-garagebucketkey-resource.md)
  — the permission-edge resource that references `garage_key.id`
- [DESIGN-0005 (sketch): Phase 5 garage_bucket_alias resource](0005-phase-5-implementation-garagebucketalias-resource.md)
  — the alias-edge resource (local aliases reference `garage_key.id`)
- [ADR-0002: Use terraform-plugin-framework over SDKv2](../adr/0002-use-terraform-plugin-framework-over-sdkv2.md)
  — WriteOnly + Computed + Sensitive support, ValidateConfig API
- [Terraform Plugin Framework: Write-only Arguments](https://developer.hashicorp.com/terraform/plugin/framework/resources/write-only-arguments)
- [Terraform 1.11: Ephemeral values in managed resources with write-only arguments](https://www.hashicorp.com/en/blog/terraform-1-11-ephemeral-values-managed-resources-write-only-arguments)
- [Garage admin v2 API: key endpoints](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

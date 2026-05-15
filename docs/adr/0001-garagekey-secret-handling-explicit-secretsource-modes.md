---
id: ADR-0001
title: "garage_key secret handling: explicit secret_source modes"
status: Proposed
author: Donald Gifford
created: 2026-05-11
---

<!-- markdownlint-disable-file MD025 MD041 -->

# 0001. garage_key secret handling: explicit secret_source modes

<!--toc:start-->
- [Status](#status)
- [Context](#context)
- [Decision](#decision)
  - [Schema](#schema)
  - [Behavior](#behavior)
  - [Rotation](#rotation)
  - [Import](#import)
  - [Example usage](#example-usage)
- [Consequences](#consequences)
  - [Positive](#positive)
  - [Negative](#negative)
  - [Neutral](#neutral)
- [Alternatives Considered](#alternatives-considered)
- [References](#references)
<!--toc:end-->

## Status

Proposed

## Context

The `garage_key` resource represents an S3 access key in Garage. Garage's admin
API v2 `CreateKey` endpoint returns the `secret_access_key` **only at creation
time**; `GetKeyInfo` does not include the secret on subsequent reads. This is
the same model AWS IAM uses for access keys.

This creates a tension that any Garage Terraform provider must resolve:

1. The `terraform-plugin-framework` mandates that `Computed` attributes are
   persisted to Terraform state. There is no "computed but ephemeral"
   combination — a value is either stored in state or it's not produced by the
   resource at all.
2. Write-only arguments (`WriteOnly: true`, Terraform 1.11+ / OpenTofu 1.11+)
   are explicitly input-only — the schema validation forbids combining
   `WriteOnly: true` with `Computed: true`.
3. Storing the credential in state means anyone with read access to the state
   backend has plaintext access to it. State encryption (OpenTofu 1.7+)
   mitigates this only when properly configured and keyed.

Our workflows require a path where the access secret can be routed directly into
a secret store (1Password in our case) at creation time without ever being
persisted in Terraform state. We also want to keep the ergonomic "let Garage
generate it" path available for development and trusted-state scenarios.

The four existing community providers (`darkmukke`, `d0ugal`, `jkossis`,
`arsolitt`) all use a single mode: Garage auto-generates, secret is stored in
state as `Computed + Sensitive`. None offer an opt-out.

## Decision

Introduce a required `secret_source` attribute on `garage_key` with two valid
values: `"garage"` and `"external"`. There is no default — the user must make an
explicit choice or the configuration fails validation.

### Schema

```go
"secret_source": schema.StringAttribute{
    Required:    true,
    Description: "Where the access secret comes from. \"garage\" auto-generates via the admin API (stored in state). \"external\" requires the secret via the write-only secret_access_key_wo attribute (never in state).",
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
    Description: "Externally-supplied S3 secret access key. Required when secret_source = \"external\"; must be unset when secret_source = \"garage\".",
},
"secret_access_key_wo_version": schema.Int64Attribute{
    Optional:    true,
    Description: "Version counter for the write-only secret. Increment to trigger rotation.",
},
```

Cross-attribute validators (implemented via `resource.ConfigValidators`)
enforce:

- `secret_source = "external"` requires `secret_access_key_wo` and
  `secret_access_key_wo_version` to be set
- `secret_source = "garage"` requires `secret_access_key_wo` and
  `secret_access_key_wo_version` to be null

### Behavior

**`secret_source = "garage"`:**

1. Provider calls `CreateKey` without supplying a secret
2. Garage auto-generates `access_key_id` and `secret_access_key`
3. Provider stores both in state (`access_key_id` as Computed,
   `secret_access_key` as Computed + Sensitive)
4. Provider emits a persistent warning diagnostic via `ValidateConfig`:

   > "secret_source = \"garage\" will cause the generated secret_access_key to
   > be persisted to Terraform state in plaintext. Ensure your state backend is
   > encrypted and access-controlled. Use secret_source = \"external\" with the
   > write-only secret_access_key_wo attribute to keep the secret out of state
   > entirely."

**`secret_source = "external"`:**

1. Provider calls `CreateKey` with the user-supplied `secret_access_key_wo`
   value
2. Garage stores it; provider does not retain it
3. `secret_access_key` in state remains null
4. No warning is emitted

### Rotation

`secret_access_key_wo_version` is stored in state and serves as the diff trigger
for the write-only secret. To rotate:

1. Update the source ephemeral value (e.g. `ephemeral "random_password"`)
2. Increment `secret_access_key_wo_version`
3. Provider sees the version change; since Garage's `UpdateKey` does not support
   in-place secret rotation, the resource is replaced (destroy + create)

The `RequiresReplace` plan modifier on `secret_source` also ensures changing
modes forces replacement (since the underlying Garage key parameters change).

### Import

`terraform import garage_key.foo <key_id>` retrieves `id`, `access_key_id`, and
`name` from Garage. The secret is unrecoverable, so `secret_access_key` is null
in state and `secret_access_key_wo` is null in config. The user adds the
appropriate mode to config and runs apply, which replaces the resource (since
the secret is by definition new on either path after import).

### Example usage

```hcl
# Mode 1: Garage-generated, ergonomic, secret in state
resource "garage_key" "loki" {
  name          = "loki"
  secret_source = "garage"
}

# Mode 2: externally-supplied, secret never in state
ephemeral "random_password" "loki_secret" {
  length = 40
}

resource "garage_key" "loki" {
  name                         = "loki"
  secret_source                = "external"
  secret_access_key_wo         = ephemeral.random_password.loki_secret.result
  secret_access_key_wo_version = 1
}

resource "onepassword_item" "loki" {
  vault                 = data.onepassword_vault.infra.uuid
  title                 = "Loki Garage Credentials"
  category              = "api_credential"
  username              = garage_key.loki.access_key_id
  credential_wo         = ephemeral.random_password.loki_secret.result
  credential_wo_version = 1
}
```

## Consequences

### Positive

- Users choose ergonomic (`garage`) or secure (`external`) workflows per
  resource
- No silent default that leaks credentials to state
- Compatible with `terraform import` for keys created out-of-band by the
  `garage` CLI
- Aligns with TF 1.11+ / OpenTofu 1.11+ ephemeral value best practices
- Persistent warning keeps the trade-off visible without being a hard block
- Mode is explicit in HCL, making security posture readable in code review

### Negative

- More verbose HCL than a single-mode provider — `secret_source` must always be
  specified
- `external` mode requires Terraform >= 1.11 or OpenTofu >= 1.11 (write-only
  attribute support)
- Two code paths in the resource's `Create` method
- Documentation burden: README and examples must clearly explain both modes
- Cross-attribute validation logic adds schema complexity

### Neutral

- The `garage` mode replicates what existing community providers already do, so
  users migrating from those providers have a familiar path
- The `external` mode is the differentiating capability and the primary reason
  this provider exists rather than adopting an existing one

## Alternatives Considered

**Computed + Sensitive only (the existing providers' approach).** Auto-generate
and always store in state. Rejected: no escape hatch for security-conscious
users; the explicit motivation for building this provider was to support the
no-state-secret workflow.

**Write-only required, no auto-generation.** Strict mode: `secret_access_key_wo`
is `Required`. Rejected: cuts off the dev/test ergonomic path; complicates
`terraform import` (where the secret is irretrievable by definition); doesn't
accommodate trusted-state-backend scenarios where in-state storage is
acceptable.

**Implicit mode based on presence of `secret_access_key_wo`.** No explicit
`secret_source` attribute; mode inferred from whether the user supplies the
write-only attribute. Rejected: users have to read documentation to understand
which path they're on; the security posture isn't visible in the HCL; mistakes
(forgetting to set `_wo`) silently fall into the less-secure mode.

**Provider-side integration with secret stores.** Provider writes the secret
directly to 1Password / Vault / AWS Secrets Manager. Rejected: tight coupling;
infinite scope creep (which stores? which authentication?); breaks
single-responsibility. The ephemeral + write-only pattern already composes
cleanly with existing secret-store providers.

**Generate-and-discard.** Auto-generate via Garage, do not store in state, do
not return to the user. Rejected: the secret is then lost forever and the key is
useless; this is "garage mode minus the value."

## References

- [RFC-0001: Garage Terraform/OpenTofu provider](../rfc/0001-garage-terraform-opentofu-provider.md)
- [Terraform Plugin Framework: Write-only Arguments](https://developer.hashicorp.com/terraform/plugin/framework/resources/write-only-arguments)
- [Terraform 1.11: Ephemeral values in managed resources with write-only arguments](https://www.hashicorp.com/en/blog/terraform-1-11-ephemeral-values-managed-resources-write-only-arguments)
- [Terraform language: Ephemeral values](https://developer.hashicorp.com/terraform/language/manage-sensitive-data/ephemeral)
- [Garage admin API v2 reference](https://garagehq.deuxfleurs.fr/documentation/reference-manual/admin-api/)

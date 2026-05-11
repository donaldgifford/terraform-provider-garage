# Repo Initialization

Step-by-step setup for the `terraform-provider-garage` repository, starting from
the HashiCorp scaffolding template.

## 1. Create the repo from the template

The
[hashicorp/terraform-provider-scaffolding-framework](https://github.com/hashicorp/terraform-provider-scaffolding-framework)
repo is configured as a GitHub template. The cleanest path:

1. Open the scaffolding repo on GitHub
2. Click **Use this template** → **Create a new repository**
3. Owner: `donaldgifford` (or wherever this should live)
4. Name: `terraform-provider-garage` — the `terraform-provider-` prefix is
   required for registry auto-discovery
5. Visibility: Public (required for registry publishing)
6. Click **Create repository from template**

## 2. Clone and rename

```bash
git clone git@github.com:donaldgifford/terraform-provider-garage.git
cd terraform-provider-garage
```

Rename the Go module and the `scaffolding` references throughout the codebase:

```bash
# Rename the Go module
go mod edit -module github.com/donaldgifford/terraform-provider-garage

# Find/replace scaffolding references in source files (GNU sed)
grep -rl scaffolding . --exclude-dir=.git --exclude-dir=vendor \
  | xargs sed -i 's/scaffolding/garage/g'

# On macOS, sed requires an empty-string argument for in-place edits:
# grep -rl scaffolding . --exclude-dir=.git --exclude-dir=vendor \
#   | xargs sed -i '' 's/scaffolding/garage/g'
```

Then walk through these by hand:

- `internal/provider/provider.go` — rename the `ScaffoldingProvider` type →
  `GarageProvider`, the `New` factory function references, and the schema's
  `MarkdownDescription` text
- Example resource and data source files in `internal/provider/` — delete them
  or strip to stubs; they'll be replaced during Phase 1 implementation
- `.goreleaser.yml` — update provider name and GPG key reference (used later in
  ADR-0004)
- `README.md` — replace with the actual provider description

Commit the rename pass as a single commit so the project history is clean from
this point.

## 3. Initialize docz

From the repo root:

```bash
docz init
```

This creates `.docz.yaml` and the standard
`docs/{rfc,adr,design,impl,plan,investigation}/` directory tree.

**Optional:** If you'd rather keep design docs separate from
`tfplugindocs`-generated provider documentation, set a custom directory in
`.docz.yaml`:

```yaml
docs_dir: design-docs
```

Otherwise the default `docs_dir: docs` is fine — see the namespace note in
section 5.

## 4. Drop in the design docs

Copy the three files into their respective locations:

```
docs/rfc/0001-garage-terraform-opentofu-provider.md
docs/adr/0001-garage-key-secret-handling.md
docs/additional.md
```

Regenerate the README index tables:

```bash
docz update
```

Commit:

```bash
git add docs/
git commit -m "docs: initial RFC-0001 and ADR-0001"
```

## 5. Documentation namespace note

The scaffolding repo uses `docs/` at the repo root for `tfplugindocs`-generated
provider documentation, which is what gets published to the registries:

- `docs/index.md` — provider overview
- `docs/resources/<name>.md` — per-resource docs
- `docs/data-sources/<name>.md` — per-data-source docs
- `docs/guides/<name>.md` — usage guides

The docz subdirectories (`docs/rfc/`, `docs/adr/`, etc.) won't collide with
those paths — `tfplugindocs` only touches its own known subdirectories. Both
styles of documentation share the `docs/` root cleanly.

If you'd rather keep them separate, use the `docs_dir` override above and put
docz under `design-docs/` or similar.

## 6. License

The scaffolding repo is licensed **MPL-2.0** (Mozilla Public License 2.0). Keep
it. Specifically:

- **Keep the `LICENSE` file as-is.** Its content is the MPL-2.0 license text —
  it doesn't need attribution to a specific copyright holder
- **Keep the SPDX headers** (`// SPDX-License-Identifier: MPL-2.0`) on any files
  derived from the scaffolding. These are required by the license for derivative
  works
- **Add your own copyright headers** to files you write from scratch — typically
  `// Copyright (c) 2026 Donald Gifford` plus the SPDX line
- **Consider adding a `NOTICE` file** crediting HashiCorp for the scaffolding
  contribution. This is courtesy, not strictly required

### Why MPL-2.0 is the right choice

MPL-2.0 is file-level weak copyleft: modifications to MPL-2.0-licensed files
must remain MPL-2.0, but you can combine them with code under other licenses in
the same project. Mixing licenses across a single repo is more work than it's
worth — keeping the whole project MPL-2.0 is the simplest path.

MPL-2.0 is also the natural fit for this ecosystem:

- OpenTofu itself is MPL-2.0
- Pre-BSL Terraform was MPL-2.0
- Most existing Terraform providers are MPL-2.0
- It explicitly allows commercial use, keeping the door open for all users

Relicensing later would require either rewriting the derivative files cleanly or
negotiating relicensing with HashiCorp for their contribution. Neither is worth
doing.

## Next steps

Once the initial commit is in:

- Begin **Phase 1** from
  [RFC-0001](docs/rfc/0001-garage-terraform-opentofu-provider.md): scaffolding
  cleanup, OpenAPI client generation via `oapi-codegen`, minimal
  `garage_cluster_info` data source as a smoke test, testcontainers-go fixture
- Create the remaining ADRs from [`docs/additional.md`](docs/additional.md) as
  the decisions become concrete (ADR-0002 through ADR-0006)
- Set up the GPG signing key in 1Password before you need it for releases
  (ADR-0004 territory)

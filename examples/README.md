# Examples

Example Terraform configurations consumed by `tfplugindocs` to generate the
provider documentation under `docs/`. Layout convention:

```
examples/
  provider/<name>.tf                            # rendered into docs/index.md
  resources/<resource-name>/resource.tf         # rendered into docs/resources/<resource-name>.md
  data-sources/<data-source-name>/data-source.tf
```

Resource and data-source subdirectories are added per the RFC-0001 phase that
introduces them.

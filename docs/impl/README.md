# Implementation Plans

This directory contains implementation plans with concrete tasks and milestones.

## What are Implementation Plans?

Implementation plans describe **the concrete steps to build a feature or system**.
Each plan includes:

- **Objective**: What is being implemented and which RFC/design it implements
- **Scope**: What is in and out of scope
- **Implementation Steps**: Ordered tasks with checkboxes
- **File Changes**: Key files that will be created or modified
- **Testing Plan**: How the implementation will be validated

## Creating a New Implementation Plan

```bash
docz create impl "Your Implementation Title"
```

## Implementation Status

- **Draft**: Plan is being written
- **In Progress**: Implementation is underway
- **Completed**: Implementation is finished
- **Paused**: Work is temporarily stopped
- **Cancelled**: Plan was abandoned

<!-- BEGIN DOCZ AUTO-GENERATED -->
## All Implementation Plans

| ID | Title | Status | Date | Author | Link |
|----|-------|--------|------|--------|------|
| IMPL-0001 | Phase 1: provider scaffold, OpenAPI client, smoke test | Draft | 2026-05-15 | Donald Gifford | [0001-phase-1-provider-scaffold-openapi-client-smoke-test.md](0001-phase-1-provider-scaffold-openapi-client-smoke-test.md) |
| IMPL-0002 | Phase 2 garage_bucket resource: client wrapper, CRUD, acceptance suite | Accepted | 2026-05-15 | Donald Gifford | [0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md](0002-phase-2-garagebucket-resource-client-wrapper-crud-acceptance.md) |
| IMPL-0003 | Phase 3 garage_key resource: client wrapper, dual secret_source modes, CRUD, acceptance suite | Draft | 2026-07-19 | Donald Gifford | [0003-phase-3-garagekey-resource-client-wrapper-dual-secretsource.md](0003-phase-3-garagekey-resource-client-wrapper-dual-secretsource.md) |
| IMPL-0004 | Phase 4 garage_bucket_key resource: permission-edge CRUD, drift, acceptance suite | Draft | 2026-07-19 | Donald Gifford | [0004-phase-4-garagebucketkey-resource-permission-edge-crud-drift.md](0004-phase-4-garagebucketkey-resource-permission-edge-crud-drift.md) |
| IMPL-0005 | Phase 5 bucket alias resources: standalone global + local, remove inline aliases | Draft | 2026-07-19 | Donald Gifford | [0005-phase-5-garagebucketalias-resource-local-aliases-crud.md](0005-phase-5-garagebucketalias-resource-local-aliases-crud.md) |
<!-- END DOCZ AUTO-GENERATED -->

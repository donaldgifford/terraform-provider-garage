// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package openapi contains the auto-generated HTTP client for Garage's
// admin v2 API.
//
// The package is internal: resource and data-source code never imports it
// directly. The wrapper at internal/client wraps the generated surface and
// hides oapi-codegen-shaped types from the rest of the provider.
//
// # Upgrade procedure
//
// Garage publishes a new admin v2 spec on every release. To bump:
//
//  1. Re-fetch the spec:
//     curl -sSL -o garage-admin-v2.json \
//     https://garagehq.deuxfleurs.fr/api/garage-admin-v2.json
//  2. Read its info.version field; update SpecVersion in version.go
//     to match.
//  3. Regenerate: just generate
//  4. Run just test and just lint to surface any wrapper breakage from
//     the new spec shape.
//  5. Commit garage-admin-v2.json + version.go + generated.go together
//     so reviewers see the upstream diff alongside the generated code.
//
// generated.go is committed (matches the tfplugindocs pattern). CI's
// generate-drift check catches forgotten regenerations and manual edits
// to the generated file.
package openapi

//go:generate go run -modfile=../../../tools/go.mod github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=oapi-codegen.yaml garage-admin-v2.openapi30.json

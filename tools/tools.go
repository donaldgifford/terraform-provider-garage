// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

//go:build generate

// Package tools pins build-only dependencies (tfplugindocs) as indirect
// imports so `go mod tidy` keeps them in tools/go.mod without leaking them
// into the main provider module.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format Terraform code in examples/ so tfplugindocs renders cleanly.
//go:generate terraform fmt -recursive ../examples/

// Generate per-resource provider documentation from examples/ + schema descriptions.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. -provider-name garage

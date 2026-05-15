// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package main is the terraform-provider-garage binary entry point.
//
// The binary speaks the Terraform plugin gRPC protocol v6 and is consumed by
// Terraform and OpenTofu via the registry address declared below.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/donaldgifford/terraform-provider-garage/internal/provider"
)

// version is set at build time via -ldflags "-X main.version=…" by the
// `just build` recipe and goreleaser. The default `dev` keeps `go run` /
// `go install` invocations identifiable in `terraform version` output.
var version = "dev"

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "run the provider in debug mode (attach delve via the printed reattach config)")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/donaldgifford/garage",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}

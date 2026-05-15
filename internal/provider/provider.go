// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package provider implements the Garage Terraform/OpenTofu provider.
//
// The provider talks to Garage's admin v2 HTTP API. Phase 1 (this commit)
// only registers the provider type — schema, Configure, resources, and
// data sources are filled in by later phases of IMPL-0001.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Compile-time interface assertion. Keeps Phase 5+ honest as additional
// framework interfaces (ProviderWithEphemeralResources, etc.) are added.
var _ provider.Provider = (*GarageProvider)(nil)

// GarageProvider is the Terraform Plugin Framework provider for Garage.
type GarageProvider struct {
	// version is injected by main at startup and surfaced through Metadata.
	version string
}

// New returns the constructor expected by providerserver.Serve. The version
// argument flows through from main.version so `terraform version` reports
// the binary's build-time version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &GarageProvider{version: version}
	}
}

// Metadata sets the provider's type name and propagates the version injected
// at build time. The type name becomes the prefix for resource and data
// source names (e.g. garage_cluster_info).
func (p *GarageProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "garage"
	resp.Version = p.version
}

// Schema is intentionally empty in Phase 2. Phase 5 fills in `endpoint` and
// `token` attributes per DESIGN-0001.
func (*GarageProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

// Configure is a no-op in Phase 2. Phase 5 wires it to construct the
// internal client and propagate it as DataSourceData / ResourceData.
func (*GarageProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

// Resources returns the resource constructors the provider exposes. Empty
// until Phase 2 of RFC-0001 introduces garage_bucket et al.
func (*GarageProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

// DataSources returns the data-source constructors the provider exposes.
// Phase 6 of IMPL-0001 adds garage_cluster_info.
func (*GarageProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

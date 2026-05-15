// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package provider implements the Garage Terraform/OpenTofu provider.
//
// The provider talks to Garage's admin v2 HTTP API. Schema attributes
// (`endpoint`, `token`) and Configure() construct an *client.Client that
// every resource and data source receives through ProviderData.
package provider

import (
	"context"
	"os"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/donaldgifford/terraform-provider-garage/internal/client"
	"github.com/donaldgifford/terraform-provider-garage/internal/datasources/clusterinfo"
)

// Environment variables consulted as fallback when the corresponding
// provider attribute is unset in the HCL config. Surfaced as constants so
// they appear in one place — the resolution helper below and the schema
// MarkdownDescription strings reference them.
const (
	envEndpoint = "GARAGE_ENDPOINT"
	envToken    = "GARAGE_TOKEN"
)

// Compile-time interface assertion. Phase 6+ may extend this with
// ProviderWithEphemeralResources etc.; surface the breakage early.
var _ provider.Provider = (*GarageProvider)(nil)

// GarageProvider is the Terraform Plugin Framework provider for Garage.
type GarageProvider struct {
	// version is injected by main at startup and surfaced through Metadata.
	version string
}

// GarageProviderModel mirrors the provider block schema for plan-time
// decoding. Attribute fields use the framework's nullable types so the
// Configure path can distinguish "unset" from "empty string".
type GarageProviderModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

// New returns the constructor expected by providerserver.Serve. The version
// argument flows through from main.version so `terraform version` reports
// the binary's build-time version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &GarageProvider{version: version}
	}
}

// Metadata sets the provider's type name and propagates the version
// injected at build time. The type name becomes the prefix for resource
// and data source names (e.g. garage_cluster_info).
func (p *GarageProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "garage"
	resp.Version = p.version
}

// Schema declares the provider's configuration attributes. Both attrs are
// Optional because each has an environment-variable fallback; Configure()
// fails with a clear diagnostic if neither source produces a value.
func (*GarageProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Configures the Garage admin v2 API client used by all `garage_*` resources and data sources.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Garage admin API endpoint (e.g. `https://garage.example.com:3903`). " +
					"Falls back to the `" + envEndpoint + "` environment variable when unset.",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^https?://`),
						"endpoint must be an http(s) URL",
					),
				},
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Garage admin bearer token. Falls back to the `" + envToken +
					"` environment variable when unset; `" + envToken + "` is the recommended supply mechanism " +
					"so the token does not appear in plan output or state files.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

// Configure constructs an *client.Client from the resolved (config OR env)
// endpoint and token and propagates it as both DataSourceData and
// ResourceData. Validation is cheap and local; first network contact
// happens on the resource/data source Read/Create path.
func (*GarageProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg GarageProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if cfg.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Unknown Garage endpoint",
			"The provider cannot create the Garage admin client because the endpoint is an unknown value. "+
				"Set the value statically or via the "+envEndpoint+" environment variable.",
		)
	}
	if cfg.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Unknown Garage token",
			"The provider cannot create the Garage admin client because the token is an unknown value. "+
				"Set the value statically or via the "+envToken+" environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := resolve(cfg.Endpoint, envEndpoint)
	token := resolve(cfg.Token, envToken)

	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Missing Garage endpoint",
			"The provider requires a Garage admin endpoint. Set the `endpoint` attribute or the "+envEndpoint+" environment variable.",
		)
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing Garage token",
			"The provider requires a Garage admin bearer token. Set the `token` attribute or the "+envToken+" environment variable.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(endpoint, token)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to construct Garage admin client",
			"Failed to build a Garage admin client from the resolved configuration: "+err.Error(),
		)
		return
	}

	resp.DataSourceData = c
	resp.ResourceData = c
}

// Resources returns the resource constructors the provider exposes. Empty
// until Phase 2 of RFC-0001 introduces garage_bucket et al.
func (*GarageProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

// DataSources returns the data-source constructors the provider exposes.
func (*GarageProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		clusterinfo.New,
	}
}

// resolve returns the attribute value if set, otherwise the named
// environment variable. Whitespace is intentionally not trimmed —
// tokens that contain surrounding whitespace are caller errors, not
// the provider's to silently fix.
func resolve(attr types.String, envKey string) string {
	if !attr.IsNull() && !attr.IsUnknown() {
		return attr.ValueString()
	}
	return os.Getenv(envKey)
}

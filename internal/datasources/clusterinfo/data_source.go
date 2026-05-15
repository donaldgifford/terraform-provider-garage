// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package clusterinfo implements the garage_cluster_info data source —
// a singleton, read-only view of the Garage cluster's status (layout
// version + node list). Doubles as the canonical connectivity smoke
// test for users dialling in their provider config.
package clusterinfo

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/donaldgifford/terraform-provider-garage/internal/client"
)

// Compile-time interface assertions.
var (
	_ datasource.DataSource              = (*Source)(nil)
	_ datasource.DataSourceWithConfigure = (*Source)(nil)
)

// Source is the framework implementation of the garage_cluster_info
// data source.
type Source struct {
	client *client.Client
}

// New is the constructor passed to provider.DataSources().
func New() datasource.DataSource {
	return &Source{}
}

// Model mirrors the data source schema for marshaling.
type Model struct {
	ID            types.String `tfsdk:"id"`
	LayoutVersion types.Int64  `tfsdk:"layout_version"`
	Nodes         []NodeModel  `tfsdk:"nodes"`
}

// NodeModel mirrors openapi.NodeResp. Pointer-shaped upstream fields
// (Addr, Hostname, GarageVersion, LastSeenSecsAgo, Role) map to nullable
// framework types so Terraform state can faithfully represent unset
// values returned by a partially-bootstrapped cluster.
type NodeModel struct {
	ID              types.String   `tfsdk:"id"`
	Addr            types.String   `tfsdk:"addr"`
	Hostname        types.String   `tfsdk:"hostname"`
	GarageVersion   types.String   `tfsdk:"garage_version"`
	IsUp            types.Bool     `tfsdk:"is_up"`
	Draining        types.Bool     `tfsdk:"draining"`
	LastSeenSecsAgo types.Int64    `tfsdk:"last_seen_secs_ago"`
	Role            *NodeRoleModel `tfsdk:"role"`
}

// NodeRoleModel mirrors openapi.NodeAssignedRole.
type NodeRoleModel struct {
	Zone     types.String   `tfsdk:"zone"`
	Capacity types.Int64    `tfsdk:"capacity"`
	Tags     []types.String `tfsdk:"tags"`
}

// Metadata sets the data source type name (provider prefix +
// "_cluster_info") used in Terraform configurations.
func (*Source) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_info"
}

// Schema declares the read-only attributes mirroring Garage admin v2's
// GetClusterStatus response.
func (*Source) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read-only view of the Garage cluster's current status — layout version " +
			"and the list of nodes that are either currently connected, part of the active layout, " +
			"or part of a previous layout that is still draining. Useful as a connectivity smoke test " +
			"for a freshly-configured provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic identifier for the data source instance. Set to the " +
					"current `layout_version` so each refresh produces a stable value.",
				Computed: true,
			},
			"layout_version": schema.Int64Attribute{
				MarkdownDescription: "Current version number of the cluster layout.",
				Computed:            true,
			},
			"nodes": schema.ListNestedAttribute{
				MarkdownDescription: "List of nodes known to the cluster.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							MarkdownDescription: "Full-length node identifier.",
							Computed:            true,
						},
						"addr": schema.StringAttribute{
							MarkdownDescription: "Socket address used by other nodes to reach this node for RPC.",
							Computed:            true,
						},
						"hostname": schema.StringAttribute{
							MarkdownDescription: "Hostname of the node.",
							Computed:            true,
						},
						"garage_version": schema.StringAttribute{
							MarkdownDescription: "Garage version running on this node.",
							Computed:            true,
						},
						"is_up": schema.BoolAttribute{
							MarkdownDescription: "Whether this node is currently connected to the cluster.",
							Computed:            true,
						},
						"draining": schema.BoolAttribute{
							MarkdownDescription: "Whether this node belongs to an older layout version and is draining data.",
							Computed:            true,
						},
						"last_seen_secs_ago": schema.Int64Attribute{
							MarkdownDescription: "For disconnected nodes, the number of seconds since last contact. " +
								"Null if no contact has been established since Garage restarted.",
							Computed: true,
						},
						"role": schema.SingleNestedAttribute{
							MarkdownDescription: "Role assigned to this node by the cluster administrator. Null for " +
								"unassigned nodes that have not yet been added to a layout.",
							Computed: true,
							Attributes: map[string]schema.Attribute{
								"zone": schema.StringAttribute{
									MarkdownDescription: "Zone name assigned by the cluster administrator.",
									Computed:            true,
								},
								"capacity": schema.Int64Attribute{
									MarkdownDescription: "Capacity (in bytes) assigned by the cluster administrator. " +
										"Null for gateway nodes.",
									Computed: true,
								},
								"tags": schema.ListAttribute{
									MarkdownDescription: "List of tags assigned by the cluster administrator.",
									ElementType:         types.StringType,
									Computed:            true,
								},
							},
						},
					},
				},
			},
		},
	}
}

// Configure asserts the provider-supplied client and stashes it on the
// data source instance for use by Read.
func (s *Source) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	s.client = c
}

// Read fetches the current cluster status from Garage and writes the
// response into Terraform state.
//
//nolint:gocritic // ReadRequest is the framework interface signature; passing by value is required.
func (s *Source) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Trace(ctx, "garage_cluster_info: read")

	status, err := s.client.GetClusterStatus(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Failed to fetch Garage cluster status",
			"GetClusterStatus returned: "+err.Error(),
		)
		return
	}

	model := Model{
		ID:            types.StringValue(fmt.Sprintf("%d", status.LayoutVersion)),
		LayoutVersion: types.Int64Value(status.LayoutVersion),
		Nodes:         make([]NodeModel, 0, len(status.Nodes)),
	}

	for _, n := range status.Nodes {
		node := NodeModel{
			ID:              types.StringValue(n.Id),
			Addr:            stringPtrToValue(n.Addr),
			Hostname:        stringPtrToValue(n.Hostname),
			GarageVersion:   stringPtrToValue(n.GarageVersion),
			IsUp:            types.BoolValue(n.IsUp),
			Draining:        types.BoolValue(n.Draining),
			LastSeenSecsAgo: int64PtrToValue(n.LastSeenSecsAgo),
		}
		if n.Role != nil {
			tags := make([]types.String, 0, len(n.Role.Tags))
			for _, tag := range n.Role.Tags {
				tags = append(tags, types.StringValue(tag))
			}
			node.Role = &NodeRoleModel{
				Zone:     types.StringValue(n.Role.Zone),
				Capacity: int64PtrToValue(n.Role.Capacity),
				Tags:     tags,
			}
		}
		model.Nodes = append(model.Nodes, node)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func stringPtrToValue(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func int64PtrToValue(p *int64) types.Int64 {
	if p == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*p)
}

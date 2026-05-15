// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

// Package client wraps the generated Garage admin v2 OpenAPI client with
// the surface the provider actually needs: bearer auth injection, 5xx
// retry for idempotent verbs, typed error sentinels, and tflog
// request/response tracing.
//
// Higher-level provider code (resources, data sources) imports only
// this package; it never reaches into internal/client/openapi.
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/donaldgifford/terraform-provider-garage/internal/client/openapi"
)

// Client is the provider-facing handle to Garage's admin v2 API.
type Client struct {
	api      *openapi.ClientWithResponses
	endpoint string
}

// New constructs a Client. Validation is intentionally cheap and local
// (URL parseable, token non-empty); no network call is made. Auth and
// connectivity errors surface on the first API request — matching the
// terraform-provider-tls / terraform-plugin-framework idiom of keeping
// Configure() free of network I/O so `terraform plan` stays responsive
// when the remote is unreachable.
func New(endpoint, token string) (*Client, error) {
	if endpoint == "" {
		return nil, errors.New("client: endpoint is required")
	}
	if token == "" {
		return nil, errors.New("client: token is required")
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("client: parse endpoint %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("client: endpoint %q must use http or https scheme", endpoint)
	}

	httpClient := &http.Client{
		Transport: newRetryingTransport(http.DefaultTransport),
	}

	authEditor := openapi.RequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	})

	api, err := openapi.NewClientWithResponses(
		strings.TrimRight(endpoint, "/"),
		openapi.WithHTTPClient(httpClient),
		openapi.WithRequestEditorFn(authEditor),
	)
	if err != nil {
		return nil, fmt.Errorf("client: construct openapi client: %w", err)
	}

	return &Client{api: api, endpoint: endpoint}, nil
}

// Endpoint returns the configured admin URL. Used by acceptance test
// helpers and for diagnostic output; resource code should not need it.
func (c *Client) Endpoint() string {
	return c.endpoint
}

// GetClusterStatus fetches the cluster's current status (layout version
// + node list). Used by the garage_cluster_info data source and as a
// connectivity smoke test — the first API call the provider makes
// against a freshly-configured Garage admin endpoint.
func (c *Client) GetClusterStatus(ctx context.Context) (*openapi.GetClusterStatusResponse, error) {
	const op = "GetClusterStatus"

	tflog.Trace(ctx, "garage admin request", map[string]any{"op": op})

	resp, err := c.api.GetClusterStatusWithResponse(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin response", map[string]any{
		"op":     op,
		"status": resp.StatusCode(),
	})

	if err := statusToError(op, resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("%s: empty response body", op)
	}
	return resp.JSON200, nil
}

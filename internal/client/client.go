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

// CreateBucket creates a new bucket with no aliases or quotas. Aliases
// land in follow-up AddBucketAlias calls — keeps create / update
// alias-diff logic uniform. Returns the freshly-created bucket info
// (including the admin-issued id).
func (c *Client) CreateBucket(ctx context.Context) (*openapi.GetBucketInfoResponse, error) {
	const op = "CreateBucket"

	tflog.Trace(ctx, "garage admin request", map[string]any{"op": op})

	resp, err := c.api.CreateBucketWithResponse(ctx, openapi.CreateBucketJSONRequestBody{})
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

// GetBucket looks up a bucket by id. HTTP 404 maps to ErrNotFound so
// callers can drive drift cleanup via RemoveResource.
func (c *Client) GetBucket(ctx context.Context, id string) (*openapi.GetBucketInfoResponse, error) {
	const op = "GetBucket"

	if id == "" {
		return nil, fmt.Errorf("%s: id is required", op)
	}

	tflog.Trace(ctx, "garage admin request", map[string]any{"op": op, "id": id})

	params := &openapi.GetBucketInfoParams{Id: &id}
	resp, err := c.api.GetBucketInfoWithResponse(ctx, params)
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

// UpdateBucket sends only the Quotas field; CORS / lifecycle / website
// are out of Phase 2 scope. Passing nil clears the bucket quota; a
// non-nil ApiBucketQuotas with literal-zero MaxSize / MaxObjects
// enforces a literal-zero quota.
func (c *Client) UpdateBucket(ctx context.Context, id string, quotas *openapi.ApiBucketQuotas) (*openapi.GetBucketInfoResponse, error) {
	const op = "UpdateBucket"

	if id == "" {
		return nil, fmt.Errorf("%s: id is required", op)
	}

	tflog.Trace(ctx, "garage admin request", map[string]any{"op": op, "id": id})

	params := &openapi.UpdateBucketParams{Id: id}
	body := openapi.UpdateBucketJSONRequestBody{Quotas: quotas}
	resp, err := c.api.UpdateBucketWithResponse(ctx, params, body)
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

// DeleteBucket removes a bucket by id. Per the Garage admin v2 spec text
// this refuses non-empty buckets; the resource's force_destroy path
// empties via the S3 data plane before calling here.
func (c *Client) DeleteBucket(ctx context.Context, id string) error {
	const op = "DeleteBucket"

	if id == "" {
		return fmt.Errorf("%s: id is required", op)
	}

	tflog.Trace(ctx, "garage admin request", map[string]any{"op": op, "id": id})

	params := &openapi.DeleteBucketParams{Id: id}
	resp, err := c.api.DeleteBucketWithResponse(ctx, params)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin response", map[string]any{
		"op":     op,
		"status": resp.StatusCode(),
	})

	return statusToError(op, resp.StatusCode(), resp.Body)
}

// AddBucketAlias attaches a global alias to a bucket. The generated
// client's union-typed body always uses the global variant here; local
// aliases land in RFC-0001 Phase 5 (garage_bucket_key).
func (c *Client) AddBucketAlias(ctx context.Context, bucketID, globalAlias string) error {
	const op = "AddBucketAlias"

	if bucketID == "" {
		return fmt.Errorf("%s: bucketID is required", op)
	}
	if globalAlias == "" {
		return fmt.Errorf("%s: globalAlias is required", op)
	}

	var body openapi.AddBucketAliasJSONRequestBody
	if err := body.FromBucketAliasEnum0(openapi.BucketAliasEnum0{
		BucketId:    bucketID,
		GlobalAlias: globalAlias,
	}); err != nil {
		return fmt.Errorf("%s: marshal request body: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin request", map[string]any{
		"op":       op,
		"bucketId": bucketID,
		"alias":    globalAlias,
	})

	resp, err := c.api.AddBucketAliasWithResponse(ctx, body)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin response", map[string]any{
		"op":     op,
		"status": resp.StatusCode(),
	})

	return statusToError(op, resp.StatusCode(), resp.Body)
}

// RemoveBucketAlias detaches a global alias from a bucket. Symmetric to
// AddBucketAlias.
func (c *Client) RemoveBucketAlias(ctx context.Context, bucketID, globalAlias string) error {
	const op = "RemoveBucketAlias"

	if bucketID == "" {
		return fmt.Errorf("%s: bucketID is required", op)
	}
	if globalAlias == "" {
		return fmt.Errorf("%s: globalAlias is required", op)
	}

	var body openapi.RemoveBucketAliasJSONRequestBody
	if err := body.FromBucketAliasEnum0(openapi.BucketAliasEnum0{
		BucketId:    bucketID,
		GlobalAlias: globalAlias,
	}); err != nil {
		return fmt.Errorf("%s: marshal request body: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin request", map[string]any{
		"op":       op,
		"bucketId": bucketID,
		"alias":    globalAlias,
	})

	resp, err := c.api.RemoveBucketAliasWithResponse(ctx, body)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	tflog.Trace(ctx, "garage admin response", map[string]any{
		"op":     op,
		"status": resp.StatusCode(),
	})

	return statusToError(op, resp.StatusCode(), resp.Body)
}

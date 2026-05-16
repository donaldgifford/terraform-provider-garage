// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package client

// ProviderData is the value the GarageProvider's Configure plumbs into
// every resource/data source's ProviderData. Lives in the client
// package (rather than alongside the provider) to sidestep the
// provider ↔ resources import cycle.
//
// The admin Client is always non-nil after a successful Configure;
// the S3 fields are optional and only meaningful for resources that
// reach the S3 data plane (currently only garage_bucket's force_destroy
// path).
type ProviderData struct {
	// Client is the configured Garage admin v2 client.
	Client *Client

	// S3Endpoint is the URL of Garage's S3 API (typically a different
	// port from the admin endpoint). Empty when neither the
	// `s3_endpoint` provider attribute nor the GARAGE_S3_ENDPOINT env
	// var is set.
	S3Endpoint string

	// S3AccessKey is the access key id used by force_destroy's
	// bucket-emptying helper. Empty when unset.
	S3AccessKey string

	// S3SecretKey is the secret access key paired with S3AccessKey.
	// Sensitive; do not surface in diagnostics or tflog.
	S3SecretKey string
}

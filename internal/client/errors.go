// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors mapped from Garage admin API HTTP responses. Callers
// compare with `errors.Is` to drive control flow — typically `ErrNotFound`
// for "resource was deleted out-of-band, remove from state" and the
// auth sentinels for surface-level diagnostics.
var (
	// ErrNotFound corresponds to HTTP 404 on any admin endpoint.
	ErrNotFound = errors.New("garage: not found")

	// ErrUnauthorized corresponds to HTTP 401 — bad or missing bearer
	// token. Surfaced verbatim to Terraform diagnostics so users know
	// to check GARAGE_TOKEN.
	ErrUnauthorized = errors.New("garage: unauthorized")

	// ErrForbidden corresponds to HTTP 403 — token is valid but lacks
	// the required permission for the request.
	ErrForbidden = errors.New("garage: forbidden")

	// ErrServerError corresponds to any 5xx that survived the retry
	// budget. Wrapped with the actual status code so the caller can
	// surface useful detail in diagnostics.
	ErrServerError = errors.New("garage: server error")
)

// APIError annotates a sentinel with the HTTP status, optional Garage
// error message, and the operation that produced it. Implements
// `errors.Is` against the underlying sentinel so callers can keep their
// switches simple.
type APIError struct {
	Op         string
	StatusCode int
	Message    string
	sentinel   error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s: %d %s: %s", e.Op, e.StatusCode, http.StatusText(e.StatusCode), e.Message)
	}
	return fmt.Sprintf("%s: %d %s", e.Op, e.StatusCode, http.StatusText(e.StatusCode))
}

func (e *APIError) Unwrap() error {
	return e.sentinel
}

// statusToError maps an HTTP status code to a typed APIError. Returns
// nil for 2xx. Treats 1xx / 3xx as server-side errors because the
// admin API responses are documented to be 2xx / 4xx / 5xx only.
func statusToError(op string, status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}

	var sentinel error
	switch {
	case status == http.StatusNotFound:
		sentinel = ErrNotFound
	case status == http.StatusUnauthorized:
		sentinel = ErrUnauthorized
	case status == http.StatusForbidden:
		sentinel = ErrForbidden
	case status >= 500:
		sentinel = ErrServerError
	default:
		sentinel = ErrServerError
	}

	return &APIError{
		Op:         op,
		StatusCode: status,
		Message:    string(body),
		sentinel:   sentinel,
	}
}

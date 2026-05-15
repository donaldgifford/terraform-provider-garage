// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/terraform-provider-garage/internal/client"
)

func TestNew_validation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		endpoint string
		token    string
		want     string // substring expected in error
	}{
		{name: "empty endpoint", endpoint: "", token: "t", want: "endpoint is required"},
		{name: "empty token", endpoint: "http://x", token: "", want: "token is required"},
		{name: "bad scheme", endpoint: "ftp://x", token: "t", want: "scheme"},
		{name: "unparseable", endpoint: "://", token: "t", want: "parse"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.New(tc.endpoint, tc.token)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestNew_okConstructsButDoesNotCallNetwork(t *testing.T) {
	t.Parallel()

	// New must not make a network call — point at an unreachable address
	// and verify that construction still succeeds.
	c, err := client.New("http://127.0.0.1:1/", "secret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c == nil {
		t.Fatal("New: nil client")
	}
	if got := c.Endpoint(); got != "http://127.0.0.1:1/" {
		t.Fatalf("Endpoint: %q", got)
	}
}

func TestGetClusterStatus_bearerHeaderSent(t *testing.T) {
	t.Parallel()

	var seen atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"layoutVersion":1,"nodes":[]}`))
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "topsecret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.GetClusterStatus(context.Background()); err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}

	got, _ := seen.Load().(string)
	if got != "Bearer topsecret" {
		t.Fatalf("Authorization header = %q, want %q", got, "Bearer topsecret")
	}
}

func TestGetClusterStatus_errorMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		statusCode int
		want       error
	}{
		{name: "401", statusCode: http.StatusUnauthorized, want: client.ErrUnauthorized},
		{name: "403", statusCode: http.StatusForbidden, want: client.ErrForbidden},
		{name: "404", statusCode: http.StatusNotFound, want: client.ErrNotFound},
		{name: "500 after retries", statusCode: http.StatusInternalServerError, want: client.ErrServerError},
		{name: "502 after retries", statusCode: http.StatusBadGateway, want: client.ErrServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.statusCode)
			}))
			t.Cleanup(server.Close)

			c, err := client.New(server.URL, "t")
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			_, err = c.GetClusterStatus(context.Background())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tc.want)
			}

			var apiErr *client.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error %v is not *APIError", err)
			}
			if apiErr.StatusCode != tc.statusCode {
				t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, tc.statusCode)
			}
		})
	}
}

// TestGetClusterStatus_retriesOn5xxThenSucceeds verifies that a GET that
// hits 5xx on early attempts succeeds once the server stabilizes —
// confirming both that retries fire and that they stop on first 2xx.
func TestGetClusterStatus_retriesOn5xxThenSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"layoutVersion":2,"nodes":[]}`))
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Bound the total test time well above retry budget (~1.75s worst case).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	got, err := c.GetClusterStatus(ctx)
	if err != nil {
		t.Fatalf("GetClusterStatus: %v", err)
	}
	if got.LayoutVersion != 2 {
		t.Fatalf("LayoutVersion = %d, want 2", got.LayoutVersion)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("server received %d calls, want 3", n)
	}
}

// TestGetClusterStatus_retryBudgetExhausted verifies that a persistent
// 5xx eventually surfaces as ErrServerError after exactly 3 attempts.
func TestGetClusterStatus_retryBudgetExhausted(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.GetClusterStatus(context.Background())
	if !errors.Is(err, client.ErrServerError) {
		t.Fatalf("err = %v, want errors.Is(..., ErrServerError) = true", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("server received %d calls, want 3", n)
	}
}

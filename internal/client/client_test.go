// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/donaldgifford/terraform-provider-garage/internal/client"
	"github.com/donaldgifford/terraform-provider-garage/internal/client/openapi"
)

// bucketInfoJSON is a minimal but complete GetBucketInfoResponse body
// for happy-path stubs — every required field present, all optional
// fields omitted.
const bucketInfoJSON = `{
	"bytes": 0,
	"created": "2026-05-15T12:00:00Z",
	"globalAliases": [],
	"id": "abc123",
	"keys": [],
	"objects": 0,
	"quotas": {},
	"unfinishedMultipartUploadBytes": 0,
	"unfinishedMultipartUploadParts": 0,
	"unfinishedMultipartUploads": 0,
	"unfinishedUploads": 0,
	"websiteAccess": false
}`

func writeBucketInfo(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, bucketInfoJSON)
}

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

// ── Bucket methods ──────────────────────────────────────────────────────
//
// IMPL-0002 Phase 1: cover the 6 bucket wrapper methods with the same
// shape as GetClusterStatus's tests above — happy path, error mapping,
// retry policy split between idempotent and mutating verbs, input
// validation, and request-body shape verification.

func TestCreateBucket_success(t *testing.T) {
	t.Parallel()

	var (
		seenAuth atomic.Value
		seenBody atomic.Value
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth.Store(r.Header.Get("Authorization"))
		b, _ := io.ReadAll(r.Body)
		seenBody.Store(string(b))
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "topsecret")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.CreateBucket(context.Background())
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if got.Id != "abc123" {
		t.Fatalf("Id = %q, want %q", got.Id, "abc123")
	}
	if auth, _ := seenAuth.Load().(string); auth != "Bearer topsecret" {
		t.Fatalf("Authorization = %q", auth)
	}
	// Empty request body — both alias fields are omitempty pointers, so
	// the marshaled body must be `{}` (no globalAlias / localAlias keys).
	body, _ := seenBody.Load().(string)
	if strings.TrimSpace(body) != "{}" {
		t.Fatalf("request body = %q, want %q", body, "{}")
	}
}

func TestGetBucket_success(t *testing.T) {
	t.Parallel()

	var seenQuery atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery.Store(r.URL.Query().Get("id"))
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := c.GetBucket(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("GetBucket: %v", err)
	}
	if got.Id != "abc123" {
		t.Fatalf("Id = %q, want %q", got.Id, "abc123")
	}
	if q, _ := seenQuery.Load().(string); q != "abc123" {
		t.Fatalf("id query param = %q, want %q", q, "abc123")
	}
}

func TestGetBucket_notFoundMapsToErrNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.GetBucket(context.Background(), "missing")
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is(..., ErrNotFound) = true", err)
	}
}

// TestGetBucket_retriesOn5xxThenSucceeds confirms that GET inherits the
// transport-level retry policy — symmetrical with GetClusterStatus's
// retry test.
func TestGetBucket_retriesOn5xxThenSucceeds(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	if _, err := c.GetBucket(ctx, "abc123"); err != nil {
		t.Fatalf("GetBucket: %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("server received %d calls, want 3", n)
	}
}

func TestUpdateBucket_success(t *testing.T) {
	t.Parallel()

	var (
		seenQuery atomic.Value
		seenBody  atomic.Value
		seenMeth  atomic.Value
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMeth.Store(r.Method)
		seenQuery.Store(r.URL.Query().Get("id"))
		b, _ := io.ReadAll(r.Body)
		seenBody.Store(b)
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	maxSize := int64(1024)
	maxObjects := int64(50)
	quotas := &openapi.ApiBucketQuotas{MaxSize: &maxSize, MaxObjects: &maxObjects}

	if _, err := c.UpdateBucket(context.Background(), "abc123", quotas); err != nil {
		t.Fatalf("UpdateBucket: %v", err)
	}

	if m, _ := seenMeth.Load().(string); m != http.MethodPost {
		t.Fatalf("method = %q, want POST", m)
	}
	if q, _ := seenQuery.Load().(string); q != "abc123" {
		t.Fatalf("id query = %q", q)
	}

	body, _ := seenBody.Load().([]byte)
	var got openapi.UpdateBucketRequestBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal request body: %v\nbody=%s", err, body)
	}
	if got.Quotas == nil || got.Quotas.MaxSize == nil || *got.Quotas.MaxSize != 1024 {
		t.Fatalf("quotas.MaxSize round-trip lost: body=%s", body)
	}
	if got.Quotas.MaxObjects == nil || *got.Quotas.MaxObjects != 50 {
		t.Fatalf("quotas.MaxObjects round-trip lost: body=%s", body)
	}
}

// TestUpdateBucket_quotaBodyShape verifies the three quota states the
// resource needs to express per DESIGN-0002 §Quota semantics:
// nil → no quotas key; non-nil with all nil fields → empty quotas object;
// non-nil with literal zero MaxSize → quotas.maxSize == 0.
func TestUpdateBucket_quotaBodyShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		quotas           *openapi.ApiBucketQuotas
		wantQuotasField  bool   // is the "quotas" key present in the body?
		wantMaxSizeKey   bool   // is "maxSize" present inside quotas?
		wantMaxSizeValue *int64 // if present, what value?
	}{
		{
			name:            "nil quotas omits field",
			quotas:          nil,
			wantQuotasField: false,
		},
		{
			name:            "empty quotas object",
			quotas:          &openapi.ApiBucketQuotas{},
			wantQuotasField: true,
			wantMaxSizeKey:  false,
		},
		{
			name:             "literal zero MaxSize",
			quotas:           &openapi.ApiBucketQuotas{MaxSize: ptrInt64(0)},
			wantQuotasField:  true,
			wantMaxSizeKey:   true,
			wantMaxSizeValue: ptrInt64(0),
		},
		{
			name:             "non-zero MaxSize",
			quotas:           &openapi.ApiBucketQuotas{MaxSize: ptrInt64(99)},
			wantQuotasField:  true,
			wantMaxSizeKey:   true,
			wantMaxSizeValue: ptrInt64(99),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var seenBody atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				seenBody.Store(b)
				writeBucketInfo(w)
			}))
			t.Cleanup(server.Close)

			c, err := client.New(server.URL, "t")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := c.UpdateBucket(context.Background(), "abc", tc.quotas); err != nil {
				t.Fatalf("UpdateBucket: %v", err)
			}

			body, _ := seenBody.Load().([]byte)
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("unmarshal body: %v: %s", err, body)
			}

			quotasRaw, hasQuotas := raw["quotas"]
			if hasQuotas != tc.wantQuotasField {
				t.Fatalf("quotas field present = %v, want %v (body=%s)", hasQuotas, tc.wantQuotasField, body)
			}
			if !hasQuotas {
				return
			}

			var quotasMap map[string]json.RawMessage
			if err := json.Unmarshal(quotasRaw, &quotasMap); err != nil {
				t.Fatalf("unmarshal quotas: %v: %s", err, quotasRaw)
			}
			maxSizeRaw, hasMaxSize := quotasMap["maxSize"]
			if hasMaxSize != tc.wantMaxSizeKey {
				t.Fatalf("maxSize key present = %v, want %v (body=%s)", hasMaxSize, tc.wantMaxSizeKey, body)
			}
			if tc.wantMaxSizeKey {
				var got int64
				if err := json.Unmarshal(maxSizeRaw, &got); err != nil {
					t.Fatalf("unmarshal maxSize: %v", err)
				}
				if got != *tc.wantMaxSizeValue {
					t.Fatalf("maxSize = %d, want %d", got, *tc.wantMaxSizeValue)
				}
			}
		})
	}
}

func TestDeleteBucket_success(t *testing.T) {
	t.Parallel()

	var (
		seenMeth  atomic.Value
		seenQuery atomic.Value
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMeth.Store(r.Method)
		seenQuery.Store(r.URL.Query().Get("id"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.DeleteBucket(context.Background(), "abc123"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	// Garage admin v2 uses RPC-style POST for all mutating ops, including
	// delete (`POST /v2/DeleteBucket?id=...`). Verb is asserted explicitly
	// here so a future spec regen that flipped to DELETE would fail loudly.
	if m, _ := seenMeth.Load().(string); m != http.MethodPost {
		t.Fatalf("method = %q, want POST", m)
	}
	if q, _ := seenQuery.Load().(string); q != "abc123" {
		t.Fatalf("id query = %q", q)
	}
}

func TestAddBucketAlias_success(t *testing.T) {
	t.Parallel()

	var seenBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody.Store(b)
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.AddBucketAlias(context.Background(), "abc123", "my-alias"); err != nil {
		t.Fatalf("AddBucketAlias: %v", err)
	}

	body, _ := seenBody.Load().([]byte)
	var got openapi.BucketAliasEnum0
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal body: %v: %s", err, body)
	}
	if got.BucketId != "abc123" || got.GlobalAlias != "my-alias" {
		t.Fatalf("body = %+v, want {BucketId:abc123, GlobalAlias:my-alias}", got)
	}
}

func TestRemoveBucketAlias_success(t *testing.T) {
	t.Parallel()

	var seenBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody.Store(b)
		writeBucketInfo(w)
	}))
	t.Cleanup(server.Close)

	c, err := client.New(server.URL, "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.RemoveBucketAlias(context.Background(), "abc123", "old-alias"); err != nil {
		t.Fatalf("RemoveBucketAlias: %v", err)
	}

	body, _ := seenBody.Load().([]byte)
	var got openapi.BucketAliasEnum0
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal body: %v: %s", err, body)
	}
	if got.BucketId != "abc123" || got.GlobalAlias != "old-alias" {
		t.Fatalf("body = %+v", got)
	}
}

// TestBucketMethods_errorMapping table-drives the auth/forbidden/server-
// error sentinels across the 6 methods. 404→ErrNotFound is covered only
// on GetBucket (the only method where the resource depends on it for
// drift cleanup).
func TestBucketMethods_errorMapping(t *testing.T) {
	t.Parallel()

	statuses := []struct {
		code int
		want error
	}{
		{http.StatusUnauthorized, client.ErrUnauthorized},
		{http.StatusForbidden, client.ErrForbidden},
		{http.StatusInternalServerError, client.ErrServerError},
	}

	methods := []struct {
		name string
		call func(*client.Client) error
	}{
		{"CreateBucket", func(c *client.Client) error { _, err := c.CreateBucket(context.Background()); return err }},
		{"GetBucket", func(c *client.Client) error { _, err := c.GetBucket(context.Background(), "id"); return err }},
		{"UpdateBucket", func(c *client.Client) error { _, err := c.UpdateBucket(context.Background(), "id", nil); return err }},
		{"DeleteBucket", func(c *client.Client) error { return c.DeleteBucket(context.Background(), "id") }},
		{"AddBucketAlias", func(c *client.Client) error { return c.AddBucketAlias(context.Background(), "id", "a") }},
		{"RemoveBucketAlias", func(c *client.Client) error { return c.RemoveBucketAlias(context.Background(), "id", "a") }},
	}

	for _, tt := range methods {
		for _, st := range statuses {
			t.Run(tt.name+"_"+http.StatusText(st.code), func(t *testing.T) {
				t.Parallel()

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(st.code)
				}))
				t.Cleanup(server.Close)

				c, err := client.New(server.URL, "t")
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				err = tt.call(c)
				if !errors.Is(err, st.want) {
					t.Fatalf("errors.Is(%v, %v) = false", err, st.want)
				}
			})
		}
	}
}

// TestBucketMutations_noRetryOn5xx ensures that the non-idempotent verbs
// (CreateBucket, UpdateBucket, DeleteBucket, AddBucketAlias,
// RemoveBucketAlias) pass through 5xx without invoking the retry budget.
// IMPL-0001 §Decisions #8 explains why: we cannot tell whether the
// server-side mutation succeeded before the connection went bad.
func TestBucketMutations_noRetryOn5xx(t *testing.T) {
	t.Parallel()

	methods := []struct {
		name string
		call func(*client.Client) error
	}{
		{"CreateBucket", func(c *client.Client) error { _, err := c.CreateBucket(context.Background()); return err }},
		{"UpdateBucket", func(c *client.Client) error { _, err := c.UpdateBucket(context.Background(), "id", nil); return err }},
		{"DeleteBucket", func(c *client.Client) error { return c.DeleteBucket(context.Background(), "id") }},
		{"AddBucketAlias", func(c *client.Client) error { return c.AddBucketAlias(context.Background(), "id", "a") }},
		{"RemoveBucketAlias", func(c *client.Client) error { return c.RemoveBucketAlias(context.Background(), "id", "a") }},
	}

	for _, tt := range methods {
		t.Run(tt.name, func(t *testing.T) {
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
			err = tt.call(c)
			if !errors.Is(err, client.ErrServerError) {
				t.Fatalf("err = %v, want errors.Is(..., ErrServerError) = true", err)
			}
			if n := calls.Load(); n != 1 {
				t.Fatalf("server received %d calls, want 1 (no retry on POST/DELETE)", n)
			}
		})
	}
}

// TestBucketMethods_inputValidation covers the cheap pre-flight argument
// checks (empty id, empty alias) before any HTTP call is made.
func TestBucketMethods_inputValidation(t *testing.T) {
	t.Parallel()

	c, err := client.New("http://127.0.0.1:1/", "t")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name string
		call func() error
		want string
	}{
		{"GetBucket empty id", func() error { _, e := c.GetBucket(context.Background(), ""); return e }, "id is required"},
		{"UpdateBucket empty id", func() error { _, e := c.UpdateBucket(context.Background(), "", nil); return e }, "id is required"},
		{"DeleteBucket empty id", func() error { return c.DeleteBucket(context.Background(), "") }, "id is required"},
		{"AddBucketAlias empty bucket", func() error { return c.AddBucketAlias(context.Background(), "", "a") }, "bucketID is required"},
		{"AddBucketAlias empty alias", func() error { return c.AddBucketAlias(context.Background(), "b", "") }, "globalAlias is required"},
		{"RemoveBucketAlias empty bucket", func() error { return c.RemoveBucketAlias(context.Background(), "", "a") }, "bucketID is required"},
		{"RemoveBucketAlias empty alias", func() error { return c.RemoveBucketAlias(context.Background(), "b", "") }, "globalAlias is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %q does not contain %q", err, tc.want)
			}
		})
	}
}

func ptrInt64(v int64) *int64 { return &v }

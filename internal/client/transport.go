// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"io"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Retry tuning. Mirrors IMPL-0001 §Decisions #7: 3 attempts max, 250ms
// base, 2x multiplier. Schedule is 250→500→1000ms; worst-case wait ~1.75s
// across the three attempts before the final failure surfaces.
const (
	retryMaxAttempts = 3
	retryBaseDelay   = 250 * time.Millisecond
	retryMultiplier  = 2
)

// retryingTransport wraps an underlying RoundTripper with bounded
// exponential-backoff retries on 5xx responses for idempotent verbs
// (GET, HEAD). Non-idempotent verbs (POST/PUT/PATCH/DELETE) pass through
// unmodified — see IMPL-0001 §Decisions #8 for why.
type retryingTransport struct {
	base  http.RoundTripper
	sleep func(time.Duration) // injection seam for tests; nil → time.Sleep
}

func newRetryingTransport(base http.RoundTripper) *retryingTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryingTransport{base: base}
}

func (t *retryingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isIdempotent(req.Method) {
		return t.base.RoundTrip(req)
	}

	var (
		resp     *http.Response
		err      error
		delay    = retryBaseDelay
		attempts = retryMaxAttempts
	)

	for attempt := 1; attempt <= attempts; attempt++ {
		resp, err = t.base.RoundTrip(req)

		if err == nil && resp != nil && resp.StatusCode < 500 {
			return resp, nil
		}

		if attempt == attempts {
			break
		}

		if resp != nil {
			drainAndClose(resp.Body)
		}

		tflog.Debug(req.Context(), "garage admin retry", map[string]any{
			"method":  req.Method,
			"url":     req.URL.String(),
			"attempt": attempt,
			"delay":   delay.String(),
		})

		t.doSleep(delay)
		delay *= retryMultiplier
	}

	return resp, err
}

func (t *retryingTransport) doSleep(d time.Duration) {
	if t.sleep != nil {
		t.sleep(d)
		return
	}
	time.Sleep(d)
}

// isIdempotent returns true for HTTP methods that can be safely retried
// on transient 5xx without risking duplicate server-side side effects.
// See RFC 7231 §4.2.2.
func isIdempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// drainAndClose reads the body to completion before closing so the
// underlying TCP connection can be reused under HTTP/1.1 keep-alive.
// Errors are intentionally swallowed: an unread body about to be
// discarded has no recovery path.
func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	if _, err := io.Copy(io.Discard, body); err != nil {
		_ = body.Close()
		return
	}
	_ = body.Close()
}

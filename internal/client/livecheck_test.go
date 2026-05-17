// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

//go:build garageprobe

// Live API verification probes for IMPL-0002 Phase 2. These tests
// exercise the bucket admin v2 surface against a real Garage container
// to capture observed behavior that DESIGN-0002 flagged as
// "verify in implementation". The findings get folded back into
// DESIGN-0002 §Decisions and inform garage_bucket's alias-diff order,
// idempotency assumptions, and diagnostic messages.
//
// Run on-demand:
//
//	go test -tags=garageprobe -run TestLiveBucket -v ./internal/client/...
//
// Default builds and CI ignore this file via the build tag — these
// probes start a Garage container per test, which is heavier than the
// unit-test budget. Re-run when bumping the Garage version pin to make
// sure observed behavior hasn't changed.

package client_test

import (
	"context"
	"errors"
	"testing"

	"github.com/donaldgifford/terraform-provider-garage/internal/acctest"
	"github.com/donaldgifford/terraform-provider-garage/internal/client"
)

// TestLiveBucket_lastAliasRemoval covers DESIGN-0002 Verification A.
// Creates a bucket with a single alias, removes the alias, and records
// which of three behaviors Garage exhibits:
//
//	(i)   RemoveBucketAlias returns 4xx — Garage refuses to detach the
//	      sole handle on a bucket
//	(ii)  Returns 2xx and GetBucket(id) still returns the bucket with
//	      no aliases — the bucket lives on, reachable only by id
//	(iii) Returns 2xx and GetBucket(id) returns 404 — Garage
//	      cascade-deleted the bucket
//
// Phase 5's alias-diff order (adds-before-removes) is correct in all
// three cases. Knowing which one Garage actually does just makes the
// diagnostic better when the resource hits the edge case.
func TestLiveBucket_lastAliasRemoval(t *testing.T) {
	t.Parallel()
	g := acctest.Start(t)
	c, err := client.New(g.Endpoint, g.AdminToken)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	ctx := context.Background()
	bucket, err := c.CreateBucket(ctx)
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const alias = "livecheck-last-alias"
	if err := c.AddBucketAlias(ctx, bucket.Id, alias); err != nil {
		t.Fatalf("AddBucketAlias: %v", err)
	}

	removeErr := c.RemoveBucketAlias(ctx, bucket.Id, alias)
	t.Logf("RemoveBucketAlias of sole alias: err=%v", removeErr)

	if removeErr != nil {
		t.Logf("FINDING: Garage refused last-alias removal (option (i))")
		return
	}

	_, getErr := c.GetBucket(ctx, bucket.Id)
	switch {
	case errors.Is(getErr, client.ErrNotFound):
		t.Logf("FINDING: Garage cascade-deleted the bucket (option (iii))")
	case getErr != nil:
		t.Fatalf("unexpected GetBucket error: %v", getErr)
	default:
		t.Logf("FINDING: bucket lives on, reachable only by id (option (ii))")
	}
}

// TestLiveBucket_addAliasIdempotency covers DESIGN-0002 Verification B.
// Adds the same alias twice to the same bucket. Records whether the
// second call no-ops (2xx, idempotent) or fails (4xx).
func TestLiveBucket_addAliasIdempotency(t *testing.T) {
	t.Parallel()
	g := acctest.Start(t)
	c, err := client.New(g.Endpoint, g.AdminToken)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	ctx := context.Background()
	bucket, err := c.CreateBucket(ctx)
	if err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	const alias = "livecheck-idempotent"
	if err := c.AddBucketAlias(ctx, bucket.Id, alias); err != nil {
		t.Fatalf("AddBucketAlias #1: %v", err)
	}

	if err := c.AddBucketAlias(ctx, bucket.Id, alias); err == nil {
		t.Logf("FINDING: AddBucketAlias is idempotent — second call 2xx (no-op)")
	} else {
		t.Logf("FINDING: AddBucketAlias rejected duplicate: err=%v", err)
	}
}

// TestLiveBucket_foreignAliasTakeover covers DESIGN-0002 Verification D.
// Bucket A claims alias "shared"; bucket B tries to claim the same
// alias. Records the error response shape for diagnostic clarity in
// the resource's Update path.
func TestLiveBucket_foreignAliasTakeover(t *testing.T) {
	t.Parallel()
	g := acctest.Start(t)
	c, err := client.New(g.Endpoint, g.AdminToken)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	ctx := context.Background()
	bucketA, err := c.CreateBucket(ctx)
	if err != nil {
		t.Fatalf("CreateBucket A: %v", err)
	}
	bucketB, err := c.CreateBucket(ctx)
	if err != nil {
		t.Fatalf("CreateBucket B: %v", err)
	}

	const alias = "livecheck-shared"
	if err := c.AddBucketAlias(ctx, bucketA.Id, alias); err != nil {
		t.Fatalf("AddBucketAlias on A: %v", err)
	}

	if err := c.AddBucketAlias(ctx, bucketB.Id, alias); err == nil {
		t.Logf("FINDING: alias takeover succeeded — Garage allows shared global aliases (unexpected)")
	} else {
		var apiErr *client.APIError
		if errors.As(err, &apiErr) {
			t.Logf("FINDING: alias takeover refused: status=%d body=%q", apiErr.StatusCode, apiErr.Message)
		} else {
			t.Logf("FINDING: alias takeover failed with non-APIError: %v", err)
		}
	}
}

// Verification C (DeleteBucket on non-empty) is deferred to IMPL-0002
// Phase 6: it needs an S3 PUT to make the bucket non-empty, which depends
// on the aws-sdk-go-v2 setup Phase 6 introduces. The spec text already
// asserts the refuse behavior ("A bucket cannot be deleted if it is not
// empty"); the remaining diagnostic-quality question is captured by
// Phase 6's TestAccGarageBucket_rejectNonEmptyWithoutForce.

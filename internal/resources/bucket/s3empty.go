// Copyright (c) 2026 Donald Gifford
// SPDX-License-Identifier: MPL-2.0

package bucket

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// s3EmptyConfig carries the inputs emptyBucket needs to talk to the S3
// data plane. Kept as a struct so the resource can pass its captured
// provider-data fields without re-deriving them per call.
type s3EmptyConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
}

// validate checks that all three fields are populated before any
// network call. Returns nil on success or a multi-line diagnostic-ready
// error naming the missing fields.
func (c s3EmptyConfig) validate() error {
	var missing []string
	if c.Endpoint == "" {
		missing = append(missing, "s3_endpoint")
	}
	if c.AccessKey == "" {
		missing = append(missing, "s3_access_key")
	}
	if c.SecretKey == "" {
		missing = append(missing, "s3_secret_key")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"force_destroy requires provider-level S3 credentials; missing: %v "+
			"(set the provider attributes or the GARAGE_S3_* env vars)",
		missing,
	)
}

// emptyBucket enumerates every object in `bucketName` via ListObjectsV2
// and batch-deletes them through DeleteObjects. Loops until the
// paginator reports the bucket has no more pages.
//
// The S3 client is configured for Garage compatibility:
//   - BaseEndpoint pinned to the provided S3 endpoint
//   - Static credentials (no AWS environment / shared-config sourcing)
//   - Region = "garage" — Garage ignores region but aws-sdk-go-v2's
//     signing path requires a non-empty value
//   - UsePathStyle = true — Garage does not implement virtual-hosted
//     S3 addressing
func emptyBucket(ctx context.Context, cfg s3EmptyConfig, bucketName string) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if bucketName == "" {
		return errors.New("emptyBucket: bucketName is required")
	}

	awsCfg := aws.Config{
		Region:      "garage",
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}

	cli := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = true
	})

	paginator := s3.NewListObjectsV2Paginator(cli, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("emptyBucket: list objects in %q: %w", bucketName, err)
		}
		if len(page.Contents) == 0 {
			continue
		}

		ids := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, obj := range page.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: obj.Key})
		}

		_, err = cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("emptyBucket: delete %d objects in %q: %w", len(ids), bucketName, err)
		}
	}
	return nil
}

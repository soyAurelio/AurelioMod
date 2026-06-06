package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Compile-time interface check.
var _ R2Store = (*S3AuditStore)(nil)

// S3AuditStore implements R2Store by writing audit events as JSON objects
// to any S3-compatible storage (Cloudflare R2 in production, MinIO in dev).
//
// Key format: workspace_id/YYYY/MM/DD/audit_id.json
//
// Configuration is via environment variables:
//
//	R2_ENDPOINT         — S3-compatible endpoint (default: http://minio:9000)
//	R2_ACCESS_KEY_ID    — access key (default: minioadmin for dev)
//	R2_SECRET_ACCESS_KEY — secret key (default: minioadmin for dev)
//	R2_BUCKET           — bucket name (default: aureliomod-audit)
//	R2_REGION           — AWS region (default: auto for R2, us-east-1 for MinIO)
//	R2_AUDIT_ENABLED    — feature gate (default: false)
type S3AuditStore struct {
	client    *s3.Client
	bucket    string
}

// NewS3AuditStoreFromEnv creates an S3AuditStore from environment variables.
// Returns nil if R2_AUDIT_ENABLED is not "true" (safe default).
func NewS3AuditStoreFromEnv(ctx context.Context) (*S3AuditStore, error) {
	if os.Getenv("R2_AUDIT_ENABLED") != "true" {
		return nil, nil
	}

	endpoint := envOrDefault("R2_ENDPOINT", "http://minio:9000")
	bucket := envOrDefault("R2_BUCKET", "aureliomod-audit")
	region := envOrDefault("R2_REGION", "auto")
	accessKey := envOrDefault("R2_ACCESS_KEY_ID", "minioadmin")
	secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")
	if secretKey == "" {
		secretKey = "minioadmin" // dev default
	}

	slog.InfoContext(ctx, "creating S3 audit store",
		slog.String("endpoint", endpoint),
		slog.String("bucket", bucket),
	)

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("s3 audit: load config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true // required for MinIO and R2
	})

	return &S3AuditStore{client: client, bucket: bucket}, nil
}

// StoreAudit writes an audit event as a JSON object to S3-compatible storage.
// The key follows the format: workspace_id/YYYY/MM/DD/audit_id.json
// This enables lifecycle policies for automatic purging (e.g., delete after
// 12 months in R2).
func (s *S3AuditStore) StoreAudit(ctx context.Context, event AuditEvent) error {
	key := objectKey(event)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("s3 audit: marshal event: %w", err)
	}

	contentType := "application/json"
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: &contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 audit: put object %s: %w", key, err)
	}

	return nil
}

// objectKey builds the S3 object key for an audit event:
//
//	workspace_id/YYYY/MM/DD/audit_id.json
func objectKey(event AuditEvent) string {
	ts := event.TimestampUTC
	return path.Join(
		sanitizeKeySegment(event.WorkspaceID),
		fmt.Sprintf("%04d", ts.Year()),
		fmt.Sprintf("%02d", ts.Month()),
		fmt.Sprintf("%02d", ts.Day()),
		event.AuditID+".json",
	)
}

// sanitizeKeySegment removes characters that are unsafe in S3 object keys.
func sanitizeKeySegment(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, s)
}

// envOrDefault returns the environment variable value, or fallback if unset.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Package s3 implements the Nipa Enterprise Edition chunk content backend
// backed by S3-compatible object storage (AWS S3, MinIO, Ceph, R2). Content is
// keyed by its BLAKE3 hash and sharded like the local backend:
//
//	<prefix>/<hash[:2]>/<hash[2:]>
//
// Enterprise Edition: see ee/LICENSE.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

var _ storage.ChunkStore = (*Store)(nil)
var _ storage.DirectTransferStore = (*Store)(nil)

// maxPresignTTL is the longest expiry SigV4 presigned requests support.
const maxPresignTTL = 7 * 24 * time.Hour

// Config describes the S3-compatible backend. Endpoint may include a scheme
// (https:// or http://); a bare host defaults to https. When no static
// credentials are given the AWS environment and EC2/ECS instance metadata
// providers are used in that order.
type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
}

// Store persists chunk content in an S3-compatible bucket.
type Store struct {
	client *minio.Client
	bucket string
	prefix string
}

// New validates the configuration, creates the S3 client and probes the
// bucket so misconfiguration fails at startup.
func New(ctx context.Context, cfg Config) (*Store, error) {
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	endpoint, secure, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	creds, err := credentialsFor(cfg)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  creds,
		Secure: secure,
		Region: strings.TrimSpace(cfg.Region),
	})
	if err != nil {
		return nil, fmt.Errorf("s3: create client for %s: %w", endpoint, err)
	}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("s3: check bucket %q: %w", bucket, err)
	}
	if !exists {
		return nil, fmt.Errorf("s3: bucket %q does not exist", bucket)
	}
	return &Store{client: client, bucket: bucket, prefix: normalizePrefix(cfg.Prefix)}, nil
}

func (s *Store) Put(ctx context.Context, hash domain.Hash, data []byte) error {
	exists, err := s.Exists(ctx, hash)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = s.client.PutObject(ctx, s.bucket, s.key(hash), bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return fmt.Errorf("s3: put chunk %s: %w", hash, err)
	}
	return nil
}

func (s *Store) Get(ctx context.Context, hash domain.Hash) ([]byte, error) {
	object, err := s.client.GetObject(ctx, s.bucket, s.key(hash), minio.GetObjectOptions{})
	if err != nil {
		return nil, s.mapError(err, hash)
	}
	defer func() { _ = object.Close() }()

	data, err := io.ReadAll(object)
	if err != nil {
		return nil, s.mapError(err, hash)
	}
	return data, nil
}

func (s *Store) Size(ctx context.Context, hash domain.Hash) (int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, s.key(hash), minio.StatObjectOptions{})
	if err != nil {
		return 0, s.mapError(err, hash)
	}
	return info.Size, nil
}

func (s *Store) Exists(ctx context.Context, hash domain.Hash) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, s.key(hash), minio.StatObjectOptions{})
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("s3: stat chunk %s: %w", hash, err)
	}
	return true, nil
}

func (s *Store) Close() error {
	return nil
}

// PresignDownload returns an absolute URL the client can GET the chunk from.
// Expiries beyond SigV4's 7-day limit are clamped.
func (s *Store) PresignDownload(ctx context.Context, hash domain.Hash, expires time.Duration) (string, error) {
	if expires > maxPresignTTL {
		expires = maxPresignTTL
	}
	url, err := s.client.PresignedGetObject(ctx, s.bucket, s.key(hash), expires, nil)
	if err != nil {
		return "", fmt.Errorf("s3: presign download for chunk %s: %w", hash, err)
	}
	return url.String(), nil
}

// PresignUpload returns a POST policy target for one chunk. The policy pins
// the exact content length, so the object store rejects any body larger or
// smaller than size before it is written.
func (s *Store) PresignUpload(ctx context.Context, hash domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	if size <= 0 {
		return storage.UploadTarget{}, fmt.Errorf("s3: invalid chunk size %d", size)
	}
	if expires > maxPresignTTL {
		expires = maxPresignTTL
	}
	policy := minio.NewPostPolicy()
	if err := policy.SetBucket(s.bucket); err != nil {
		return storage.UploadTarget{}, fmt.Errorf("s3: upload policy: %w", err)
	}
	if err := policy.SetKey(s.key(hash)); err != nil {
		return storage.UploadTarget{}, fmt.Errorf("s3: upload policy: %w", err)
	}
	if err := policy.SetContentLengthRange(size, size); err != nil {
		return storage.UploadTarget{}, fmt.Errorf("s3: upload policy: %w", err)
	}
	if err := policy.SetExpires(time.Now().Add(expires)); err != nil {
		return storage.UploadTarget{}, fmt.Errorf("s3: upload policy: %w", err)
	}
	url, formData, err := s.client.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return storage.UploadTarget{}, fmt.Errorf("s3: presign upload for chunk %s: %w", hash, err)
	}
	return storage.UploadTarget{URL: url.String(), Method: http.MethodPost, FormData: formData}, nil
}

// DeleteChunk removes chunk content so a client can re-upload it.
func (s *Store) DeleteChunk(ctx context.Context, hash domain.Hash) error {
	if err := s.client.RemoveObject(ctx, s.bucket, s.key(hash), minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("s3: delete chunk %s: %w", hash, err)
	}
	return nil
}

func (s *Store) mapError(err error, hash domain.Hash) error {
	if isNotFound(err) {
		return domain.NewErrorNotFound("chunk not found: " + hash.String())
	}
	return fmt.Errorf("s3: chunk %s: %w", hash, err)
}

func (s *Store) key(hash domain.Hash) string {
	hex := hash.String()
	base := hex[:2] + "/" + hex[2:]
	if s.prefix == "" {
		return base
	}
	return s.prefix + "/" + base
}

func isNotFound(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NotFound" || response.StatusCode == 404
}

func parseEndpoint(endpoint string) (string, bool, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "s3.amazonaws.com", true, nil
	}
	if !strings.Contains(endpoint, "://") {
		return strings.TrimSuffix(endpoint, "/"), true, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", false, fmt.Errorf("s3: invalid endpoint %q: %w", endpoint, err)
	}
	if parsed.Host == "" {
		return "", false, fmt.Errorf("s3: invalid endpoint %q", endpoint)
	}
	switch parsed.Scheme {
	case "https":
		return parsed.Host, true, nil
	case "http":
		return parsed.Host, false, nil
	default:
		return "", false, fmt.Errorf("s3: unsupported endpoint scheme %q", parsed.Scheme)
	}
}

func credentialsFor(cfg Config) (*credentials.Credentials, error) {
	accessKeyID := strings.TrimSpace(cfg.AccessKeyID)
	secretAccessKey := strings.TrimSpace(cfg.SecretAccessKey)
	switch {
	case accessKeyID == "" && secretAccessKey == "":
		return credentials.NewChainCredentials([]credentials.Provider{
			&credentials.EnvAWS{},
			&credentials.IAM{},
		}), nil
	case accessKeyID == "" || secretAccessKey == "":
		return nil, errors.New("s3: access key id and secret access key must be set together")
	default:
		return credentials.NewStaticV4(accessKeyID, secretAccessKey, ""), nil
	}
}

func normalizePrefix(prefix string) string {
	return strings.Trim(strings.TrimSpace(prefix), "/")
}

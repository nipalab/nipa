package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/nipalab/nipa/internal/storage"
)

const (
	probeTimeout = 30 * time.Second
	probeTTL     = 5 * time.Minute
	probeSize    = 64
)

// Probe check names, reported by ProbeError.Check.
const (
	ProbeBucket        = "bucket"
	ProbeContentLength = "signed_content_length"
	ProbeUpload        = "presigned_put"
	ProbeCreateOnly    = "conditional_create"
	ProbeDownload      = "presigned_get"
	ProbeStat          = "stat"
	ProbeDelete        = "delete"
)

// ProbeError names the S3 capability a backend failed during Probe.
type ProbeError struct {
	Check string
	Err   error
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("s3 probe %s: %v", e.Check, e.Err)
}

func (e *ProbeError) Unwrap() error { return e.Err }

// Probe verifies every capability Nipa relies on against a throwaway object
// under the prefix: the bucket, presigned create-only PUTs with a signed
// length, presigned GETs, stat and delete. The object is always removed.
func (s *Store) Probe(ctx context.Context) error {
	if err := s.checkBucket(ctx); err != nil {
		return &ProbeError{Check: ProbeBucket, Err: err}
	}
	key, data, err := s.probeObject()
	if err != nil {
		return &ProbeError{Check: ProbeUpload, Err: err}
	}
	deleted := false
	defer func() {
		if !deleted {
			_ = s.client.RemoveObject(context.WithoutCancel(ctx), s.bucket, key, minio.RemoveObjectOptions{})
		}
	}()

	target, err := s.presignPut(ctx, key, int64(len(data)), probeTTL)
	if err != nil {
		return &ProbeError{Check: ProbeUpload, Err: err}
	}
	status, err := s.put(ctx, target, append(append([]byte{}, data...), 0))
	if err != nil {
		return &ProbeError{Check: ProbeContentLength, Err: err}
	}
	if status < 400 {
		return &ProbeError{Check: ProbeContentLength, Err: fmt.Errorf("a body longer than the signed length was accepted (%d)", status)}
	}
	status, err = s.put(ctx, target, data)
	if err != nil {
		return &ProbeError{Check: ProbeUpload, Err: err}
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return &ProbeError{Check: ProbeUpload, Err: fmt.Errorf("upload answered %d", status)}
	}
	status, err = s.put(ctx, target, data)
	if err != nil {
		return &ProbeError{Check: ProbeCreateOnly, Err: err}
	}
	if status != http.StatusPreconditionFailed {
		return &ProbeError{Check: ProbeCreateOnly, Err: fmt.Errorf("a second upload answered %d, want 412: If-None-Match is not enforced", status)}
	}
	if err := s.probeDownload(ctx, key, data); err != nil {
		return &ProbeError{Check: ProbeDownload, Err: err}
	}
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return &ProbeError{Check: ProbeStat, Err: err}
	}
	if info.Size != int64(len(data)) {
		return &ProbeError{Check: ProbeStat, Err: fmt.Errorf("stat reported %d bytes, want %d", info.Size, len(data))}
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return &ProbeError{Check: ProbeDelete, Err: err}
	}
	deleted = true
	return nil
}

func (s *Store) probeObject() (string, []byte, error) {
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return "", nil, err
	}
	data := make([]byte, probeSize)
	if _, err := rand.Read(data); err != nil {
		return "", nil, err
	}
	key := ".nipa-probe-" + hex.EncodeToString(suffix)
	if s.prefix != "" {
		key = s.prefix + "/" + key
	}
	return key, data, nil
}

func (s *Store) put(ctx context.Context, target storage.UploadTarget, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	for name, value := range target.Headers {
		req.Header.Set(name, value)
	}
	res, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	return res.StatusCode, nil
}

func (s *Store) probeDownload(ctx context.Context, key string, want []byte) error {
	presigned, err := s.client.PresignedGetObject(ctx, s.bucket, key, probeTTL, nil)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, presigned.String(), nil)
	if err != nil {
		return err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download answered %d", res.StatusCode)
	}
	got, err := io.ReadAll(io.LimitReader(res.Body, int64(len(want))+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("download returned different bytes")
	}
	return nil
}

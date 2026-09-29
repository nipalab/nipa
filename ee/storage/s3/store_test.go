package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

type fakeS3 struct {
	mu       sync.Mutex
	buckets  map[string]map[string][]byte
	puts     int
	failPut  bool
	failGet  bool
	failHead bool
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	t.Helper()
	fake := &fakeS3{buckets: map[string]map[string][]byte{"test-bucket": {}}}
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeS3) serveHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	bucket := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}

	f.mu.Lock()
	objects, bucketOK := f.buckets[bucket]
	f.mu.Unlock()

	if key == "" {
		f.serveBucket(w, r, bucketOK)
		return
	}
	if !bucketOK {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}

	f.mu.Lock()
	data, exists := objects[key]
	f.mu.Unlock()

	switch r.Method {
	case http.MethodPut:
		if f.failPut {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeS3Error(w, http.StatusBadRequest, "InvalidRequest")
			return
		}
		if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") {
			body, err = decodeAWSChunked(body)
			if err != nil {
				writeS3Error(w, http.StatusBadRequest, "InvalidRequest")
				return
			}
		}
		f.mu.Lock()
		objects[key] = body
		f.puts++
		f.mu.Unlock()
		w.Header().Set("ETag", `"fake-etag"`)
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		if f.failHead {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("ETag", `"fake-etag"`)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if f.failGet {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		if !exists {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		_, _ = w.Write(data)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (f *fakeS3) serveBucket(w http.ResponseWriter, r *http.Request, bucketOK bool) {
	if r.Method == http.MethodGet && r.URL.Query().Has("location") {
		if !bucketOK {
			writeS3Error(w, http.StatusNotFound, "NoSuchBucket")
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
		return
	}
	if r.Method != http.MethodHead {
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed")
		return
	}
	if !bucketOK {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeS3Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

// decodeAWSChunked unwraps the aws-chunked transfer encoding minio-go uses
// for streaming signed PUTs: "<hex-size>[;chunk-signature=...]\r\n<data>\r\n"
// frames terminated by a zero-size frame.
func decodeAWSChunked(raw []byte) ([]byte, error) {
	var decoded []byte
	for {
		end := bytes.Index(raw, []byte("\r\n"))
		if end < 0 {
			return nil, fmt.Errorf("chunk header missing")
		}
		header := string(raw[:end])
		raw = raw[end+2:]
		if semi := strings.IndexByte(header, ';'); semi >= 0 {
			header = header[:semi]
		}
		size, err := strconv.ParseInt(header, 16, 64)
		if err != nil {
			return nil, fmt.Errorf("chunk size %q: %w", header, err)
		}
		if size == 0 {
			return decoded, nil
		}
		if int64(len(raw)) < size+2 {
			return nil, fmt.Errorf("truncated chunk")
		}
		decoded = append(decoded, raw[:size]...)
		raw = raw[size+2:]
	}
}

func testConfig(server *httptest.Server) Config {
	return Config{
		Endpoint:        server.URL,
		Bucket:          "test-bucket",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
	}
}

func newTestStore(t *testing.T) (*Store, *fakeS3) {
	t.Helper()
	fake, server := newFakeS3(t)
	store, err := New(context.Background(), testConfig(server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store, fake
}

func TestNew_RequiresBucket(t *testing.T) {
	_, err := New(context.Background(), Config{Endpoint: "http://127.0.0.1:1"})
	require.ErrorContains(t, err, "bucket is required")
}

func TestNew_RequiresCompleteCredentialPair(t *testing.T) {
	_, err := New(context.Background(), Config{Bucket: "b", AccessKeyID: "only-key"})
	require.ErrorContains(t, err, "must be set together")

	_, err = New(context.Background(), Config{Bucket: "b", SecretAccessKey: "only-secret"})
	require.ErrorContains(t, err, "must be set together")
}

func TestNew_BucketMissing(t *testing.T) {
	_, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Bucket = "absent-bucket"
	_, err := New(context.Background(), cfg)
	require.ErrorContains(t, err, `bucket "absent-bucket" does not exist`)
}

func TestNew_InvalidEndpointScheme(t *testing.T) {
	_, err := New(context.Background(), Config{Endpoint: "ftp://minio:9000", Bucket: "b"})
	require.ErrorContains(t, err, "unsupported endpoint scheme")
}

func TestStore_PutGetRoundTrip(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("hello s3 storage")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, hash, data))

	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStore_PutIsIdempotent(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()

	data := []byte("same content")
	hash := chunker.Sum(data)
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Put(ctx, hash, data))
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.Equal(t, 1, fake.puts)
}

func TestStore_Exists(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	present := chunker.Sum([]byte("present"))
	missing := chunker.Sum([]byte("missing"))
	require.NoError(t, store.Put(ctx, present, []byte("present")))

	ok, err := store.Exists(ctx, present)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.Exists(ctx, missing)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStore_Size(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("size me")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, hash, data))

	size, err := store.Size(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)

	_, err = store.Size(ctx, chunker.Sum([]byte("missing")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestStore_GetMissingReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Get(context.Background(), chunker.Sum([]byte("nope")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestStore_KeyLayout(t *testing.T) {
	fake, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Prefix = "tenant-a/chunks"
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	data := []byte("sharded")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(context.Background(), hash, data))

	want := "tenant-a/chunks/" + hash.String()[:2] + "/" + hash.String()[2:]
	fake.mu.Lock()
	defer fake.mu.Unlock()
	_, ok := fake.buckets["test-bucket"][want]
	require.True(t, ok)
	require.Equal(t, want, store.key(hash))
}

func TestStore_PutError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failPut = true

	err := store.Put(context.Background(), chunker.Sum([]byte("x")), []byte("x"))
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestStore_HeadError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failHead = true
	ctx := context.Background()
	hash := chunker.Sum([]byte("x"))

	_, err := store.Exists(ctx, hash)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))

	_, err = store.Size(ctx, hash)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))

	require.Error(t, store.Put(ctx, hash, []byte("x")))
}

func TestStore_GetError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failGet = true

	_, err := store.Get(context.Background(), chunker.Sum([]byte("x")))
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestStore_Close(t *testing.T) {
	store, _ := newTestStore(t)
	require.NoError(t, store.Close())
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		secure bool
		err    bool
	}{
		{in: "", want: "s3.amazonaws.com", secure: true},
		{in: "minio:9000", want: "minio:9000", secure: true},
		{in: "minio:9000/", want: "minio:9000", secure: true},
		{in: "http://minio:9000", want: "minio:9000", secure: false},
		{in: "https://s3.example.com", want: "s3.example.com", secure: true},
		{in: "ftp://minio:9000", err: true},
		{in: "http://", err: true},
		{in: "://bad", err: true},
	}
	for _, tt := range tests {
		endpoint, secure, err := parseEndpoint(tt.in)
		if tt.err {
			require.Error(t, err, tt.in)
			continue
		}
		require.NoError(t, err, tt.in)
		require.Equal(t, tt.want, endpoint, tt.in)
		require.Equal(t, tt.secure, secure, tt.in)
	}
}

func TestNormalizePrefix(t *testing.T) {
	require.Equal(t, "", normalizePrefix(""))
	require.Equal(t, "", normalizePrefix("/"))
	require.Equal(t, "chunks", normalizePrefix("chunks"))
	require.Equal(t, "chunks", normalizePrefix("/chunks/"))
	require.Equal(t, "a/b", normalizePrefix(" a/b "))
}

func TestCredentialsFor_StaticPair(t *testing.T) {
	creds, err := credentialsFor(Config{AccessKeyID: "key", SecretAccessKey: "secret"})
	require.NoError(t, err)
	require.NotNil(t, creds)
}

func TestCredentialsFor_ChainWithoutStaticKeys(t *testing.T) {
	creds, err := credentialsFor(Config{})
	require.NoError(t, err)
	require.NotNil(t, creds)
}

func TestIntegration_S3Compatible(t *testing.T) {
	endpoint := os.Getenv("NIPA_S3_TEST_ENDPOINT")
	bucket := os.Getenv("NIPA_S3_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set NIPA_S3_TEST_ENDPOINT and NIPA_S3_TEST_BUCKET to run against a real S3-compatible server")
	}

	cfg := Config{
		Endpoint:        endpoint,
		Region:          os.Getenv("NIPA_S3_TEST_REGION"),
		Bucket:          bucket,
		Prefix:          os.Getenv("NIPA_S3_TEST_PREFIX"),
		AccessKeyID:     os.Getenv("NIPA_S3_TEST_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("NIPA_S3_TEST_SECRET_ACCESS_KEY"),
	}
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	data := []byte("nipa s3 integration " + strings.Repeat("x", 1024))
	hash := chunker.Sum(data)

	exists, err := store.Exists(ctx, hash)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, store.Put(ctx, hash, data))
	t.Cleanup(func() {
		_ = store.client.RemoveObject(context.Background(), store.bucket, store.key(hash), minio.RemoveObjectOptions{})
	})

	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	size, err := store.Size(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)

	exists, err = store.Exists(ctx, hash)
	require.NoError(t, err)
	require.True(t, exists)
}

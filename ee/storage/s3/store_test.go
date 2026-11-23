package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// testProject owns the chunks of these tests; keys live under p/<base36>/.
const testProject snow.ID = 7

type fakeS3 struct {
	mu       sync.Mutex
	buckets  map[string]map[string][]byte
	puts     int
	failPut  atomic.Bool
	failGet  atomic.Bool
	failHead atomic.Bool
	// ignoreIfNoneMatch overwrites existing objects like a backend without
	// conditional create; wantLength refuses PUT bodies of any other size,
	// standing in for a signed Content-Length.
	ignoreIfNoneMatch atomic.Bool
	wantLength        atomic.Int64
	failDelete        atomic.Bool
	corruptGet        atomic.Bool
	failList          atomic.Bool
	// truncateGet announces the full Content-Length but sends half the body.
	truncateGet atomic.Bool
	gets        atomic.Int64
	heads       atomic.Int64
	// lastRange is the Range header of the last GET.
	lastRange atomic.Value
	listTime  time.Time
	// payloadSHA is the X-Amz-Content-Sha256 header of the last PUT.
	payloadSHA atomic.Value
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	t.Helper()
	fake := &fakeS3{buckets: map[string]map[string][]byte{"test-bucket": {}}, listTime: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
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
		if f.failPut.Load() {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		if want := f.wantLength.Load(); want > 0 && r.ContentLength != want {
			writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch")
			return
		}
		if r.Header.Get("If-None-Match") == "*" && exists && !f.ignoreIfNoneMatch.Load() {
			writeS3Error(w, http.StatusPreconditionFailed, "PreconditionFailed")
			return
		}
		f.payloadSHA.Store(r.Header.Get("X-Amz-Content-Sha256"))
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
		f.heads.Add(1)
		if f.failHead.Load() {
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
		f.gets.Add(1)
		if f.failGet.Load() {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		if !exists {
			writeS3Error(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		f.lastRange.Store(r.Header.Get("Range"))
		if r.Header.Get("Range") != "" {
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		if f.corruptGet.Load() {
			data = bytes.Repeat([]byte{'x'}, len(data))
		}
		if f.truncateGet.Load() {
			data = data[:len(data)/2]
		}
		_, _ = w.Write(data)
	case http.MethodDelete:
		if f.failDelete.Load() {
			writeS3Error(w, http.StatusForbidden, "AccessDenied")
			return
		}
		f.mu.Lock()
		delete(objects, key)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
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
	if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
		f.serveList(w, r, bucketOK)
		return
	}
	if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
		f.serveMultiDelete(w, r)
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

// serveList answers ListObjectsV2 in one page, keys sorted.
func (f *fakeS3) serveList(w http.ResponseWriter, r *http.Request, bucketOK bool) {
	if !bucketOK {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket")
		return
	}
	if f.failList.Load() {
		writeS3Error(w, http.StatusForbidden, "AccessDenied")
		return
	}
	prefix := r.URL.Query().Get("prefix")
	f.mu.Lock()
	var keys []string
	sizes := map[string]int{}
	for key, data := range f.buckets["test-bucket"] {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
			sizes[key] = len(data)
		}
	}
	f.mu.Unlock()
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>test-bucket</Name>`)
	fmt.Fprintf(&body, "<Prefix>%s</Prefix><KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>", prefix, len(keys))
	for _, key := range keys {
		fmt.Fprintf(&body, "<Contents><Key>%s</Key><LastModified>%s</LastModified><ETag>&quot;e&quot;</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>",
			key, f.listTime.UTC().Format("2006-01-02T15:04:05.000Z"), sizes[key])
	}
	body.WriteString("</ListBucketResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, body.String())
}

// serveMultiDelete answers DeleteObjects: every listed key is removed, or
// reported as an AccessDenied error per key when deletes fail.
func (f *fakeS3) serveMultiDelete(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Objects []struct {
			Key string `xml:"Key"`
		} `xml:"Object"`
	}
	if err := xml.NewDecoder(r.Body).Decode(&request); err != nil {
		writeS3Error(w, http.StatusBadRequest, "MalformedXML")
		return
	}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	f.mu.Lock()
	for _, object := range request.Objects {
		if f.failDelete.Load() {
			fmt.Fprintf(&body, "<Error><Key>%s</Key><Code>AccessDenied</Code><Message>denied</Message></Error>", object.Key)
			continue
		}
		delete(f.buckets["test-bucket"], object.Key)
	}
	f.mu.Unlock()
	body.WriteString("</DeleteResult>")
	w.Header().Set("Content-Type", "application/xml")
	_, _ = io.WriteString(w, body.String())
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
	require.NoError(t, store.Put(ctx, testProject, hash, data))

	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStore_PutIsIdempotent(t *testing.T) {
	store, fake := newTestStore(t)
	ctx := context.Background()

	data := []byte("same content")
	hash := chunker.Sum(data)
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Put(ctx, testProject, hash, data))
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
	require.NoError(t, store.Put(ctx, testProject, present, []byte("present")))

	ok, err := store.Exists(ctx, testProject, present)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.Exists(ctx, testProject, missing)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStore_Size(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("size me")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))

	size, err := store.Size(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)

	_, err = store.Size(ctx, testProject, chunker.Sum([]byte("missing")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestStore_GetMissingReturnsNotFound(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Get(context.Background(), testProject, chunker.Sum([]byte("nope")))
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
	require.NoError(t, store.Put(context.Background(), testProject, hash, data))

	want := "tenant-a/chunks/p/" + testProject.Base36() + "/" + hash.String()[:2] + "/" + hash.String()[2:]
	fake.mu.Lock()
	defer fake.mu.Unlock()
	_, ok := fake.buckets["test-bucket"][want]
	require.True(t, ok)
	require.Equal(t, want, store.key(testProject, hash))
}

func TestStore_PutError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failPut.Store(true)

	err := store.Put(context.Background(), testProject, chunker.Sum([]byte("x")), []byte("x"))
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestStore_HeadError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failHead.Store(true)
	ctx := context.Background()
	hash := chunker.Sum([]byte("x"))

	_, err := store.Exists(ctx, testProject, hash)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))

	_, err = store.Size(ctx, testProject, hash)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))

	require.Error(t, store.Put(ctx, testProject, hash, []byte("x")))
}

func TestStore_GetError(t *testing.T) {
	store, fake := newTestStore(t)
	fake.failGet.Store(true)

	_, err := store.Get(context.Background(), testProject, chunker.Sum([]byte("x")))
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestStore_Close(t *testing.T) {
	store, _ := newTestStore(t)
	require.NoError(t, store.Close())
}

func TestStore_PresignDownload(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("presign me")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))

	raw, err := store.PresignDownload(ctx, testProject, hash, time.Hour)
	require.NoError(t, err)

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.True(t, parsed.IsAbs())
	require.Equal(t, "/test-bucket/p/"+testProject.Base36()+"/"+hash.String()[:2]+"/"+hash.String()[2:], parsed.Path)
	require.Equal(t, "3600", parsed.Query().Get("X-Amz-Expires"))
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))

	res, err := http.Get(raw)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, data, body)
}

func TestStore_PresignDownloadClampsTTL(t *testing.T) {
	store, _ := newTestStore(t)

	raw, err := store.PresignDownload(context.Background(), testProject, chunker.Sum([]byte("x")), 30*24*time.Hour)
	require.NoError(t, err)

	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, "604800", parsed.Query().Get("X-Amz-Expires"))
}

func TestStore_PresignDownloadInvalidTTL(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.PresignDownload(context.Background(), testProject, chunker.Sum([]byte("x")), 0)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestStore_PresignUpload(t *testing.T) {
	store, _ := newTestStore(t)
	data := []byte("upload me")
	hash := chunker.Sum(data)

	target, err := store.PresignUpload(context.Background(), testProject, hash, int64(len(data)), time.Hour)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"If-None-Match": "*"}, target.Headers)

	parsed, err := url.Parse(target.URL)
	require.NoError(t, err)
	require.True(t, parsed.IsAbs())
	require.Equal(t, "/test-bucket/p/"+testProject.Base36()+"/"+hash.String()[:2]+"/"+hash.String()[2:], parsed.Path, "uploads target the project's key")
	signed := strings.Split(parsed.Query().Get("X-Amz-SignedHeaders"), ";")
	require.Contains(t, signed, "if-none-match", "create-only is part of the signature")
	require.Contains(t, signed, "content-length", "the exact size is part of the signature")
	require.Equal(t, "3600", parsed.Query().Get("X-Amz-Expires"))
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
}

func TestStore_PresignUploadClampsTTL(t *testing.T) {
	store, _ := newTestStore(t)

	target, err := store.PresignUpload(context.Background(), testProject, chunker.Sum([]byte("x")), 1, 30*24*time.Hour)
	require.NoError(t, err)

	parsed, err := url.Parse(target.URL)
	require.NoError(t, err)
	require.Equal(t, "604800", parsed.Query().Get("X-Amz-Expires"))
}

func TestStore_PresignUploadInvalidInput(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.PresignUpload(context.Background(), testProject, chunker.Sum([]byte("x")), 0, time.Hour)
	require.Error(t, err)
	_, err = store.PresignUpload(context.Background(), testProject, chunker.Sum([]byte("x")), 1, 0)
	require.Error(t, err)
}

func TestStore_PresignUploadIsCreateOnly(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("direct upload payload")
	hash := chunker.Sum(data)
	target, err := store.PresignUpload(ctx, testProject, hash, int64(len(data)), time.Hour)
	require.NoError(t, err)

	first := putUploadTarget(t, target, data)
	defer func() { _ = first.Body.Close() }()
	require.Equal(t, http.StatusOK, first.StatusCode)

	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	replay := putUploadTarget(t, target, []byte("different bytes, same target"))
	defer func() { _ = replay.Body.Close() }()
	require.Equal(t, http.StatusPreconditionFailed, replay.StatusCode, "a replayed target cannot overwrite the object")

	got, err = store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStore_DeleteChunk(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	data := []byte("delete me")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))
	require.NoError(t, store.DeleteChunk(ctx, testProject, hash))

	exists, err := store.Exists(ctx, testProject, hash)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, store.DeleteChunk(ctx, testProject, chunker.Sum([]byte("absent"))))
}

func putUploadTarget(t *testing.T, target storage.UploadTarget, data []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, target.URL, bytes.NewReader(data))
	require.NoError(t, err)
	for name, value := range target.Headers {
		request.Header.Set(name, value)
	}
	res, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return res
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

	exists, err := store.Exists(ctx, testProject, hash)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, store.Put(ctx, testProject, hash, data))
	t.Cleanup(func() {
		_ = store.client.RemoveObject(context.Background(), store.bucket, store.key(testProject, hash), minio.RemoveObjectOptions{})
	})

	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	size, err := store.Size(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)

	exists, err = store.Exists(ctx, testProject, hash)
	require.NoError(t, err)
	require.True(t, exists)

	raw, err := store.PresignDownload(ctx, testProject, hash, time.Hour)
	require.NoError(t, err)
	res, err := http.Get(raw)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, http.StatusOK, res.StatusCode)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, data, body)
}

// TestIntegration_S3CreateOnlyUploads checks, against a real S3-compatible
// backend (AWS S3, MinIO, Cloudflare R2), that a presigned PUT is create-only:
// the backend must enforce the signed If-None-Match and Content-Length. It
// shares the NIPA_S3_TEST_* variables with TestIntegration_S3Compatible, e.g.
// for R2:
//
//	NIPA_S3_TEST_ENDPOINT=https://<account>.r2.cloudflarestorage.com
//	NIPA_S3_TEST_BUCKET=<bucket> NIPA_S3_TEST_REGION=auto
//	NIPA_S3_TEST_ACCESS_KEY_ID=... NIPA_S3_TEST_SECRET_ACCESS_KEY=...
func TestIntegration_S3CreateOnlyUploads(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()

	unique := fmt.Sprintf("nipa create-only %d", time.Now().UnixNano())
	data := []byte(unique + strings.Repeat("y", 512))
	hash := chunker.Sum(data)
	t.Cleanup(func() { _ = store.DeleteChunk(context.Background(), testProject, hash) })

	target, err := store.PresignUpload(ctx, testProject, hash, int64(len(data)), time.Hour)
	require.NoError(t, err)

	wrongSize := putUploadTarget(t, target, append(append([]byte{}, data...), 'x'))
	defer func() { _ = wrongSize.Body.Close() }()
	require.GreaterOrEqual(t, wrongSize.StatusCode, 400, "the signed content length is enforced")

	request, err := http.NewRequest(http.MethodPut, target.URL, bytes.NewReader(data))
	require.NoError(t, err)
	unconditional, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer func() { _ = unconditional.Body.Close() }()
	require.GreaterOrEqual(t, unconditional.StatusCode, 400, "dropping the signed If-None-Match header is refused")

	exists, err := store.Exists(ctx, testProject, hash)
	require.NoError(t, err)
	require.False(t, exists, "rejected writes leave nothing behind")

	first := putUploadTarget(t, target, data)
	defer func() { _ = first.Body.Close() }()
	require.Contains(t, []int{http.StatusOK, http.StatusNoContent}, first.StatusCode)
	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	replay := putUploadTarget(t, target, bytes.Repeat([]byte("z"), len(data)))
	defer func() { _ = replay.Body.Close() }()
	require.Equal(t, http.StatusPreconditionFailed, replay.StatusCode, "a replayed target must not overwrite")

	fresh, err := store.PresignUpload(ctx, testProject, hash, int64(len(data)), time.Hour)
	require.NoError(t, err)
	again := putUploadTarget(t, fresh, bytes.Repeat([]byte("z"), len(data)))
	defer func() { _ = again.Body.Close() }()
	require.Equal(t, http.StatusPreconditionFailed, again.StatusCode, "a new target for an existing key must not overwrite either")

	got, err = store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, data, got, "the original bytes survive every overwrite attempt")
}

func newIntegrationStore(t *testing.T) *Store {
	t.Helper()
	endpoint := os.Getenv("NIPA_S3_TEST_ENDPOINT")
	bucket := os.Getenv("NIPA_S3_TEST_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("set NIPA_S3_TEST_ENDPOINT and NIPA_S3_TEST_BUCKET to run against a real S3-compatible server")
	}
	store, err := New(context.Background(), Config{
		Endpoint:        endpoint,
		Region:          os.Getenv("NIPA_S3_TEST_REGION"),
		Bucket:          bucket,
		Prefix:          os.Getenv("NIPA_S3_TEST_PREFIX"),
		AccessKeyID:     os.Getenv("NIPA_S3_TEST_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("NIPA_S3_TEST_SECRET_ACCESS_KEY"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestIntegration_S3Probe runs Probe against the real backend configured by
// the NIPA_S3_TEST_* variables.
func TestIntegration_S3Probe(t *testing.T) {
	store := newIntegrationStore(t)
	require.NoError(t, store.Probe(context.Background()))
}

func TestStore_ListChunks(t *testing.T) {
	fake, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Prefix = "tenant"
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	a, b := []byte("first chunk"), []byte("second chunk, longer")
	const other snow.ID = 8
	require.NoError(t, store.Put(ctx, testProject, chunker.Sum(a), a))
	require.NoError(t, store.Put(ctx, testProject, chunker.Sum(b), b))
	require.NoError(t, store.Put(ctx, other, chunker.Sum(a), a))
	hex := chunker.Sum(a).String()
	fake.mu.Lock()
	fake.buckets["test-bucket"]["tenant/probe.txt"] = []byte("not a chunk")
	fake.buckets["test-bucket"]["tenant/zz/not-hex"] = []byte("x")
	fake.buckets["test-bucket"]["tenant/"+hex[:2]+"/"+hex[2:]] = a // the old global layout
	fake.buckets["test-bucket"]["other/ab/cd"] = []byte("outside the prefix")
	fake.mu.Unlock()

	type key struct {
		project snow.ID
		hash    domain.Hash
	}
	found := map[key]StoredChunk{}
	require.NoError(t, store.ListChunks(ctx, func(chunk StoredChunk) error {
		found[key{chunk.ProjectID, chunk.Hash}] = chunk
		return nil
	}))
	require.Len(t, found, 3)
	require.Equal(t, int64(len(a)), found[key{testProject, chunker.Sum(a)}].SizeBytes)
	require.Equal(t, int64(len(b)), found[key{testProject, chunker.Sum(b)}].SizeBytes)
	require.Equal(t, int64(len(a)), found[key{other, chunker.Sum(a)}].SizeBytes, "the same hash in another project is another object")
	require.Equal(t, fake.listTime, found[key{testProject, chunker.Sum(a)}].LastModified.UTC())

	stop := errors.New("stop")
	require.ErrorIs(t, store.ListChunks(ctx, func(StoredChunk) error { return stop }), stop)

	fake.failList.Store(true)
	require.Error(t, store.ListChunks(ctx, func(StoredChunk) error { return nil }))
}

func TestStore_DeleteProject(t *testing.T) {
	fake, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Prefix = "tenant"
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	const other snow.ID = 8
	// testProject ("7") and lookalike ("71" in base36) share a prefix; the trailing
	// slash keeps the delete inside project 7.
	const lookalike snow.ID = 7*36 + 1
	var hashes []domain.Hash
	for i := 0; i < 5; i++ {
		data := []byte(fmt.Sprintf("chunk %d", i))
		hash := chunker.Sum(data)
		hashes = append(hashes, hash)
		require.NoError(t, store.Put(ctx, testProject, hash, data))
		require.NoError(t, store.Put(ctx, other, hash, data))
	}
	require.NoError(t, store.Put(ctx, lookalike, hashes[0], []byte("chunk 0")))

	require.NoError(t, store.DeleteProject(ctx, testProject))
	for _, hash := range hashes {
		exists, err := store.Exists(ctx, testProject, hash)
		require.NoError(t, err)
		require.False(t, exists)
		exists, err = store.Exists(ctx, other, hash)
		require.NoError(t, err)
		require.True(t, exists, "another project's objects stay")
	}
	exists, err := store.Exists(ctx, lookalike, hashes[0])
	require.NoError(t, err)
	require.True(t, exists, "a project whose id shares the prefix stays")

	require.NoError(t, store.DeleteProject(ctx, testProject), "an empty prefix is not an error")

	fake.failDelete.Store(true)
	require.Error(t, store.DeleteProject(ctx, other))
	fake.failDelete.Store(false)
	fake.failList.Store(true)
	require.Error(t, store.DeleteProject(ctx, other))
}

func TestStore_Packs(t *testing.T) {
	fake, server := newFakeS3(t)
	cfg := testConfig(server)
	cfg.Prefix = "tenant"
	store, err := New(context.Background(), cfg)
	require.NoError(t, err)
	ctx := context.Background()

	const packID snow.ID = 99
	key := storage.PackKey(testProject, packID)
	require.Equal(t, "packs/7/2r", key)
	body := []byte("NPK1-chunk-one-chunk-two-index")
	require.NoError(t, store.PutPack(ctx, key, bytes.NewReader(body), int64(len(body))))
	fake.mu.Lock()
	stored := fake.buckets["test-bucket"]["tenant/"+key]
	fake.mu.Unlock()
	require.Equal(t, body, stored, "the pack lands under the store prefix")

	got, err := store.GetRange(ctx, key, 5, 9)
	require.NoError(t, err)
	require.Equal(t, []byte("chunk-one"), got)
	require.Equal(t, "bytes=5-13", fake.lastRange.Load())

	_, err = store.GetRange(ctx, key, int64(len(body))-2, 10)
	require.Error(t, err, "a range past the end is an error, not a short read")
	_, err = store.GetRange(ctx, key, -1, 3)
	require.Error(t, err)
	_, err = store.GetRange(ctx, key, 0, 0)
	require.Error(t, err)
	_, err = store.GetRange(ctx, storage.PackKey(testProject, 100), 0, 3)
	require.True(t, domain.IsErrorNotFound(err), "a missing pack is NotFound")
	fake.failGet.Store(true)
	_, err = store.GetRange(ctx, key, 0, 3)
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
	fake.failGet.Store(false)

	presigned, err := store.PresignPackDownload(ctx, key, 8*24*time.Hour)
	require.NoError(t, err)
	u, err := url.Parse(presigned)
	require.NoError(t, err)
	require.Equal(t, "/test-bucket/tenant/"+key, u.Path)
	require.Equal(t, "604800", u.Query().Get("X-Amz-Expires"), "clamped to SigV4's 7 days")
	request, err := http.NewRequest(http.MethodGet, presigned, nil)
	require.NoError(t, err)
	request.Header.Set("Range", "bytes=15-23")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	ranged, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusPartialContent, response.StatusCode, "a client reads part of the pack with a Range header")
	require.Equal(t, []byte("chunk-two"), ranged)
	_, err = store.PresignPackDownload(ctx, key, -time.Second)
	require.Error(t, err)

	// DeleteProject takes the pack prefix too.
	require.NoError(t, store.PutPack(ctx, storage.PackKey(8, 1), bytes.NewReader(body), int64(len(body))))
	require.NoError(t, store.DeleteProject(ctx, testProject))
	_, err = store.GetRange(ctx, key, 0, 3)
	require.True(t, domain.IsErrorNotFound(err))
	got, err = store.GetRange(ctx, storage.PackKey(8, 1), 0, 4)
	require.NoError(t, err, "another project's packs stay")
	require.Equal(t, []byte("NPK1"), got)

	require.NoError(t, store.DeletePack(ctx, storage.PackKey(8, 1)))
	require.NoError(t, store.DeletePack(ctx, storage.PackKey(8, 1)), "a missing pack is not an error")
	fake.failDelete.Store(true)
	require.Error(t, store.DeletePack(ctx, storage.PackKey(8, 1)))
	fake.failDelete.Store(false)

	fake.failPut.Store(true)
	require.Error(t, store.PutPack(ctx, key, bytes.NewReader(body), int64(len(body))))
}

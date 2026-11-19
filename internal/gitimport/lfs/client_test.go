package lfs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLFS struct {
	mu           sync.Mutex
	objects      map[string][]byte
	user, pass   string
	batchFail    int
	downloadFail int
	corrupt      bool
	truncate     bool
	transfer     string
	relative     bool
	omit         bool
	noAction     bool
	hrefBase     string
	batches      int
	downloads    int
	downloadAuth []string
	actionHeader string
	srv          *httptest.Server
}

func newFakeLFS(t *testing.T, contents ...string) *fakeLFS {
	f := &fakeLFS{objects: map[string][]byte{}}
	for _, c := range contents {
		f.objects[oidOf(c)] = []byte(c)
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLFS) endpoint() string {
	return f.srv.URL + "/org/repo.git/info/lfs"
}

func (f *fakeLFS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/objects/batch") {
		f.batches++
		if f.user != "" {
			u, p, ok := r.BasicAuth()
			if !ok || u != f.user || p != f.pass {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		if f.batchFail > 0 {
			f.batchFail--
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"message":"try later"}`))
			return
		}
		if r.Header.Get("Content-Type") != mediaType {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		var req batchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Operation != "download" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp := batchResponse{Transfer: f.transfer}
		if !f.omit {
			for _, o := range req.Objects {
				out := batchObject{OID: o.OID, Size: o.Size}
				switch {
				case f.objects[o.OID] == nil:
					out.Error = &batchError{Code: 404, Message: "Object does not exist"}
				case f.noAction:
				default:
					base := f.srv.URL
					if f.hrefBase != "" {
						base = f.hrefBase
					}
					href := base + "/objects/" + o.OID
					if f.relative {
						href = "../../../objects/" + o.OID
					}
					out.Actions = map[string]batchAction{"download": {Href: href, Header: map[string]string{"X-Action": "yes"}}}
				}
				resp.Objects = append(resp.Objects, out)
			}
		}
		w.Header().Set("Content-Type", mediaType)
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/objects/") {
		f.downloads++
		f.downloadAuth = append(f.downloadAuth, r.Header.Get("Authorization"))
		f.actionHeader = r.Header.Get("X-Action")
		if f.downloadFail > 0 {
			f.downloadFail--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		data, ok := f.objects[strings.TrimPrefix(r.URL.Path, "/objects/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		out := append([]byte(nil), data...)
		if f.corrupt {
			out[0] ^= 0xff
		}
		if f.truncate {
			out = out[:len(out)-1]
		}
		_, _ = w.Write(out)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

type fakeHelper struct {
	creds    Credentials
	err      error
	filled   []string
	approved int
	rejected int
}

func (h *fakeHelper) Fill(_ context.Context, u *url.URL) (Credentials, error) {
	h.filled = append(h.filled, u.String())
	return h.creds, h.err
}

func (h *fakeHelper) Approve(context.Context, *url.URL, Credentials) error {
	h.approved++
	return nil
}

func (h *fakeHelper) Reject(context.Context, *url.URL, Credentials) error {
	h.rejected++
	return nil
}

func newTestClient(t *testing.T, endpoint string, helper CredentialHelper) *Client {
	t.Helper()
	opts := ClientOptions{Credentials: helper, TempDir: t.TempDir() + "/tmp", Backoff: -1}
	c, err := NewClient(endpoint, opts)
	require.NoError(t, err)
	return c
}

func fetchAll(t *testing.T, c *Client, content string) (string, error) {
	t.Helper()
	obj, err := c.Fetch(context.Background(), Pointer{OID: oidOf(content), Size: int64(len(content))})
	if err != nil {
		return "", err
	}
	defer func() { require.NoError(t, obj.Close()) }()
	data, err := io.ReadAll(obj)
	require.NoError(t, err)
	return string(data), nil
}

func TestClientFetchOK(t *testing.T) {
	f := newFakeLFS(t, "big asset")
	c := newTestClient(t, f.endpoint()+"/", nil)
	assert.Equal(t, f.endpoint(), c.Endpoint())
	got, err := fetchAll(t, c, "big asset")
	require.NoError(t, err)
	assert.Equal(t, "big asset", got)
	assert.Equal(t, "yes", f.actionHeader)
	assert.Equal(t, []string{""}, f.downloadAuth)
	entries, err := os.ReadDir(c.tempDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the spooled object is removed on close")
}

func TestClientCredentialRetryOn401(t *testing.T) {
	f := newFakeLFS(t, "secret asset")
	f.user, f.pass = "ann", "pw"
	helper := &fakeHelper{creds: Credentials{Username: "ann", Password: "pw"}}
	c := newTestClient(t, f.endpoint(), helper)
	got, err := fetchAll(t, c, "secret asset")
	require.NoError(t, err)
	assert.Equal(t, "secret asset", got)
	assert.Equal(t, []string{f.endpoint()}, helper.filled)
	assert.Equal(t, 1, helper.approved)
	assert.Equal(t, 2, f.batches)
	require.Len(t, f.downloadAuth, 1)
	assert.Equal(t, "Basic YW5uOnB3", f.downloadAuth[0], "credentials go to the same origin")

	_, err = fetchAll(t, c, "secret asset")
	require.NoError(t, err)
	assert.Len(t, helper.filled, 1)
	assert.Equal(t, 1, helper.approved)

	helper = &fakeHelper{creds: Credentials{Username: "ann", Password: "wrong"}}
	c = newTestClient(t, f.endpoint(), helper)
	_, err = fetchAll(t, c, "secret asset")
	var se *ServerError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusUnauthorized, se.Status)
	assert.Equal(t, 1, helper.rejected)

	c = newTestClient(t, f.endpoint(), &fakeHelper{err: ErrNoCredentials})
	_, err = fetchAll(t, c, "secret asset")
	require.ErrorAs(t, err, &se)
	assert.Contains(t, err.Error(), "authentication required")

	c = newTestClient(t, f.endpoint(), nil)
	_, err = fetchAll(t, c, "secret asset")
	require.ErrorAs(t, err, &se)

	u, _ := url.Parse(f.endpoint())
	u.User = url.UserPassword("ann", "pw")
	c = newTestClient(t, u.String(), nil)
	assert.NotContains(t, c.Endpoint(), "pw")
	got, err = fetchAll(t, c, "secret asset")
	require.NoError(t, err)
	assert.Equal(t, "secret asset", got)
}

func TestClientNeverSendsCredentialsToAnotherHost(t *testing.T) {
	storage := newFakeLFS(t, "cdn asset")
	f := newFakeLFS(t, "cdn asset")
	f.user, f.pass = "ann", "pw"
	f.hrefBase = storage.srv.URL
	c := newTestClient(t, f.endpoint(), &fakeHelper{creds: Credentials{Username: "ann", Password: "pw"}})
	_, err := fetchAll(t, c, "cdn asset")
	require.NoError(t, err)
	assert.Equal(t, []string{""}, storage.downloadAuth)
	assert.Empty(t, f.downloadAuth)
}

func TestClientCorruptObject(t *testing.T) {
	f := newFakeLFS(t, "pristine")
	f.corrupt = true
	c := newTestClient(t, f.endpoint(), nil)
	_, err := fetchAll(t, c, "pristine")
	require.ErrorIs(t, err, ErrCorrupt)
	assert.Equal(t, 3, f.downloads, "a corrupt download is retried")

	f.corrupt, f.truncate, f.downloads = false, true, 0
	_, err = fetchAll(t, c, "pristine")
	require.ErrorIs(t, err, ErrCorrupt)
	assert.Contains(t, err.Error(), "the pointer says")
}

func TestClientMissingObject(t *testing.T) {
	f := newFakeLFS(t, "here")
	c := newTestClient(t, f.endpoint(), nil)
	_, err := fetchAll(t, c, "not here")
	var se *ServerError
	require.ErrorAs(t, err, &se)
	assert.True(t, se.NotFound())
	assert.Equal(t, oidOf("not here"), se.OID)
	assert.Contains(t, err.Error(), "Object does not exist")
	assert.Equal(t, 1, f.batches, "a missing object is not retried")

	f.omit = true
	_, err = fetchAll(t, c, "here")
	require.ErrorContains(t, err, "missing from the batch response")
	f.omit, f.noAction = false, true
	_, err = fetchAll(t, c, "here")
	require.ErrorContains(t, err, "no download action")
	f.noAction = false

	delete(f.objects, oidOf("here"))
	f.objects[oidOf("ghost")] = []byte("ghost")
	f.relative = true
	_, err = fetchAll(t, c, "ghost")
	require.NoError(t, err)
}

func TestClientTransferErrorsAreRetried(t *testing.T) {
	f := newFakeLFS(t, "flaky")
	f.batchFail, f.downloadFail = 1, 1
	c := newTestClient(t, f.endpoint(), nil)
	got, err := fetchAll(t, c, "flaky")
	require.NoError(t, err)
	assert.Equal(t, "flaky", got)
	assert.Equal(t, 3, f.batches)
	assert.Equal(t, 2, f.downloads)

	f.batchFail = 5
	_, err = fetchAll(t, c, "flaky")
	require.ErrorContains(t, err, "try later")

	f.downloadFail = 5
	f.batchFail = 0
	_, err = fetchAll(t, c, "flaky")
	require.ErrorContains(t, err, "HTTP 502")
}

func TestClientProtocolErrors(t *testing.T) {
	f := newFakeLFS(t, "x")
	f.transfer = "tus"
	c := newTestClient(t, f.endpoint(), nil)
	_, err := fetchAll(t, c, "x")
	require.ErrorContains(t, err, "unsupported transfer")

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forbidden/objects/batch" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("not json"))
	}))
	defer bad.Close()
	c = newTestClient(t, bad.URL+"/garbage", nil)
	_, err = fetchAll(t, c, "x")
	require.ErrorContains(t, err, "read lfs batch response")
	c = newTestClient(t, bad.URL+"/forbidden", nil)
	_, err = fetchAll(t, c, "x")
	require.ErrorContains(t, err, "lfs server: HTTP 403")

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	c = newTestClient(t, closed.URL, nil)
	_, err = fetchAll(t, c, "x")
	require.ErrorContains(t, err, "download lfs object")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.transfer = ""
	c = newTestClient(t, f.endpoint(), nil)
	_, err = c.Fetch(ctx, Pointer{OID: oidOf("x"), Size: 1})
	require.ErrorIs(t, err, context.Canceled)

	for _, endpoint := range []string{"ftp://example.com/x", "https://", "::"} {
		_, err := NewClient(endpoint, ClientOptions{})
		require.Error(t, err, endpoint)
	}
	_, err = NewClient("https://ann:pw@example.com/x", ClientOptions{TempDir: "/dev/null/x"})
	require.NoError(t, err)
	assert.NotContains(t, (&ServerError{Status: 500}).Error(), "pw")
}

func TestClientSpoolErrors(t *testing.T) {
	f := newFakeLFS(t, "spool")
	c, err := NewClient(f.endpoint(), ClientOptions{TempDir: "/dev/null/sub", Backoff: -1})
	require.NoError(t, err)
	_, err = fetchAll(t, c, "spool")
	require.Error(t, err)
	assert.Equal(t, 1, f.downloads)
}

func TestSleepAndRedact(t *testing.T) {
	require.NoError(t, sleep(context.Background(), 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleep(ctx, 1<<40), context.Canceled)
	assert.Equal(t, "https://example.com/x", Redact("https://ann:pw@example.com/x"))
	assert.Equal(t, "plain", Redact("plain"))
	c, err := NewClient("https://example.com", ClientOptions{})
	require.NoError(t, err)
	assert.Equal(t, 3, c.attempts)
	assert.Equal(t, http.DefaultClient, c.http)
	assert.True(t, transient(500))
	assert.False(t, transient(404))
	assert.Equal(t, "", readMessage(strings.NewReader("{")))
	assert.True(t, errors.Is(&retryable{err: context.Canceled}, context.Canceled))
}

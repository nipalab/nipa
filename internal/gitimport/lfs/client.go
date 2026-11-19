package lfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const mediaType = "application/vnd.git-lfs+json"

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	endpoint    *url.URL
	http        Doer
	credentials CredentialHelper
	tempDir     string
	attempts    int
	backoff     time.Duration

	mu       sync.Mutex
	creds    Credentials
	haveAuth bool
	filled   bool
	approved bool
}

type ClientOptions struct {
	HTTP        Doer
	Credentials CredentialHelper
	TempDir     string
	Attempts    int
	Backoff     time.Duration
}

func NewClient(endpoint string, opts ClientOptions) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid lfs endpoint %q", Redact(endpoint))
	}
	c := &Client{endpoint: StripUserinfo(u), http: opts.HTTP, credentials: opts.Credentials, tempDir: opts.TempDir, attempts: opts.Attempts, backoff: opts.Backoff}
	if creds, ok := FromURL(u); ok {
		c.creds, c.haveAuth = creds, true
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	if c.attempts <= 0 {
		c.attempts = 3
	}
	if c.backoff < 0 {
		c.backoff = 0
	} else if c.backoff == 0 {
		c.backoff = 500 * time.Millisecond
	}
	return c, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint.String()
}

type ServerError struct {
	OID     string
	Status  int
	Message string
}

func (e *ServerError) Error() string {
	where := "lfs server"
	if e.OID != "" {
		where = "lfs object " + e.OID
	}
	if e.Message != "" {
		return fmt.Sprintf("%s: %s (%d)", where, e.Message, e.Status)
	}
	return fmt.Sprintf("%s: HTTP %d", where, e.Status)
}

func (e *ServerError) NotFound() bool {
	return e.Status == http.StatusNotFound || e.Status == http.StatusGone
}

type retryable struct{ err error }

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (c *Client) Fetch(ctx context.Context, p Pointer) (Object, error) {
	var last error
	for attempt := 0; attempt < c.attempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, c.backoff<<(attempt-1)); err != nil {
				return nil, err
			}
		}
		obj, err := c.fetchOnce(ctx, p)
		if err == nil {
			return obj, nil
		}
		var r *retryable
		if !errors.As(err, &r) {
			return nil, err
		}
		last = r.err
	}
	return nil, fmt.Errorf("download lfs object %s: %w", p.OID, last)
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type batchRequest struct {
	Operation string        `json:"operation"`
	Transfers []string      `json:"transfers"`
	Objects   []batchObject `json:"objects"`
	HashAlgo  string        `json:"hash_algo"`
}

type batchObject struct {
	OID     string                 `json:"oid"`
	Size    int64                  `json:"size"`
	Actions map[string]batchAction `json:"actions,omitempty"`
	Error   *batchError            `json:"error,omitempty"`
}

type batchAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header,omitempty"`
}

type batchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type batchResponse struct {
	Transfer string        `json:"transfer"`
	Objects  []batchObject `json:"objects"`
	Message  string        `json:"message"`
}

func (c *Client) fetchOnce(ctx context.Context, p Pointer) (Object, error) {
	action, err := c.batch(ctx, p)
	if err != nil {
		return nil, err
	}
	return c.download(ctx, p, action)
}

func (c *Client) batch(ctx context.Context, p Pointer) (*batchAction, error) {
	body, err := json.Marshal(batchRequest{Operation: "download", Transfers: []string{"basic"}, Objects: []batchObject{{OID: p.OID, Size: p.Size}}, HashAlgo: "sha256"})
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		msg := readMessage(resp.Body)
		err := &ServerError{Status: resp.StatusCode, Message: msg}
		if transient(resp.StatusCode) {
			return nil, &retryable{err}
		}
		return nil, err
	}
	var br batchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&br); err != nil {
		return nil, &retryable{fmt.Errorf("read lfs batch response: %w", err)}
	}
	if br.Transfer != "" && br.Transfer != "basic" {
		return nil, fmt.Errorf("lfs server chose unsupported transfer %q", br.Transfer)
	}
	for _, o := range br.Objects {
		if o.OID != p.OID {
			continue
		}
		if o.Error != nil {
			return nil, &ServerError{OID: p.OID, Status: o.Error.Code, Message: o.Error.Message}
		}
		if a, ok := o.Actions["download"]; ok && a.Href != "" {
			return &a, nil
		}
		return nil, &ServerError{OID: p.OID, Status: http.StatusNotFound, Message: "no download action"}
	}
	return nil, &ServerError{OID: p.OID, Status: http.StatusNotFound, Message: "object missing from the batch response"}
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String()+"/objects/batch", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", mediaType)
		req.Header.Set("Content-Type", mediaType)
		creds, ok := c.auth()
		if ok {
			req.Header.Set("Authorization", creds.Header())
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, &retryable{err}
		}
		if resp.StatusCode != http.StatusUnauthorized {
			if resp.StatusCode == http.StatusOK {
				c.approve(ctx)
			}
			return resp, nil
		}
		_ = resp.Body.Close()
		if !c.fill(ctx) {
			return nil, &ServerError{Status: http.StatusUnauthorized, Message: "authentication required"}
		}
	}
}

func (c *Client) auth() (Credentials, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creds, c.haveAuth
}

func (c *Client) fill(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.filled && c.haveAuth && c.credentials != nil {
		_ = c.credentials.Reject(ctx, c.endpoint, c.creds)
	}
	if c.filled || c.credentials == nil {
		return false
	}
	c.filled = true
	creds, err := c.credentials.Fill(ctx, c.endpoint)
	if err != nil {
		return false
	}
	c.creds, c.haveAuth = creds, true
	return true
}

func (c *Client) approve(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.filled && !c.approved && c.credentials != nil {
		c.approved = true
		_ = c.credentials.Approve(ctx, c.endpoint, c.creds)
	}
}

func (c *Client) download(ctx context.Context, p Pointer, a *batchAction) (Object, error) {
	href, err := url.Parse(a.Href)
	if err != nil {
		return nil, fmt.Errorf("lfs object %s: invalid download href", p.OID)
	}
	if !href.IsAbs() {
		href = c.endpoint.ResolveReference(href)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, StripUserinfo(href).String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range a.Header {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Authorization") == "" && SameOrigin(href, c.endpoint) {
		if creds, ok := c.auth(); ok {
			req.Header.Set("Authorization", creds.Header())
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &retryable{err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := &ServerError{OID: p.OID, Status: resp.StatusCode, Message: readMessage(resp.Body)}
		if transient(resp.StatusCode) {
			return nil, &retryable{err}
		}
		return nil, err
	}
	return c.spool(ctx, p, resp.Body)
}

func (c *Client) spool(ctx context.Context, p Pointer, body io.Reader) (Object, error) {
	if c.tempDir != "" {
		if err := os.MkdirAll(c.tempDir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.CreateTemp(c.tempDir, "lfs-*")
	if err != nil {
		return nil, err
	}
	obj := &tempObject{File: f}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, p.Size+1))
	if err == nil {
		err = check(p, n, h)
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = obj.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &retryable{err}
	}
	return obj, nil
}

type tempObject struct {
	*os.File
}

func (t *tempObject) Close() error {
	err := t.File.Close()
	if rmErr := os.Remove(t.Name()); rmErr != nil && err == nil {
		err = rmErr
	}
	return err
}

func transient(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}

func readMessage(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 4096))
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		return body.Message
	}
	return ""
}

func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	return StripUserinfo(u).String()
}

package chunkurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/domain"
)

// bundleDomain separates bundle URL signatures from single-chunk URL
// signatures (Sign) and receipts made with the same key: a chunk URL's
// signed message starts with its operation, never with this label, so no
// signature stands in for another.
const bundleDomain = "nipa-chunk-bundle:v1"

const queryCount = "count"

// BundleParams are the signed limits of a bundle upload URL.
type BundleParams struct {
	// MaxSize is the largest body (Content-Length) the URL accepts.
	MaxSize int64
	// MaxCount is the most entries one body may hold.
	MaxCount int
	Exp      int64
	Sig      string
}

// BundlePath signs a bundle upload path for the project:
// /api/chunk-bundles/{org}/{project}?size=&count=&exp=&sig=. It lets the
// holder upload any number of bundles, each at most maxSize bytes and
// maxCount entries, into the project until exp.
func BundlePath(key, org, project string, maxSize int64, maxCount int, exp int64) string {
	q := url.Values{}
	q.Set(querySize, strconv.FormatInt(maxSize, 10))
	q.Set(queryCount, strconv.Itoa(maxCount))
	q.Set(queryExp, strconv.FormatInt(exp, 10))
	q.Set(querySig, SignBundle(key, org, project, maxSize, maxCount, exp))
	return "/api/chunk-bundles/" + url.PathEscape(org) + "/" + url.PathEscape(project) + "?" + q.Encode()
}

// SignBundle is the HMAC-SHA256 (hex) of a bundle URL's limits under key.
func SignBundle(key, org, project string, maxSize int64, maxCount int, exp int64) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%s\n%d\n%d\n%d", bundleDomain, org, project, maxSize, maxCount, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

// ParseBundleParams reads a bundle URL's query.
func ParseBundleParams(query url.Values) (BundleParams, error) {
	size, err := strconv.ParseInt(query.Get(querySize), 10, 64)
	if err != nil || size <= 0 {
		return BundleParams{}, domain.NewErrorUser("invalid chunk bundle size")
	}
	count, err := strconv.Atoi(query.Get(queryCount))
	if err != nil || count <= 0 {
		return BundleParams{}, domain.NewErrorUser("invalid chunk bundle count")
	}
	exp, err := strconv.ParseInt(query.Get(queryExp), 10, 64)
	if err != nil {
		return BundleParams{}, domain.NewErrorUser("invalid chunk url expiry")
	}
	sig := query.Get(querySig)
	if sig == "" {
		return BundleParams{}, domain.NewErrorNoPermission()
	}
	return BundleParams{MaxSize: size, MaxCount: count, Exp: exp, Sig: sig}, nil
}

// VerifyBundle checks a bundle URL's signature in constant time and its
// expiry.
func VerifyBundle(key, org, project string, p BundleParams, now time.Time) error {
	if now.Unix() > p.Exp {
		return domain.NewErrorNoPermission()
	}
	expected := SignBundle(key, org, project, p.MaxSize, p.MaxCount, p.Exp)
	if !hmac.Equal([]byte(expected), []byte(p.Sig)) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

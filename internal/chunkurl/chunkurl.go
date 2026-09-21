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

const (
	OpUpload   = "put"
	OpDownload = "get"
)

const (
	queryOp   = "op"
	querySize = "size"
	queryExp  = "exp"
	querySig  = "sig"
)

type Params struct {
	Op   string
	Size int64
	Exp  int64
	Sig  string
}

func ParseParams(query url.Values) (Params, error) {
	op := query.Get(queryOp)
	if op != OpUpload && op != OpDownload {
		return Params{}, domain.NewErrorUser("invalid chunk transfer operation")
	}
	exp, err := strconv.ParseInt(query.Get(queryExp), 10, 64)
	if err != nil {
		return Params{}, domain.NewErrorUser("invalid chunk url expiry")
	}
	var size int64
	if raw := query.Get(querySize); raw != "" {
		size, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || size < 0 {
			return Params{}, domain.NewErrorUser("invalid chunk url size")
		}
	}
	sig := query.Get(querySig)
	if sig == "" {
		return Params{}, domain.NewErrorNoPermission()
	}
	return Params{Op: op, Size: size, Exp: exp, Sig: sig}, nil
}

func Verify(key, org, project, hash string, p Params, now time.Time) error {
	if now.Unix() > p.Exp {
		return domain.NewErrorNoPermission()
	}
	expected := Sign(key, org, project, hash, p.Op, p.Size, p.Exp)
	if !hmac.Equal([]byte(expected), []byte(p.Sig)) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

func Sign(key, org, project, hash, op string, size, exp int64) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s\n%s\n%s\n%s\n%d\n%d", op, org, project, hash, size, exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func UploadPath(key, org, project, hash string, size, exp int64) string {
	q := url.Values{}
	q.Set(queryOp, OpUpload)
	q.Set(querySize, strconv.FormatInt(size, 10))
	q.Set(queryExp, strconv.FormatInt(exp, 10))
	q.Set(querySig, Sign(key, org, project, hash, OpUpload, size, exp))
	return chunkPath(org, project, hash) + "?" + q.Encode()
}

func DownloadPath(key, org, project, hash string, exp int64) string {
	q := url.Values{}
	q.Set(queryOp, OpDownload)
	q.Set(queryExp, strconv.FormatInt(exp, 10))
	q.Set(querySig, Sign(key, org, project, hash, OpDownload, 0, exp))
	return chunkPath(org, project, hash) + "?" + q.Encode()
}

func Expiry(now time.Time, ttl time.Duration) int64 {
	return now.Add(ttl).Unix()
}

func chunkPath(org, project, hash string) string {
	return "/api/v1/orgs/" + url.PathEscape(org) + "/projects/" + url.PathEscape(project) + "/chunks/" + url.PathEscape(hash)
}

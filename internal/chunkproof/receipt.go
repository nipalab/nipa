package chunkproof

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// ReceiptHeader is the response header a proxied chunk upload carries its
// receipt in.
const ReceiptHeader = "X-Nipa-Chunk-Receipt"

const (
	// receiptDomain separates receipt MACs from the other MACs made with the
	// same key (signed chunk URLs), so none can stand in for another.
	receiptDomain = "nipa-chunk-receipt:v1"
	expiryBytes   = 8
	sizeBytes     = 8
	receiptBytes  = expiryBytes + sizeBytes
)

// ErrInvalidReceipt is returned for a malformed, forged, expired or misbound
// receipt.
var ErrInvalidReceipt = errors.New("invalid chunk upload receipt")

// NewReceipt records that the server received and stored size bytes hashing
// to hash on a transfer signed for projectID. It is stateless: the HMAC under
// key covers the project, the hash, the server-measured size and
// the expiry.
func NewReceipt(key string, projectID snow.ID, hash domain.Hash, size int64, expires time.Time) string {
	payload := make([]byte, receiptBytes)
	binary.BigEndian.PutUint64(payload, uint64(expires.Unix()))
	binary.BigEndian.PutUint64(payload[expiryBytes:], uint64(size))
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + signReceipt(key, projectID, hash, encoded)
}

// VerifyReceipt checks the signature, the project and hash binding and the
// expiry of a receipt and returns the size it records.
func VerifyReceipt(key string, projectID snow.ID, hash domain.Hash, receipt string, now time.Time) (int64, error) {
	encoded, sig, ok := strings.Cut(receipt, ".")
	if !ok || encoded == "" || sig == "" {
		return 0, ErrInvalidReceipt
	}
	if !hmac.Equal([]byte(sig), []byte(signReceipt(key, projectID, hash, encoded))) {
		return 0, ErrInvalidReceipt
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) != receiptBytes {
		return 0, ErrInvalidReceipt
	}
	if now.Unix() > int64(binary.BigEndian.Uint64(payload)) {
		return 0, ErrInvalidReceipt
	}
	size := int64(binary.BigEndian.Uint64(payload[expiryBytes:]))
	if size <= 0 {
		return 0, ErrInvalidReceipt
	}
	return size, nil
}

func signReceipt(key string, projectID snow.ID, hash domain.Hash, encoded string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(receiptDomain))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(projectID.Int64()))
	mac.Write(id[:])
	mac.Write(hash[:])
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

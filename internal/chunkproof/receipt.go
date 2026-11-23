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

const (
	// receiptDomain separates receipt MACs from the other MACs made with the
	// same key (signed chunk and bundle URLs), so none can stand in for
	// another. v1 receipts (size only, no location) are not accepted.
	receiptDomain = "nipa-chunk-receipt:v2"
	expiryBytes   = 8
	sizeBytes     = 8
	// fixedBytes is the payload before the location: expiry and size.
	fixedBytes = expiryBytes + sizeBytes
)

// LocationKind says how a stored chunk's bytes are laid out in its store.
type LocationKind byte

const (
	// LocationLoose is one object per chunk at the project's key for its
	// hash. It carries no further fields.
	LocationLoose LocationKind = 1
)

// Location is where a bundle upload stored a chunk. It is encoded as one kind
// byte followed by the kind's fields, so a new kind (a pack and an offset)
// adds a value, not a new receipt format.
type Location struct {
	Kind LocationKind
}

// Loose is the location of a chunk stored as its own object.
func Loose() Location {
	return Location{Kind: LocationLoose}
}

func (l Location) encode() ([]byte, bool) {
	switch l.Kind {
	case LocationLoose:
		return []byte{byte(LocationLoose)}, true
	default:
		return nil, false
	}
}

func decodeLocation(raw []byte) (Location, bool) {
	if len(raw) == 0 {
		return Location{}, false
	}
	switch LocationKind(raw[0]) {
	case LocationLoose:
		return Loose(), len(raw) == 1
	default:
		return Location{}, false
	}
}

// ErrInvalidReceipt is returned for a malformed, forged, expired or misbound
// receipt.
var ErrInvalidReceipt = errors.New("invalid chunk upload receipt")

// NewReceipt records that the server received, hashed and stored size bytes
// hashing to hash for projectID at location. It is stateless:
// base64url(expiry ‖ size ‖ location) "." base64url(HMAC-SHA256), the MAC
// under key covering the domain label, the project, the hash and the whole
// payload. It panics on a location kind it cannot encode (a programming
// error).
func NewReceipt(key string, projectID snow.ID, hash domain.Hash, size int64, location Location, expires time.Time) string {
	loc, ok := location.encode()
	if !ok {
		panic("chunkproof: unknown receipt location kind")
	}
	payload := make([]byte, fixedBytes, fixedBytes+len(loc))
	binary.BigEndian.PutUint64(payload, uint64(expires.Unix()))
	binary.BigEndian.PutUint64(payload[expiryBytes:], uint64(size))
	payload = append(payload, loc...)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + signReceipt(key, projectID, hash, payload)
}

// VerifyReceipt checks the signature, the project and hash binding and the
// expiry of a receipt and returns the size and location it records.
func VerifyReceipt(key string, projectID snow.ID, hash domain.Hash, receipt string, now time.Time) (int64, Location, error) {
	encoded, sig, ok := strings.Cut(receipt, ".")
	if !ok || encoded == "" || sig == "" {
		return 0, Location{}, ErrInvalidReceipt
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(payload) <= fixedBytes {
		return 0, Location{}, ErrInvalidReceipt
	}
	if !hmac.Equal([]byte(sig), []byte(signReceipt(key, projectID, hash, payload))) {
		return 0, Location{}, ErrInvalidReceipt
	}
	if now.Unix() > int64(binary.BigEndian.Uint64(payload)) {
		return 0, Location{}, ErrInvalidReceipt
	}
	size := int64(binary.BigEndian.Uint64(payload[expiryBytes:]))
	if size <= 0 {
		return 0, Location{}, ErrInvalidReceipt
	}
	location, ok := decodeLocation(payload[fixedBytes:])
	if !ok {
		return 0, Location{}, ErrInvalidReceipt
	}
	return size, location, nil
}

func signReceipt(key string, projectID snow.ID, hash domain.Hash, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(receiptDomain))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(projectID.Int64()))
	mac.Write(id[:])
	mac.Write(hash[:])
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

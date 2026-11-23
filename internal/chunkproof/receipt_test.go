package chunkproof

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestReceiptRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))

	receipt := NewReceipt("key", snow.ID(7), hash, 4096, Loose(), now.Add(time.Hour))
	size, location, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
	require.NoError(t, err)
	require.Equal(t, int64(4096), size)
	require.Equal(t, Loose(), location)
	require.Equal(t, LocationLoose, location.Kind)

	size, _, err = VerifyReceipt("key", snow.ID(7), hash, receipt, now.Add(time.Hour))
	require.NoError(t, err, "a receipt is valid up to and including its expiry second")
	require.Equal(t, int64(4096), size)
}

func TestReceiptRoundTripPacked(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))

	receipt := NewReceipt("key", snow.ID(7), hash, 4096, Packed(snow.ID(42), 1234), now.Add(time.Hour))
	size, location, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
	require.NoError(t, err)
	require.Equal(t, int64(4096), size)
	require.Equal(t, LocationPack, location.Kind)
	require.Equal(t, snow.ID(42), location.PackID)
	require.Equal(t, int64(1234), location.Offset)
}

func TestNewReceiptPanicsOnUnknownLocation(t *testing.T) {
	for name, location := range map[string]Location{
		"unknown kind":    {Kind: 99},
		"pack without id": Packed(0, 4),
		"negative offset": Packed(snow.ID(1), -1),
	} {
		t.Run(name, func(t *testing.T) {
			require.Panics(t, func() {
				NewReceipt("key", snow.ID(7), domain.Hash{}, 1, location, time.Unix(1, 0))
			})
		})
	}
}

// packFields encodes a pack location's kind byte and fields.
func packFields(packID, offset int64) []byte {
	out := []byte{byte(LocationPack)}
	out = binary.BigEndian.AppendUint64(out, uint64(packID))
	return binary.BigEndian.AppendUint64(out, uint64(offset))
}

func TestVerifyReceiptRejectsMovedPackLocation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	receipt := NewReceipt("key", snow.ID(7), hash, 4096, Packed(snow.ID(42), 1234), now.Add(time.Hour))
	_, sig, _ := strings.Cut(receipt, ".")
	for name, fields := range map[string][]byte{
		"other pack":   packFields(43, 1234),
		"other offset": packFields(42, 1235),
		"loose":        {byte(LocationLoose)},
	} {
		t.Run(name, func(t *testing.T) {
			forged := b64(encodePayload(now.Add(time.Hour), 4096, fields...)) + "." + sig
			_, _, err := VerifyReceipt("key", snow.ID(7), hash, forged, now)
			require.ErrorIs(t, err, ErrInvalidReceipt)
		})
	}
}

// encodePayload builds a receipt payload for forging tests.
func encodePayload(expires time.Time, size int64, location ...byte) []byte {
	payload := make([]byte, fixedBytes, fixedBytes+len(location))
	binary.BigEndian.PutUint64(payload, uint64(expires.Unix()))
	binary.BigEndian.PutUint64(payload[expiryBytes:], uint64(size))
	return append(payload, location...)
}

func b64(payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload)
}

func TestVerifyReceiptRejects(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	receipt := NewReceipt("key", snow.ID(7), hash, 4096, Loose(), now.Add(time.Hour))
	encoded, sig, _ := strings.Cut(receipt, ".")
	loose := byte(LocationLoose)

	// A receipt-shaped payload carrying another domain's MAC: the same key
	// signs other things (chunk and bundle URLs), so only the domain label
	// tells them apart.
	otherPayload := encodePayload(now.Add(time.Hour), 4096, loose)
	otherMAC := b64(otherPayload) + "." + signWithDomain("nipa-chunk-other:v1", "key", snow.ID(7), hash, otherPayload)
	// A v1 receipt: size only, the MAC over the encoded payload under the v1
	// label.
	v1Encoded := b64(encodePayload(now.Add(time.Hour), 4096))
	v1 := v1Encoded + "." + signWithDomain("nipa-chunk-receipt:v1", "key", snow.ID(7), hash, []byte(v1Encoded))
	// A bigger size, a later expiry or another location under the original MAC.
	resized := b64(encodePayload(now.Add(time.Hour), 1<<20, loose)) + "." + sig
	extended := b64(encodePayload(now.Add(48*time.Hour), 4096, loose)) + "." + sig
	relocated := b64(encodePayload(now.Add(time.Hour), 4096, 2, 0, 0, 0, 0, 0, 0, 0, 1)) + "." + sig

	cases := map[string]string{
		"wrong key":                   "",
		"wrong project":               "",
		"wrong hash":                  "",
		"expired":                     "",
		"empty":                       "",
		"no separator":                encoded,
		"empty signature":             encoded + ".",
		"empty payload":               "." + sig,
		"tampered signature":          encoded + "." + strings.Repeat("A", len(sig)),
		"tampered size":               resized,
		"tampered expiry":             extended,
		"tampered location":           relocated,
		"other domain MAC as receipt": otherMAC,
		"v1 receipt":                  v1,
		"receipt with trailing bytes": receipt + "x",
	}
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			key, project, h, at := "key", snow.ID(7), hash, now
			switch name {
			case "wrong key":
				key, candidate = "other", receipt
			case "wrong project":
				project, candidate = snow.ID(8), receipt
			case "wrong hash":
				h, candidate = domain.Hash{1}, receipt
			case "expired":
				at, candidate = now.Add(time.Hour+time.Second), receipt
			}
			size, location, err := VerifyReceipt(key, project, h, candidate, at)
			require.ErrorIs(t, err, ErrInvalidReceipt)
			require.Zero(t, size)
			require.Zero(t, location)
		})
	}
}

func TestVerifyReceiptRejectsBadPayloads(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	signed := func(payload []byte) string {
		return b64(payload) + "." + signReceipt("key", snow.ID(7), hash, payload)
	}
	loose := byte(LocationLoose)

	for name, receipt := range map[string]string{
		"no location":        signed(encodePayload(now.Add(time.Hour), 10)),
		"short payload":      signed(make([]byte, fixedBytes-1)),
		"not base64":         "!!not-base64!!." + signReceipt("key", snow.ID(7), hash, nil),
		"zero size":          signed(encodePayload(now.Add(time.Hour), 0, loose)),
		"negative size":      signed(encodePayload(now.Add(time.Hour), -1, loose)),
		"unknown kind":       signed(encodePayload(now.Add(time.Hour), 10, 0)),
		"loose with a field": signed(encodePayload(now.Add(time.Hour), 10, loose, 1)),
		"short pack fields":  signed(encodePayload(now.Add(time.Hour), 10, 2, 0, 0, 0, 0, 0, 0, 0, 1)),
		"long pack fields":   signed(encodePayload(now.Add(time.Hour), 10, append(packFields(1, 4), 0)...)),
		"zero pack id":       signed(encodePayload(now.Add(time.Hour), 10, packFields(0, 4)...)),
		"negative pack id":   signed(encodePayload(now.Add(time.Hour), 10, packFields(-1, 4)...)),
		"negative offset":    signed(encodePayload(now.Add(time.Hour), 10, packFields(1, -4)...)),
		"future kind":        signed(encodePayload(now.Add(time.Hour), 10, 3)),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
			require.ErrorIs(t, err, ErrInvalidReceipt)
		})
	}
}

func FuzzVerifyReceipt(f *testing.F) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	f.Add(NewReceipt("key", snow.ID(7), hash, 4096, Loose(), now.Add(time.Hour)))
	f.Add(NewReceipt("key", snow.ID(7), hash, 4096, Packed(snow.ID(9), 4), now.Add(time.Hour)))
	f.Add("")
	f.Add(".")
	f.Add("AAAAAAAAAAAAAAAAAAAAAAE.sig")
	f.Fuzz(func(t *testing.T, receipt string) {
		size, location, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
		if err != nil {
			require.ErrorIs(t, err, ErrInvalidReceipt)
			require.Zero(t, size)
			return
		}
		// Only a receipt this key issued is accepted, and it re-encodes to
		// itself.
		require.Positive(t, size)
		encoded, _, _ := strings.Cut(receipt, ".")
		payload, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		require.NoError(t, decodeErr)
		expires := time.Unix(int64(binary.BigEndian.Uint64(payload)), 0)
		require.Equal(t, NewReceipt("key", snow.ID(7), hash, size, location, expires), receipt)
	})
}

func FuzzReceiptRoundTrip(f *testing.F) {
	f.Add(int64(1), int64(4096), int64(3600), int64(0), int64(0))
	f.Add(int64(-5), int64(16<<20), int64(0), int64(7), int64(4))
	f.Fuzz(func(t *testing.T, project, size, ttl, packID, offset int64) {
		if size <= 0 || ttl < 0 || ttl > 1<<40 || packID < 0 || offset < 0 {
			t.Skip()
		}
		now := time.Unix(1700000000, 0)
		hash := chunker.Sum([]byte("payload"))
		want := Loose()
		if packID > 0 {
			want = Packed(snow.ID(packID), offset)
		}
		receipt := NewReceipt("key", snow.ID(project), hash, size, want, now.Add(time.Duration(ttl)*time.Second))
		got, location, err := VerifyReceipt("key", snow.ID(project), hash, receipt, now)
		require.NoError(t, err)
		require.Equal(t, size, got)
		require.Equal(t, want, location)
		_, _, err = VerifyReceipt("key", snow.ID(project+1), hash, receipt, now)
		require.ErrorIs(t, err, ErrInvalidReceipt)
	})
}

func signWithDomain(label, key string, projectID snow.ID, hash domain.Hash, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(label))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(projectID.Int64()))
	mac.Write(id[:])
	mac.Write(hash[:])
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSignWithDomainMatchesReceiptLabel(t *testing.T) {
	hash := chunker.Sum([]byte("payload"))
	require.Equal(t, signReceipt("key", snow.ID(7), hash, []byte("abc")), signWithDomain(receiptDomain, "key", snow.ID(7), hash, []byte("abc")))
}

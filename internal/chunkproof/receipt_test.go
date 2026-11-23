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

	receipt := NewReceipt("key", snow.ID(7), hash, 4096, now.Add(time.Hour))
	size, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
	require.NoError(t, err)
	require.Equal(t, int64(4096), size)

	size, err = VerifyReceipt("key", snow.ID(7), hash, receipt, now.Add(time.Hour))
	require.NoError(t, err, "a receipt is valid up to and including its expiry second")
	require.Equal(t, int64(4096), size)
}

// encodeReceiptPayload builds a receipt payload for forging tests.
func encodeReceiptPayload(expires time.Time, size int64) string {
	payload := make([]byte, receiptBytes)
	binary.BigEndian.PutUint64(payload, uint64(expires.Unix()))
	binary.BigEndian.PutUint64(payload[expiryBytes:], uint64(size))
	return base64.RawURLEncoding.EncodeToString(payload)
}

func TestVerifyReceiptRejects(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	receipt := NewReceipt("key", snow.ID(7), hash, 4096, now.Add(time.Hour))
	encoded, sig, _ := strings.Cut(receipt, ".")

	// A receipt-shaped payload carrying another domain's MAC: the same key
	// signs other things (chunk URLs), so only the domain label tells them
	// apart.
	otherMAC := encodeReceiptPayload(now.Add(time.Hour), 4096)
	otherMAC += "." + signWithDomain("nipa-chunk-other:v1", "key", snow.ID(7), hash, otherMAC)
	// A bigger size under the original MAC.
	resized := encodeReceiptPayload(now.Add(time.Hour), 1<<20) + "." + sig
	// A later expiry under the original MAC.
	extended := encodeReceiptPayload(now.Add(48*time.Hour), 4096) + "." + sig

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
		"other domain MAC as receipt": otherMAC,
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
			size, err := VerifyReceipt(key, project, h, candidate, at)
			require.ErrorIs(t, err, ErrInvalidReceipt)
			require.Zero(t, size)
		})
	}
}

func TestVerifyReceiptRejectsBadPayloads(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	signed := func(encoded string) string {
		return encoded + "." + signReceipt("key", snow.ID(7), hash, encoded)
	}

	short := base64.RawURLEncoding.EncodeToString(make([]byte, receiptBytes-1))
	notBase64 := "!!not-base64!!"
	zeroSize := encodeReceiptPayload(now.Add(time.Hour), 0)
	negativeSize := encodeReceiptPayload(now.Add(time.Hour), -1)

	for name, receipt := range map[string]string{
		"short payload": signed(short),
		"not base64":    signed(notBase64),
		"zero size":     signed(zeroSize),
		"negative size": signed(negativeSize),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
			require.ErrorIs(t, err, ErrInvalidReceipt)
		})
	}
}

func FuzzVerifyReceipt(f *testing.F) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	f.Add(NewReceipt("key", snow.ID(7), hash, 4096, now.Add(time.Hour)))
	f.Add("")
	f.Add(".")
	f.Add("AAAAAAAAAAAAAAAAAAAAAA.sig")
	f.Fuzz(func(t *testing.T, receipt string) {
		size, err := VerifyReceipt("key", snow.ID(7), hash, receipt, now)
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
		require.Equal(t, NewReceipt("key", snow.ID(7), hash, size, expires), receipt)
	})
}

func FuzzReceiptRoundTrip(f *testing.F) {
	f.Add(int64(1), int64(4096), int64(3600))
	f.Add(int64(-5), int64(16<<20), int64(0))
	f.Fuzz(func(t *testing.T, project, size, ttl int64) {
		if size <= 0 || ttl < 0 || ttl > 1<<40 {
			t.Skip()
		}
		now := time.Unix(1700000000, 0)
		hash := chunker.Sum([]byte("payload"))
		receipt := NewReceipt("key", snow.ID(project), hash, size, now.Add(time.Duration(ttl)*time.Second))
		got, err := VerifyReceipt("key", snow.ID(project), hash, receipt, now)
		require.NoError(t, err)
		require.Equal(t, size, got)
		_, err = VerifyReceipt("key", snow.ID(project+1), hash, receipt, now)
		require.ErrorIs(t, err, ErrInvalidReceipt)
	})
}

func signWithDomain(label, key string, projectID snow.ID, hash domain.Hash, encoded string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(label))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(projectID.Int64()))
	mac.Write(id[:])
	mac.Write(hash[:])
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSignWithDomainMatchesReceiptLabel(t *testing.T) {
	hash := chunker.Sum([]byte("payload"))
	require.Equal(t, signReceipt("key", snow.ID(7), hash, "abc"), signWithDomain(receiptDomain, "key", snow.ID(7), hash, "abc"))
}

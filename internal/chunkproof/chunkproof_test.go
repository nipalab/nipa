package chunkproof

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestChallengeRoundTrip(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))

	challenge, err := NewChallenge("key", snow.ID(7), hash, now.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, VerifyChallenge("key", snow.ID(7), hash, challenge, now))

	again, err := NewChallenge("key", snow.ID(7), hash, now.Add(time.Hour))
	require.NoError(t, err)
	require.NotEqual(t, challenge, again, "every challenge carries a fresh nonce")
}

func TestVerifyChallengeRejects(t *testing.T) {
	now := time.Unix(1700000000, 0)
	hash := chunker.Sum([]byte("payload"))
	challenge, err := NewChallenge("key", snow.ID(7), hash, now.Add(time.Hour))
	require.NoError(t, err)

	cases := map[string]error{
		"wrong key":      VerifyChallenge("other", snow.ID(7), hash, challenge, now),
		"wrong project":  VerifyChallenge("key", snow.ID(8), hash, challenge, now),
		"wrong hash":     VerifyChallenge("key", snow.ID(7), domain.Hash{1}, challenge, now),
		"expired":        VerifyChallenge("key", snow.ID(7), hash, challenge, now.Add(2*time.Hour)),
		"empty":          VerifyChallenge("key", snow.ID(7), hash, "", now),
		"no separator":   VerifyChallenge("key", snow.ID(7), hash, "abc", now),
		"empty parts":    VerifyChallenge("key", snow.ID(7), hash, ".", now),
		"forged payload": VerifyChallenge("key", snow.ID(7), hash, "AAAA."+challenge[len(challenge)-10:], now),
	}
	for name, err := range cases {
		require.ErrorIs(t, err, ErrInvalidChallenge, name)
	}
}

func TestVerifyChallengeRejectsBadPayloadLength(t *testing.T) {
	hash := chunker.Sum([]byte("payload"))
	encoded := "AAAA"
	forged := encoded + "." + sign("key", snow.ID(7), hash, encoded)
	require.ErrorIs(t, VerifyChallenge("key", snow.ID(7), hash, forged, time.Unix(0, 0)), ErrInvalidChallenge)

	notBase64 := "!!!"
	forged = notBase64 + "." + sign("key", snow.ID(7), hash, notBase64)
	require.ErrorIs(t, VerifyChallenge("key", snow.ID(7), hash, forged, time.Unix(0, 0)), ErrInvalidChallenge)
}

func TestProof(t *testing.T) {
	data := []byte("chunk bytes")

	proof := Proof("challenge-1", data)
	require.True(t, Equal(proof, Proof("challenge-1", data)))
	require.False(t, Equal(proof, Proof("challenge-2", data)), "a proof is bound to its challenge")
	require.False(t, Equal(proof, Proof("challenge-1", []byte("other bytes"))), "a proof needs the bytes")
	require.Len(t, proof, 64)
}

func TestVerifierMatchesProofAndSum(t *testing.T) {
	for _, size := range []int{0, 1, 63, 64 << 10, 1<<20 + 17} {
		data := make([]byte, size)
		_, err := rand.Read(data)
		require.NoError(t, err)

		v := NewVerifier("challenge")
		// Feed in uneven pieces, as a streamed object read would.
		for rest := data; len(rest) > 0; {
			n := min(len(rest), 1+len(rest)/3)
			written, err := v.Write(rest[:n])
			require.NoError(t, err)
			require.Equal(t, n, written)
			rest = rest[n:]
		}
		require.Equal(t, int64(size), v.Size())
		require.Equal(t, chunker.Sum(data), v.Sum())
		require.Equal(t, Proof("challenge", data), v.Proof())
		require.NotEqual(t, Proof("other", data), v.Proof())
	}
}

func FuzzVerifierMatchesProofAndSum(f *testing.F) {
	f.Add("challenge", []byte("chunk bytes"), uint8(3))
	f.Add("", []byte{}, uint8(0))
	f.Add("c.sig", bytes.Repeat([]byte{0}, 4096), uint8(200))
	f.Fuzz(func(t *testing.T, challenge string, data []byte, split uint8) {
		v := NewVerifier(challenge)
		cut := 0
		if len(data) > 0 {
			cut = int(split) % (len(data) + 1)
		}
		_, _ = v.Write(data[:cut])
		_, _ = v.Write(data[cut:])
		if v.Size() != int64(len(data)) {
			t.Fatalf("size %d, want %d", v.Size(), len(data))
		}
		if v.Sum() != chunker.Sum(data) {
			t.Fatal("streamed hash differs from chunker.Sum")
		}
		if !Equal(v.Proof(), Proof(challenge, data)) {
			t.Fatal("streamed proof differs from Proof")
		}
	})
}

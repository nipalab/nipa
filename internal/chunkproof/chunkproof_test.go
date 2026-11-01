package chunkproof

import (
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

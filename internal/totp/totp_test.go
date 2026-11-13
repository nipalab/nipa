package totp

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var rfcSecret = []byte("12345678901234567890")

func TestCode_RFC6238Vectors(t *testing.T) {
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		assert.Equal(t, tc.code, Code(rfcSecret, Step(time.Unix(tc.unix, 0))), "t=%d", tc.unix)
	}
}

func TestMatch_Window(t *testing.T) {
	now := time.Unix(1234567890, 0)
	step := Step(now)
	for _, delta := range []int64{-1, 0, 1} {
		got, ok := Match(rfcSecret, Code(rfcSecret, step+delta), now, 0)
		require.True(t, ok, "delta %d", delta)
		assert.Equal(t, step+delta, got)
	}
	for _, delta := range []int64{-2, 2} {
		_, ok := Match(rfcSecret, Code(rfcSecret, step+delta), now, 0)
		assert.False(t, ok, "delta %d", delta)
	}
}

func TestMatch_RejectsReplayAndJunk(t *testing.T) {
	now := time.Unix(1234567890, 0)
	step := Step(now)
	code := Code(rfcSecret, step)
	_, ok := Match(rfcSecret, code, now, step)
	assert.False(t, ok)
	_, ok = Match(rfcSecret, Code(rfcSecret, step-1), now, step-1)
	assert.False(t, ok)
	got, ok := Match(rfcSecret, Code(rfcSecret, step+1), now, step)
	assert.True(t, ok)
	assert.Equal(t, step+1, got)

	_, ok = Match(rfcSecret, code[:3]+" "+code[3:], now, 0)
	assert.True(t, ok)
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		_, ok := Match(rfcSecret, bad, now, 0)
		assert.False(t, ok, bad)
	}
}

func TestNewSecretAndURI(t *testing.T) {
	secret, err := NewSecret()
	require.NoError(t, err)
	assert.Len(t, secret, SecretBytes)

	uri := URI("Nipa", "ann@example.com", rfcSecret)
	u, err := url.Parse(uri)
	require.NoError(t, err)
	assert.Equal(t, "otpauth", u.Scheme)
	assert.Equal(t, "totp", u.Host)
	assert.Equal(t, "/Nipa:ann@example.com", u.Path)
	q := u.Query()
	assert.Equal(t, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", q.Get("secret"))
	assert.Equal(t, "Nipa", q.Get("issuer"))
	assert.Equal(t, "6", q.Get("digits"))
	assert.Equal(t, "30", q.Get("period"))
	assert.Equal(t, "SHA1", q.Get("algorithm"))
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := NewRecoveryCodes(RecoveryCodeCount)
	require.NoError(t, err)
	require.Len(t, codes, RecoveryCodeCount)
	seen := map[string]bool{}
	for _, code := range codes {
		assert.Len(t, code, RecoveryCodeLength+1)
		assert.Equal(t, "-", code[5:6])
		assert.False(t, seen[code])
		seen[code] = true
		normalized := NormalizeRecoveryCode(code)
		assert.Len(t, normalized, RecoveryCodeLength)
		assert.Equal(t, HashRecoveryCode(code), HashRecoveryCode(strings.ToLower(normalized)))
	}
	assert.Empty(t, NormalizeRecoveryCode("ABCDE"))
	assert.Empty(t, NormalizeRecoveryCode("ABCDE-0OI1L"))
	assert.Equal(t, "ABCDEFGHJK", NormalizeRecoveryCode(" abcde fghjk "))
}

func TestIsCodeAndNormalize(t *testing.T) {
	assert.True(t, IsCode("012345"))
	assert.False(t, IsCode("01234a"))
	assert.Equal(t, "123456", NormalizeCode("123 456"))
	assert.Equal(t, "123456", NormalizeCode("123-\t456"))
}

func TestCipher(t *testing.T) {
	_, err := NewCipher([]byte("short"))
	require.Error(t, err)

	key := bytes.Repeat([]byte{7}, 32)
	c, err := NewCipher(key)
	require.NoError(t, err)
	sealed, err := c.Seal(rfcSecret, []byte("user-1"))
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), string(rfcSecret))

	opened, err := c.Open(sealed, []byte("user-1"))
	require.NoError(t, err)
	assert.Equal(t, rfcSecret, opened)

	_, err = c.Open(sealed, []byte("user-2"))
	assert.ErrorIs(t, err, ErrDecrypt)
	_, err = c.Open([]byte{1, 2}, []byte("user-1"))
	assert.ErrorIs(t, err, ErrDecrypt)

	other, err := NewCipher(bytes.Repeat([]byte{8}, 32))
	require.NoError(t, err)
	_, err = other.Open(sealed, []byte("user-1"))
	assert.ErrorIs(t, err, ErrDecrypt)
}

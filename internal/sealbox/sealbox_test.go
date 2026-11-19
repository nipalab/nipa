package sealbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

var material = []byte("0123456789abcdef0123456789abcdef")

func TestSealOpen(t *testing.T) {
	box, err := New(material, "test v1")
	require.NoError(t, err)

	sealed, err := box.Seal([]byte("secret"), []byte("7"))
	require.NoError(t, err)
	other, err := box.Seal([]byte("secret"), []byte("7"))
	require.NoError(t, err)
	require.NotEqual(t, sealed, other)

	plain, err := box.Open(sealed, []byte("7"))
	require.NoError(t, err)
	require.Equal(t, "secret", string(plain))

	_, err = box.Open(sealed, []byte("8"))
	require.ErrorIs(t, err, ErrOpen)
	_, err = box.Open(sealed[:4], []byte("7"))
	require.ErrorIs(t, err, ErrOpen)

	relabeled, err := New(material, "test v2")
	require.NoError(t, err)
	_, err = relabeled.Open(sealed, []byte("7"))
	require.ErrorIs(t, err, ErrOpen)
}

func TestNewRejectsShortKey(t *testing.T) {
	_, err := New(material[:31], "test v1")
	require.ErrorIs(t, err, ErrShortKey)
}

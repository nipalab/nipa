package grpc

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func jwtWith(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

func TestClient_UserID(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: jwtWith(`{"sub":"u42","user_id":7}`)})
	id, err := c.UserID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "u42", id)

	c = NewClient(NewTransport(), &stubSession{getTokenErr: errors.New("signed out")})
	_, err = c.UserID(context.Background())
	require.EqualError(t, err, "signed out")
}

func TestTokenSubject(t *testing.T) {
	padded := "h." + base64.URLEncoding.EncodeToString([]byte(`{"sub":"ab"}`)) + ".s"
	id, err := tokenSubject(padded)
	require.NoError(t, err)
	require.Equal(t, "ab", id)

	for _, bad := range []string{"opaque", "a.!!!.c", jwtWith("not json"), jwtWith(`{"user_id":1}`)} {
		_, err := tokenSubject(bad)
		require.Error(t, err, bad)
	}
}

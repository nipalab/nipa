package usecase

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type addressedLoginExecutor struct {
	stubLoginExecutor
	address string
}

func (a *addressedLoginExecutor) Address() string { return a.address }

func TestAuth_WarnIfCleartext(t *testing.T) {
	var out bytes.Buffer
	auth := NewAuth(&stubLoginExecutor{}, &stubSecureStorage{}, nil).WithCleartextWarning(&out)

	auth.WarnIfCleartext("https://nipa.example.com")
	auth.WarnIfCleartext("localhost:6745")
	auth.WarnIfCleartext("127.0.0.1:6745")
	auth.WarnIfCleartext("[::1]:6745")
	require.Empty(t, out.String())

	auth.WarnIfCleartext("http://nipa.example.com:6745")
	auth.WarnIfCleartext("other.example.com")
	require.Equal(t, 1, strings.Count(out.String(), "\n"), "at most once per invocation")
	require.Contains(t, out.String(), "warning: nipa.example.com:6745 is reached without TLS")
	require.Contains(t, out.String(), "https://")
}

func TestAuth_WarnIfCleartextDisabledByDefault(t *testing.T) {
	auth := NewAuth(&stubLoginExecutor{}, &stubSecureStorage{}, nil)
	require.NotPanics(t, func() { auth.WarnIfCleartext("nipa.example.com") })
}

func TestAuth_ImplicitLoginWarnsForDialedAddress(t *testing.T) {
	cases := []struct {
		dialed string
		warn   bool
	}{
		{"https://nipa.example.com", false},
		{"nipa.example.com", true},
		{"http://127.0.0.1:6745", false},
		{"", true},
	}
	for _, c := range cases {
		var out bytes.Buffer
		executor := &addressedLoginExecutor{stubLoginExecutor: stubLoginExecutor{usernameResult: &domain.LoginResult{AccessToken: "tok"}}, address: c.dialed}
		auth := NewAuth(executor, &stubSecureStorage{loadErr: errors.New("not found")}, &stubUserInput{username: "apin", password: "secret"}).WithCleartextWarning(&out)

		require.NoError(t, auth.MakeSureLoggedIn(context.Background(), "nipa.example.com"))
		require.Equal(t, c.warn, out.Len() > 0, c.dialed)
	}
}

func TestAuth_LoggedInSkipsCleartextWarning(t *testing.T) {
	var out bytes.Buffer
	token := signTestToken(t, "secret")
	auth := NewAuth(&stubLoginExecutor{}, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}, nil).WithCleartextWarning(&out)

	require.NoError(t, auth.MakeSureLoggedIn(context.Background(), "nipa.example.com"))
	require.Empty(t, out.String())
}

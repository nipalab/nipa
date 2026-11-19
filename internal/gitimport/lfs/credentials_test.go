package lfs

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedRun struct {
	args  []string
	stdin string
}

func fakeRunner(out string, err error, calls *[]recordedRun) CommandRunner {
	return func(_ context.Context, stdin string, args ...string) (string, error) {
		*calls = append(*calls, recordedRun{args: args, stdin: stdin})
		return out, err
	}
}

func TestGitCredentials(t *testing.T) {
	ctx := context.Background()
	u, _ := url.Parse("https://lfs.example.com:8443/org/repo.git/info/lfs")
	var calls []recordedRun

	g := &GitCredentials{Run: fakeRunner("protocol=https\nhost=lfs.example.com:8443\nusername=ann\npassword=s3cret\nnoise\n", nil, &calls)}
	c, err := g.Fill(ctx, u)
	require.NoError(t, err)
	assert.Equal(t, Credentials{Username: "ann", Password: "s3cret"}, c)
	assert.Equal(t, []string{"credential", "fill"}, calls[0].args)
	assert.Equal(t, "protocol=https\nhost=lfs.example.com:8443\ncapability[]=authtype\n\n", calls[0].stdin)

	require.NoError(t, g.Approve(ctx, u, c))
	assert.Equal(t, []string{"credential", "approve"}, calls[1].args)
	assert.Equal(t, "protocol=https\nhost=lfs.example.com:8443\nusername=ann\npassword=s3cret\n\n", calls[1].stdin)
	require.NoError(t, g.Reject(ctx, u, c))
	assert.Equal(t, []string{"credential", "reject"}, calls[2].args)

	calls = nil
	g = &GitCredentials{Run: fakeRunner("authtype=Bearer\ncredential=tok\n", nil, &calls)}
	c, err = g.Fill(ctx, u)
	require.NoError(t, err)
	assert.Equal(t, "Bearer tok", c.Header())
	require.NoError(t, g.Approve(ctx, u, c))
	assert.Contains(t, calls[1].stdin, "authtype=Bearer\ncredential=tok\n")

	g = &GitCredentials{Run: fakeRunner("", errors.New("exit 128"), &calls)}
	_, err = g.Fill(ctx, u)
	require.ErrorIs(t, err, ErrNoCredentials)
	g = &GitCredentials{Run: fakeRunner("protocol=https\n", nil, &calls)}
	_, err = g.Fill(ctx, u)
	require.ErrorIs(t, err, ErrNoCredentials)
}

func TestCredentialHelpers(t *testing.T) {
	assert.Equal(t, "Basic YW5uOnB3", Credentials{Username: "ann", Password: "pw"}.Header())
	assert.True(t, Credentials{}.Empty())

	u, _ := url.Parse("https://ann:pw@example.com/x")
	c, ok := FromURL(u)
	require.True(t, ok)
	assert.Equal(t, "pw", c.Password)
	assert.Equal(t, "https://example.com/x", StripUserinfo(u).String())
	assert.Equal(t, "https://ann:pw@example.com/x", u.String(), "strip copies")
	_, ok = FromURL(StripUserinfo(u))
	assert.False(t, ok)

	same := func(a, b string) bool {
		ua, _ := url.Parse(a)
		ub, _ := url.Parse(b)
		return SameOrigin(ua, ub)
	}
	assert.True(t, same("https://Example.com/a", "https://example.com:443/b"))
	assert.True(t, same("http://example.com/a", "http://example.com:80/b"))
	assert.False(t, same("https://example.com/a", "http://example.com/a"))
	assert.False(t, same("https://example.com/a", "https://cdn.example.com/a"))
	assert.False(t, same("https://example.com:8443/a", "https://example.com/a"))
	assert.True(t, same("ftp://h/a", "ftp://h/b"))

	if _, err := exec.LookPath("git"); err == nil {
		assert.NotNil(t, NewGitCredentials())
		out, err := runGit(context.Background(), "", "--version")
		require.NoError(t, err)
		assert.Contains(t, out, "git version")
	}
	t.Setenv("PATH", "")
	assert.Nil(t, NewGitCredentials())
}

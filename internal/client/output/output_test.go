package output

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, map[string]string{"a": "<b>"}))
	require.Equal(t, "{\"a\":\"<b>\"}\n", buf.String())
}

func TestFromEnv(t *testing.T) {
	t.Setenv(EnvFormat, "json")
	require.True(t, FromEnv())
	t.Setenv(EnvFormat, "JSON")
	require.True(t, FromEnv())
	t.Setenv(EnvFormat, "text")
	require.False(t, FromEnv())
}

func TestWriteError_DomainError(t *testing.T) {
	var buf bytes.Buffer
	err := clientDomain.NewUserError("binary file requires a lock").
		WithHint("binary changes need a lock", "nipa lock a.psd")
	require.NoError(t, WriteError(&buf, err))
	require.JSONEq(t, `{"error":{"code":400,"message":"binary file requires a lock","hint":"binary changes need a lock","action":"nipa lock a.psd"}}`, buf.String())
}

func TestWriteError_PlainError(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteError(&buf, errors.New("boom")))
	require.JSONEq(t, `{"error":{"code":1,"message":"boom"}}`, buf.String())
}

func TestNewStatus(t *testing.T) {
	st := NewStatus(&clientDomain.Status{Staged: []string{"a"}, Conflicts: []string{"c"}})
	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, st))
	require.JSONEq(t, `{"staged":["a"],"deleted":[],"modified":[],"untracked":[],"missing":[],"conflicts":["c"]}`, buf.String())
}

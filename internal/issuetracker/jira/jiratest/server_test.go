package jiratest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func call(t *testing.T, s *Server, method, path, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, s.URL+path, strings.NewReader(body))
	require.NoError(t, err)
	cfg := s.Config()
	req.SetBasicAuth(cfg.Account, cfg.Secret)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode
}

func TestServer(t *testing.T) {
	s := New(Options{})
	defer s.Close()
	s.AddIssue(Issue{Key: "A-1"})

	_, ok := s.Issue("A-2")
	assert.False(t, ok)
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodGet, "/rest/api/2/myself", ""))
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodGet, "/rest/api/2/issue/A-1/transitions", ""))
	assert.Equal(t, http.StatusNotFound, call(t, s, http.MethodGet, "/rest/api/2/issue/A-2/transitions", ""))
	assert.Equal(t, http.StatusBadRequest, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/remotelink", "{"))
	assert.Equal(t, http.StatusNotFound, call(t, s, http.MethodPost, "/rest/api/2/issue/A-2/remotelink", `{"object":{"url":"u","title":"t"}}`))
	assert.Equal(t, http.StatusBadRequest, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/comment", `{}`))
	assert.Equal(t, http.StatusNotFound, call(t, s, http.MethodPost, "/rest/api/2/issue/A-2/comment", `{"body":"x"}`))
	assert.Equal(t, http.StatusBadRequest, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/transitions", "{"))
	assert.Equal(t, http.StatusNotFound, call(t, s, http.MethodPost, "/rest/api/2/issue/A-2/transitions", `{}`))
	assert.Equal(t, http.StatusBadRequest, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/transitions", `{"transition":{"id":"999"}}`))
	assert.Equal(t, http.StatusNoContent, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/transitions", `{"transition":{"id":"31"}}`))

	issue, ok := s.Issue("A-1")
	require.True(t, ok)
	assert.Equal(t, "Done", issue.Status)
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodGet, "/rest/api/2/serverInfo", ""))
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodGet, "/rest/api/2/issue/A-1", ""))
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodGet, "/rest/api/2/issue/picker?query=a", ""))
	assert.Equal(t, http.StatusCreated, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/comment", `{"body":"x"}`))
	assert.Equal(t, http.StatusCreated, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/remotelink", `{"globalId":"g","object":{"url":"u","title":"t"}}`))
	assert.Equal(t, http.StatusOK, call(t, s, http.MethodPost, "/rest/api/2/issue/A-1/remotelink", `{"globalId":"g","object":{"url":"u","title":"t2"}}`))
	assert.Equal(t, "t2", s.RemoteLinks("A-1")[0].Title)
	assert.Equal(t, []string{"x"}, s.Comments("A-1"))
	assert.Len(t, s.Requests(), 17)
	assert.Equal(t, "GET /rest/api/2/myself", s.Requests()[0])
}

func TestServerBearer(t *testing.T) {
	s := New(Options{AuthKind: "bearer", Deployment: "Server"})
	defer s.Close()
	assert.Empty(t, s.Config().Account)
	assert.Equal(t, http.StatusUnauthorized, call(t, s, http.MethodGet, "/rest/api/2/myself", ""))
}

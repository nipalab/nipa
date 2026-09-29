package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

func TestNewPlan_Nil(t *testing.T) {
	out := NewPlan(nil)
	require.NotNil(t, out.Changes)
	require.NotNil(t, out.Conflicts)
	require.Empty(t, out.Changes)
	require.Empty(t, out.Conflicts)
}

func TestNewPlan_Fields(t *testing.T) {
	out := NewPlan(&clientDomain.Plan{
		Kind:          "merge",
		FastForward:   true,
		SourceBranch:  "feature",
		Targets:       []string{"3", "2"},
		Conflicts:     []string{"c.txt"},
		Changes:       []clientDomain.PlanChange{{Path: "a.txt", Status: "M", Binary: true, SizeBytes: 7}},
		UploadObjects: 2,
		UploadBytes:   99,
	})
	require.Equal(t, "merge", out.Kind)
	require.True(t, out.FastForward)
	require.Equal(t, "feature", out.SourceBranch)
	require.Equal(t, []string{"3", "2"}, out.Targets)
	require.Equal(t, []string{"c.txt"}, out.Conflicts)
	require.Equal(t, 2, out.UploadObjects)
	require.Equal(t, int64(99), out.UploadBytes)

	var buf bytes.Buffer
	require.NoError(t, WriteJSON(&buf, out))
	require.JSONEq(t, `{
		"kind":"merge",
		"fast_forward":true,
		"source_branch":"feature",
		"targets":["3","2"],
		"conflicts":["c.txt"],
		"changes":[{"path":"a.txt","status":"M","binary":true,"size_bytes":7}],
		"upload_objects":2,
		"upload_bytes":99
	}`, buf.String())
}

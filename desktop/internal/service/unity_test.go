package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
}

func TestService_MetaWarnings(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"ProjectSettings/ProjectVersion.txt": "m_EditorVersion: 6000.0.1f1",
		"Assets/hero.png":                    "PNG",
		"Assets/crate.fbx.meta":              "guid: 2222\n",
		"Assets/crate.fbx":                   "FBX",
	})
	svc, d := newModels(t, map[string]string{"Assets/crate.fbx.meta": "guid: 1111\n"})
	d.status = &daemonpb.StatusResponse{
		Branch:    "main",
		Untracked: []string{"Assets/hero.png"},
		Modified:  []string{"Assets/crate.fbx.meta"},
	}
	out, err := svc.MetaWarnings(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, []MetaWarning{
		{Path: "Assets/crate.fbx.meta", Kind: "guid-changed", Related: "Assets/crate.fbx", Message: "GUID changed from 1111 to 2222; references to this asset will break"},
		{Path: "Assets/hero.png", Kind: "missing-meta", Related: "Assets/hero.png.meta", Message: "has no .meta file; open the project in Unity so it gets one, then push both"},
	}, out)
	assert.Equal(t, "snapshot", d.reads[0].GetBlob().GetSource())

	d.status = &daemonpb.StatusResponse{Branch: "main", Modified: []string{"README.md"}}
	out, err = svc.MetaWarnings(ctx, root)
	require.NoError(t, err)
	assert.Empty(t, out)
	assert.NotNil(t, out)

	var many []string
	for i := 0; i < maxGUIDChecks+2; i++ {
		many = append(many, "Assets/m"+string(rune('a'+i%26))+string(rune('a'+i/26))+".meta")
	}
	d.reads = nil
	d.status = &daemonpb.StatusResponse{Branch: "main", Modified: many}
	_, err = svc.MetaWarnings(ctx, root)
	require.NoError(t, err)
	assert.Len(t, d.reads, maxGUIDChecks)

	d.callErr = status.Error(codes.Internal, "boom")
	_, err = svc.MetaWarnings(ctx, root)
	assert.ErrorContains(t, err, "boom")
}

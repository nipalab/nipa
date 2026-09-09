package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestRepo_Add_MarksNewFile(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "hello world")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	err := repo.Add(context.Background(), root, []string{"a.txt"})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, local.stageAdd)
}

func TestRepo_Add_DoesNotReadContent(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "original")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{
		Path: "a.txt", Hash: contentHash(t, "original"),
	}}}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	require.NoError(t, repo.Add(context.Background(), root, []string{"a.txt"}))
	require.Equal(t, []string{"a.txt"}, local.stageAdd, "an unchanged file is still marked for push")
}

func TestRepo_Add_DirectoryAndWholeRepo(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "a")
	writeRepoFile(t, root, "assets/logo.txt", "logo")
	writeRepoFile(t, root, "docs/readme.md", "readme")
	writeRepoFile(t, root, ".nipa/ignored.bin", "secret")
	writeRepoFile(t, root, "sub/.nipa/hidden.txt", "hidden")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	require.NoError(t, repo.Add(context.Background(), root, []string{""}))
	require.Equal(t, []string{"a.txt", "assets/logo.txt", "docs/readme.md"}, local.stageAdd, ".nipa must be excluded")
}

func TestRepo_Add_MissingPath(t *testing.T) {
	repo := NewRepo(nil, &stubRepoInterface{}, &stubLocalRepo{snapshot: &domain.Snapshot{}})
	err := repo.Add(context.Background(), t.TempDir(), []string{"nope.txt"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func TestRepo_Add_PathInsideNipa(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, ".nipa/config", "{}")
	repo := NewRepo(nil, &stubRepoInterface{}, &stubLocalRepo{snapshot: &domain.Snapshot{}})
	err := repo.Add(context.Background(), root, []string{".nipa/config"})
	require.Error(t, err)
}

func TestRepo_Add_PathOutsideRepo(t *testing.T) {
	repo := NewRepo(nil, &stubRepoInterface{}, &stubLocalRepo{snapshot: &domain.Snapshot{}})
	err := repo.Add(context.Background(), t.TempDir(), []string{"../outside.txt"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside")
}

func TestRepo_Add_InitError(t *testing.T) {
	wantErr := os.ErrInvalid
	repo := NewRepo(nil, &stubRepoInterface{}, &stubLocalRepo{initErr: wantErr, snapshot: &domain.Snapshot{}})
	err := repo.Add(context.Background(), t.TempDir(), []string{"a"})
	require.ErrorIs(t, err, wantErr)
}

func TestRepo_Remove_ExactAndPrefix(t *testing.T) {
	local := &stubLocalRepo{staged: []string{"a.txt", "b/c.txt", "b/d/e.txt"}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	require.NoError(t, repo.Remove(context.Background(), t.TempDir(), []string{"b"}))
	require.Equal(t, []string{"b/c.txt", "b/d/e.txt"}, local.stageRemove)
}

func TestRepo_Remove_FileNotStaged(t *testing.T) {
	local := &stubLocalRepo{}
	repo := NewRepo(nil, &stubRepoInterface{}, local)
	require.NoError(t, repo.Remove(context.Background(), t.TempDir(), []string{"a.txt"}))
	require.Empty(t, local.stageRemove)
}

func TestRepo_Remove_WholeRepo(t *testing.T) {
	local := &stubLocalRepo{staged: []string{"a.txt", "b/c.txt"}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)
	require.NoError(t, repo.Remove(context.Background(), t.TempDir(), []string{""}))
	require.Equal(t, []string{"a.txt", "b/c.txt"}, local.stageRemove)
}

func TestRepo_Remove_PathOutsideRepo(t *testing.T) {
	repo := NewRepo(nil, &stubRepoInterface{}, &stubLocalRepo{})
	err := repo.Remove(context.Background(), t.TempDir(), []string{"../outside.txt"})
	require.Error(t, err)
}

func TestRepo_Status(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "tracked.txt", "original")
	writeRepoFile(t, root, "modified.txt", "changed")
	writeRepoFile(t, root, "new.txt", "untracked")
	writeRepoFile(t, root, "staged.txt", "any content")
	writeRepoFile(t, root, "sub/.nipa/hidden.txt", "x")

	local := &stubLocalRepo{
		snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{
			{Path: "tracked.txt", Hash: contentHash(t, "original")},
			{Path: "modified.txt", Hash: contentHash(t, "original")},
			{Path: "missing.txt", Hash: contentHash(t, "whatever")},
		}},
		staged: []string{"staged.txt"},
	}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	st, err := repo.Status(context.Background(), root)
	require.NoError(t, err)

	require.Equal(t, []string{"staged.txt"}, st.Staged)
	require.Equal(t, []string{"missing.txt"}, st.Missing)
	require.Equal(t, []string{"modified.txt"}, st.Modified)
	require.Equal(t, []string{"new.txt"}, st.Untracked)
}

func TestRepo_Status_StagedFileIsNotListedTwice(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "staged.txt", "content")
	local := &stubLocalRepo{snapshot: &domain.Snapshot{}, staged: []string{"staged.txt"}}
	repo := NewRepo(nil, &stubRepoInterface{}, local)

	st, err := repo.Status(context.Background(), root)
	require.NoError(t, err)
	require.Equal(t, []string{"staged.txt"}, st.Staged)
	require.Empty(t, st.Modified)
	require.Empty(t, st.Untracked)
	require.Empty(t, st.Missing)
}

func contentHash(t *testing.T, content string) serverDomain.Hash {
	t.Helper()
	h, _, err := chunkFile([]byte(content))
	require.NoError(t, err)
	return h
}

func writeRepoFile(t *testing.T, root, path, content string) {
	t.Helper()
	fp := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
	require.NoError(t, os.WriteFile(fp, []byte(content), 0o644))
}

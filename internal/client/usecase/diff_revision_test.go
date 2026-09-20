package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubDiffClient struct {
	connectHost     string
	connectErr      error
	branches        map[string]*serverDomain.Branch
	commits         map[string]*clientDomain.CommitDetail
	mergeBase       *clientDomain.MergeBaseInfo
	mergeBaseErr    error
	lastMergeTarget clientDomain.MergeRef
	lastMergeSource clientDomain.MergeRef
	download        map[serverDomain.Hash][]byte
	downloaded      []serverDomain.Hash
	downloadErr     error
	branchLookups   []string
	commitLookups   []string
}

func (s *stubDiffClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubDiffClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	s.branchLookups = append(s.branchLookups, name)
	if branch, ok := s.branches[name]; ok {
		return branch, nil
	}
	return nil, &clientDomain.Error{Code: 404, Message: fmt.Sprintf("branch %q not found", name)}
}

func (s *stubDiffClient) GetCommit(_ context.Context, _, _, commitID string) (*clientDomain.CommitDetail, error) {
	s.commitLookups = append(s.commitLookups, commitID)
	if commit, ok := s.commits[commitID]; ok {
		return commit, nil
	}
	return nil, &clientDomain.Error{Code: 404, Message: fmt.Sprintf("commit %s not found", commitID)}
}

func (s *stubDiffClient) GetMergeBase(_ context.Context, _, _ string, target, source clientDomain.MergeRef) (*clientDomain.MergeBaseInfo, error) {
	s.lastMergeTarget, s.lastMergeSource = target, source
	return s.mergeBase, s.mergeBaseErr
}

func (s *stubDiffClient) DownloadChunks(_ context.Context, _ clientDomain.ChunkScope, hashes []serverDomain.Hash, onChunk func(h serverDomain.Hash, data []byte) error) error {
	s.downloaded = append(s.downloaded, hashes...)
	if s.downloadErr != nil {
		return s.downloadErr
	}
	for _, h := range hashes {
		data, ok := s.download[h]
		if !ok {
			continue
		}
		if err := onChunk(h, data); err != nil {
			return err
		}
	}
	return nil
}

func ptrID(id snow.ID) *snow.ID {
	return &id
}

func fileTree(t *testing.T, path, content string) *serverDomain.TreeNode {
	t.Helper()
	chunks, err := chunker.ChunkAll([]byte(content))
	require.NoError(t, err)
	hashes := make([]serverDomain.Hash, len(chunks))
	fileChunks := make([]serverDomain.Chunk, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
		fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
	}
	return &serverDomain.TreeNode{
		Name: "",
		FileChildren: []*serverDomain.File{{
			Name:      path,
			Mode:      2,
			SizeBytes: int64(len(content)),
			IsBinary:  chunker.IsBinary([]byte(content)),
			Hash:      chunker.FileHash(hashes),
			Chunks:    fileChunks,
		}},
	}
}

func contentChunks(t *testing.T, contents ...string) map[serverDomain.Hash][]byte {
	t.Helper()
	out := make(map[serverDomain.Hash][]byte)
	for _, content := range contents {
		chunks, err := chunker.ChunkAll([]byte(content))
		require.NoError(t, err)
		for _, c := range chunks {
			out[c.Hash] = c.Data
		}
	}
	return out
}

func diffAuth(t *testing.T) *Auth {
	t.Helper()
	return NewAuth(nil, &stubSecureStorage{loadResult: &clientDomain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
}

func TestDiff_RevisionVsWorking(t *testing.T) {
	root := t.TempDir()
	oldContent := "old line\n"
	commitID := snow.ID(5).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(5))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			commitID: {ID: commitID, Tree: fileTree(t, "a.txt", oldContent)},
		},
		download: contentChunks(t, oldContent),
	}
	writeRepoFile(t, root, "a.txt", "new line\n")

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"feature"}, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []byte(oldContent), files[0].Old)
	require.Equal(t, []byte("new line\n"), files[0].New)
	require.False(t, files[0].OldUnavailable)
	require.Equal(t, []string{"feature"}, client.branchLookups)
	require.Equal(t, []string{commitID}, client.commitLookups)
	require.NotEmpty(t, client.downloaded)
}

func TestDiff_RevisionVsRevision(t *testing.T) {
	root := t.TempDir()
	oldContent := "one\n"
	newContent := "one\ntwo\n"
	mainID := snow.ID(5).Base36()
	featureID := snow.ID(6).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main":    {Name: "main", CommitID: ptrID(snow.ID(5))},
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(6))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			mainID:    {ID: mainID, Tree: fileTree(t, "a.txt", oldContent)},
			featureID: {ID: featureID, Tree: fileTree(t, "a.txt", newContent)},
		},
		download: contentChunks(t, oldContent, newContent),
	}

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"main", "feature"}, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
	require.Equal(t, []byte(oldContent), files[0].Old)
	require.Equal(t, []byte(newContent), files[0].New)
}

func TestDiff_CommitIDRevision(t *testing.T) {
	root := t.TempDir()
	content := "hello\n"
	commitID := snow.ID(5).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		commits: map[string]*clientDomain.CommitDetail{
			commitID: {ID: commitID, Tree: fileTree(t, "a.txt", content)},
		},
		download: contentChunks(t, content),
	}
	writeRepoFile(t, root, "a.txt", content)

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{commitID}, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
	require.Equal(t, []string{commitID}, client.branchLookups)
	require.Equal(t, []string{commitID}, client.commitLookups)
}

func TestDiff_RevisionNotFound(t *testing.T) {
	repo := newDiffStub()
	client := &stubDiffClient{}
	_, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), t.TempDir(), []string{"nope"}, DiffOptions{})
	require.ErrorContains(t, err, `revision "nope" not found`)
}

func TestDiff_HeadUsesPinnedCommit(t *testing.T) {
	root := t.TempDir()
	content := "pinned\n"
	commitID := snow.ID(7).Base36()
	repo := newDiffStub()
	repo.commit = &clientDomain.LocalCommit{CommitID: commitID, CommitHash: "beef"}
	client := &stubDiffClient{
		commits: map[string]*clientDomain.CommitDetail{
			commitID: {ID: commitID, Tree: fileTree(t, "a.txt", content)},
		},
		download: contentChunks(t, content),
	}
	writeRepoFile(t, root, "a.txt", content)

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"HEAD"}, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
	require.Empty(t, client.branchLookups)
	require.Equal(t, []string{commitID}, client.commitLookups)
}

func TestDiff_HeadFallsBackToBranch(t *testing.T) {
	root := t.TempDir()
	content := "fallback\n"
	commitID := snow.ID(7).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main": {Name: "main", CommitID: ptrID(snow.ID(7))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			commitID: {ID: commitID, Tree: fileTree(t, "a.txt", content)},
		},
		download: contentChunks(t, content),
	}
	writeRepoFile(t, root, "a.txt", content)

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"@"}, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
	require.Equal(t, []string{"main"}, client.branchLookups)
}

func TestDiff_MergeBase(t *testing.T) {
	root := t.TempDir()
	baseContent := "base\n"
	featureContent := "base\nfeature\n"
	mainID := snow.ID(5).Base36()
	featureID := snow.ID(6).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main":    {Name: "main", CommitID: ptrID(snow.ID(5))},
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(6))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			mainID:    {ID: mainID, Tree: fileTree(t, "a.txt", baseContent)},
			featureID: {ID: featureID, Tree: fileTree(t, "a.txt", featureContent)},
		},
		mergeBase: &clientDomain.MergeBaseInfo{
			MergeBaseTree: fileTree(t, "a.txt", baseContent),
		},
		download: contentChunks(t, baseContent, featureContent),
	}

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"main", "feature"}, DiffOptions{MergeBase: true})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, []byte(baseContent), files[0].Old)
	require.Equal(t, []byte(featureContent), files[0].New)
	require.Equal(t, mainID, client.lastMergeTarget.CommitID)
	require.Equal(t, featureID, client.lastMergeSource.CommitID)
}

func TestDiff_MergeBaseEmptySide(t *testing.T) {
	root := t.TempDir()
	content := "hello\n"
	mainID := snow.ID(5).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main":  {Name: "main", CommitID: ptrID(snow.ID(5))},
			"empty": {Name: "empty"},
		},
		commits: map[string]*clientDomain.CommitDetail{
			mainID: {ID: mainID, Tree: fileTree(t, "a.txt", content)},
		},
		download: contentChunks(t, content),
	}

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"empty", "main"}, DiffOptions{MergeBase: true})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, diff.Added, files[0].Change.Status)
	require.Equal(t, clientDomain.MergeRef{}, client.lastMergeTarget, "an empty side has no merge base to resolve")
}

func TestDiff_EmptyBranchRevision(t *testing.T) {
	root := t.TempDir()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"empty": {Name: "empty"},
		},
	}
	writeRepoFile(t, root, "loose.txt", "hello\n")

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"empty"}, DiffOptions{})
	require.NoError(t, err)
	require.Empty(t, files)
	require.Empty(t, client.commitLookups)
}

func TestDiff_BinaryRevision(t *testing.T) {
	root := t.TempDir()
	oldContent := "old\x00data"
	newContent := "new\x00data"
	mainID := snow.ID(5).Base36()
	featureID := snow.ID(6).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main":    {Name: "main", CommitID: ptrID(snow.ID(5))},
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(6))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			mainID:    {ID: mainID, Tree: fileTree(t, "img.bin", oldContent)},
			featureID: {ID: featureID, Tree: fileTree(t, "img.bin", newContent)},
		},
	}

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"main", "feature"}, DiffOptions{})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.True(t, files[0].Change.Old.IsBinary)
	require.False(t, files[0].OldUnavailable)
	require.False(t, files[0].NewUnavailable)
	require.Empty(t, client.downloaded, "binary content is not needed to render the binary line")

	patch := diff.Patch(files, diff.Options{Context: diff.DefaultContext})
	require.Contains(t, patch, "Binary files a/img.bin and b/img.bin differ")
}

func TestDiff_RevisionDownloadError(t *testing.T) {
	root := t.TempDir()
	content := "old\n"
	commitID := snow.ID(5).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(5))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			commitID: {ID: commitID, Tree: fileTree(t, "a.txt", content)},
		},
		downloadErr: errors.New("network down"),
	}
	writeRepoFile(t, root, "a.txt", "new\n")

	_, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"feature"}, DiffOptions{})
	require.ErrorContains(t, err, "network down")
}

func TestDiff_ConnectError(t *testing.T) {
	repo := newDiffStub()
	client := &stubDiffClient{connectErr: errors.New("no route")}
	_, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), t.TempDir(), []string{"main"}, DiffOptions{})
	require.ErrorContains(t, err, "no route")
}

func TestDiff_TooManyRevisions(t *testing.T) {
	repo := newDiffStub()
	client := &stubDiffClient{}
	_, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), t.TempDir(), []string{"a", "b", "c"}, DiffOptions{})
	require.ErrorContains(t, err, "too many revisions")
}

func TestDiff_MergeBaseRequiresTwoRevisions(t *testing.T) {
	repo := newDiffStub()
	client := &stubDiffClient{}
	_, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), t.TempDir(), nil, DiffOptions{MergeBase: true})
	require.ErrorContains(t, err, "--merge-base requires two revisions")
	_, err = NewDiff(diffAuth(t), client, repo).Run(context.Background(), t.TempDir(), []string{"main"}, DiffOptions{MergeBase: true})
	require.ErrorContains(t, err, "--merge-base requires two revisions")
}

func TestDiff_BinaryOptionLoadsContent(t *testing.T) {
	root := t.TempDir()
	oldContent := "old\x00data"
	newContent := "new\x00data"
	mainID := snow.ID(5).Base36()
	featureID := snow.ID(6).Base36()
	repo := newDiffStub()
	client := &stubDiffClient{
		branches: map[string]*serverDomain.Branch{
			"main":    {Name: "main", CommitID: ptrID(snow.ID(5))},
			"feature": {Name: "feature", CommitID: ptrID(snow.ID(6))},
		},
		commits: map[string]*clientDomain.CommitDetail{
			mainID:    {ID: mainID, Tree: fileTree(t, "img.bin", oldContent)},
			featureID: {ID: featureID, Tree: fileTree(t, "img.bin", newContent)},
		},
		download: contentChunks(t, oldContent, newContent),
	}

	files, err := NewDiff(diffAuth(t), client, repo).Run(context.Background(), root, []string{"main", "feature"}, DiffOptions{Binary: true})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, []byte(oldContent), files[0].Old)
	require.Equal(t, []byte(newContent), files[0].New)
	require.NotEmpty(t, client.downloaded)
}

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func getPrompt(t *testing.T, session *mcp.ClientSession, name string, args map[string]string) (string, error) {
	t.Helper()
	res, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		return "", err
	}
	require.Len(t, res.Messages, 1)
	require.Equal(t, mcp.Role("user"), res.Messages[0].Role)
	text, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	require.NotEmpty(t, res.Description)
	return text.Text, nil
}

func TestPrompts_Listed(t *testing.T) {
	session := connect(t, Options{NewUseCases: offlineUseCases})
	res, err := session.ListPrompts(context.Background(), nil)
	require.NoError(t, err)
	byName := map[string]*mcp.Prompt{}
	for _, p := range res.Prompts {
		byName[p.Name] = p
	}
	for _, name := range []string{"nipa_commit_message", "nipa_mr_description", "nipa_review_mr"} {
		require.Contains(t, byName, name)
	}
	review := byName["nipa_review_mr"]
	require.Len(t, review.Arguments, 2)
	require.True(t, review.Arguments[1].Required)
}

func TestCommitMessagePrompt(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "a.txt", "one\ntwo\n")
	writeFile(t, root, "b.txt", "unstaged\n")
	stageFile(t, root, "a.txt")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	text, err := getPrompt(t, session, "nipa_commit_message", map[string]string{"repo": root})
	require.NoError(t, err)
	require.Contains(t, text, "Output only the commit message")
	require.Contains(t, text, "\n---\n")
	require.Contains(t, text, `staged on branch "main"`)
	require.Contains(t, text, "M a.txt\n")
	require.Contains(t, text, "+two")
	require.NotContains(t, text, "b.txt")

	empty := setupRepo(t, map[string]string{"a.txt": "one\n"})
	_, err = getPrompt(t, session, "nipa_commit_message", map[string]string{"repo": empty})
	require.ErrorContains(t, err, "nothing staged")
	require.ErrorContains(t, err, "next: nipa add <path>")

	_, err = getPrompt(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_commit_message", map[string]string{"repo": root})
	require.ErrorContains(t, err, "diff is not configured")
	_, err = getPrompt(t, session, "nipa_commit_message", map[string]string{"repo": t.TempDir()})
	require.ErrorContains(t, err, "not a nipa repository")
}

func TestMRDescriptionPrompt(t *testing.T) {
	root := setupRepo(t, nil)
	diffClient := revisionClient()
	diffClient.mergeBase = "9"
	repo := fakeRepoClient{
		branches: []*serverDomain.Branch{{Name: "main", IsDefault: true}, {Name: "feature"}},
		log: []*serverDomain.CommitLogEntry{
			{Commit: serverDomain.Commit{ID: 11, Message: "add c\n\nbody"}, AuthorName: "alice"},
			{Commit: serverDomain.Commit{ID: 9, Message: "base"}},
		},
	}
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Diff:      usecase.NewDiff(loggedInAuth(), diffClient, localrepo.NewLocalRepo()),
			Repo:      usecase.NewRepo(loggedInAuth(), repo, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})

	text, err := getPrompt(t, session, "nipa_mr_description", map[string]string{"repo": root, "source": "feature"})
	require.NoError(t, err)
	require.Contains(t, text, `from "feature" into "main"`)
	require.Contains(t, text, "- add c (alice)\n")
	require.NotContains(t, text, "- base")
	require.Contains(t, text, "A c.txt\n")
	require.Contains(t, text, "first line is the title")

	_, err = getPrompt(t, session, "nipa_mr_description", map[string]string{"repo": root})
	require.ErrorContains(t, err, "must differ")
}

func TestReviewMRPrompt(t *testing.T) {
	root := setupRepo(t, nil)
	mrClient := &describeMRClient{
		mr:           &domain.MergeRequest{Number: 3, Title: "Add c", SourceBranch: "feature", TargetBranch: "main", Status: "open"},
		mergeability: &domain.Mergeability{SourceCommitID: "b", MergeBaseCommitID: "a"},
	}
	mrClient.diff = []*domain.MergeRequestDiffFile{
		{Path: "a.txt", Status: "M", Patch: []string{"@@ -1 +1,2 @@", " one", "+two"}},
		{Path: "c.txt", Status: "A", Patch: []string{"@@ -0,0 +1 @@", "+c"}},
	}
	withDiff := func() UseCases {
		return UseCases{
			MR:   usecase.NewMergeRequest(loggedInAuth(), mrClient, localrepo.NewLocalRepo()),
			Diff: usecase.NewDiff(loggedInAuth(), revisionClient(), localrepo.NewLocalRepo()),
		}
	}
	session := connect(t, Options{NewUseCases: withDiff})

	text, err := getPrompt(t, session, "nipa_review_mr", map[string]string{"repo": root, "number": "3"})
	require.NoError(t, err)
	require.Contains(t, text, "Review merge request #3.")
	require.Contains(t, text, "title: Add c")
	require.Contains(t, text, "Do not approve")
	require.Contains(t, text, "- [unresolved] a.txt\n")
	require.Contains(t, text, "- m\n")
	require.Contains(t, text, "+two")

	for _, bad := range []string{"", "x", "0"} {
		_, err = getPrompt(t, session, "nipa_review_mr", map[string]string{"repo": root, "number": bad})
		require.ErrorContains(t, err, "positive merge request number")
	}
	mrClient.viewErr = errors.New("view failed")
	_, err = getPrompt(t, session, "nipa_review_mr", map[string]string{"repo": root, "number": "3"})
	require.ErrorContains(t, err, "view failed")

	noDiff := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), mrClient, localrepo.NewLocalRepo())}
	}})
	_, err = getPrompt(t, noDiff, "nipa_review_mr", map[string]string{"repo": root, "number": "3"})
	require.ErrorContains(t, err, "diff is not configured")
	_, err = getPrompt(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_review_mr", map[string]string{"repo": root, "number": "3"})
	require.ErrorContains(t, err, "merge requests are not configured")
}

func TestPromptArgs_Nil(t *testing.T) {
	require.Nil(t, promptArgs(nil))
	require.Nil(t, promptArgs(&mcp.GetPromptRequest{}))
}

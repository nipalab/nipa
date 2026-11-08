package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func connectAs(t *testing.T, opts Options, impl *mcp.Implementation) *mcp.ClientSession {
	t.Helper()
	srv := NewServer(opts)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(impl, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	require.False(t, res.IsError, "tool failed: %s", toolText(t, res))
	var out T
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &out))
	return out
}

type recordingMRClient struct {
	fakeMRClient
	err          error
	descriptions []string
	bodies       []string
	draft        *bool
	file         string
	newLine      *int64
	resolved     *bool
	strategy     string
	deleteSource bool
	commit       string
	diff         []*domain.MergeRequestDiffFile
}

func (r *recordingMRClient) CreateMergeRequest(_ context.Context, _, _ string, in domain.NewMergeRequest) (*domain.MergeRequest, error) {
	r.descriptions = append(r.descriptions, in.Description)
	return &domain.MergeRequest{Number: 2, Title: in.Title, Description: in.Description, Draft: in.Draft, Status: domain.MergeRequestOpen}, nil
}

func (r *recordingMRClient) UpdateMergeRequest(_ context.Context, _, _ string, number int64, title, description string, draft *bool) (*domain.MergeRequest, error) {
	r.descriptions = append(r.descriptions, description)
	r.draft = draft
	return &domain.MergeRequest{Number: number, Title: title, Description: description, Status: domain.MergeRequestOpen}, nil
}

func (r *recordingMRClient) GetMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	if r.err != nil {
		return nil, nil, r.err
	}
	return &domain.MergeRequest{Number: number, Title: "View", Status: domain.MergeRequestOpen},
		&domain.Mergeability{Status: "mergeable", BlockedBy: "status_checks"}, nil
}

func (r *recordingMRClient) ListMergeRequestReviews(context.Context, string, string, int64) ([]*domain.MergeRequestReview, error) {
	return []*domain.MergeRequestReview{{ID: "r1", State: "approved"}}, r.err
}

func (r *recordingMRClient) ListMergeRequestChecks(context.Context, string, string, int64) ([]*domain.MergeRequestCheck, error) {
	return []*domain.MergeRequestCheck{{ID: "c1", Name: "ci", State: "success"}}, r.err
}

func (r *recordingMRClient) GetMergeRequestDiff(context.Context, string, string, int64) ([]*domain.MergeRequestDiffFile, error) {
	return r.diff, r.err
}

func (r *recordingMRClient) ListMergeRequestThreads(context.Context, string, string, int64) ([]*domain.MergeRequestThread, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []*domain.MergeRequestThread{{ID: "t1", FilePath: "a.txt"}}, nil
}

func (r *recordingMRClient) ListMergeRequestCommits(context.Context, string, string, int64) ([]*serverDomain.CommitLogEntry, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []*serverDomain.CommitLogEntry{{Commit: serverDomain.Commit{ID: 9001, Message: "m", CreatedAt: time.Unix(0, 0)}}}, nil
}

func (r *recordingMRClient) ListMergeRequestTimeline(context.Context, string, string, int64) ([]*domain.MergeRequestTimelineItem, error) {
	if r.err != nil {
		return nil, r.err
	}
	return []*domain.MergeRequestTimelineItem{{ID: "e1", Kind: "opened"}}, nil
}

func (r *recordingMRClient) AddMergeRequestComment(_ context.Context, _, _ string, _ int64, file string, _, newLine *int64, body string) (*domain.MergeRequestThread, error) {
	r.file, r.newLine = file, newLine
	r.bodies = append(r.bodies, body)
	return &domain.MergeRequestThread{ID: "t2", FilePath: file}, nil
}

func (r *recordingMRClient) ReplyMergeRequestThread(_ context.Context, _, _ string, _ int64, _, body string) (*domain.MergeRequestComment, error) {
	r.bodies = append(r.bodies, body)
	return &domain.MergeRequestComment{ID: "c2", Body: body}, nil
}

func (r *recordingMRClient) ResolveMergeRequestThread(_ context.Context, _, _ string, _ int64, threadID string, resolved bool) (*domain.MergeRequestThread, error) {
	r.resolved = &resolved
	return &domain.MergeRequestThread{ID: threadID, Resolved: resolved}, nil
}

func (r *recordingMRClient) SubmitMergeRequestReview(_ context.Context, _, _ string, _ int64, state, body string) (*domain.MergeRequestReview, error) {
	r.bodies = append(r.bodies, body)
	return &domain.MergeRequestReview{ID: "r2", State: state, Body: body}, nil
}

func (r *recordingMRClient) RequestMergeRequestReview(_ context.Context, _, _ string, _ int64, userID string) (*domain.MergeRequestReviewRequest, error) {
	return &domain.MergeRequestReviewRequest{ID: "rr1", Reviewer: domain.ReviewActor{UserID: userID}}, nil
}

func (r *recordingMRClient) ReportMergeRequestCheck(_ context.Context, _, _ string, _ int64, name, state, _, commit string) (*domain.MergeRequestCheck, error) {
	r.commit = commit
	return &domain.MergeRequestCheck{ID: "c3", Name: name, State: state}, nil
}

func (r *recordingMRClient) MergeMergeRequest(_ context.Context, _, _ string, number int64, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	r.strategy, r.deleteSource = strategy, deleteSource
	return &domain.MergeRequest{Number: number, Status: domain.MergeRequestMerged}, &domain.Mergeability{Status: "merged"}, nil
}

func mrUseCases(client *recordingMRClient) func() UseCases {
	return func() UseCases {
		return UseCases{MR: usecase.NewMergeRequest(loggedInAuth(), client, localrepo.NewLocalRepo())}
	}
}

func TestAllowCaps_RegisterToolsPerCapability(t *testing.T) {
	groups := map[string][]string{
		CapStage:  {"nipa_add", "nipa_restore"},
		CapPush:   {"nipa_push"},
		CapSync:   {"nipa_update", "nipa_switch"},
		CapBranch: {"nipa_branch_create", "nipa_tag_create"},
		CapLock:   {"nipa_lock", "nipa_unlock"},
		CapMR: {"nipa_mr_create", "nipa_mr_update", "nipa_mr_comment", "nipa_mr_reply", "nipa_mr_resolve",
			"nipa_mr_review", "nipa_mr_request_review", "nipa_mr_check"},
		CapMerge: {"nipa_merge", "nipa_resolve_conflict", "nipa_mr_merge"},
	}
	reads := []string{"nipa_status", "nipa_diff", "nipa_log", "nipa_branch_list", "nipa_mr_list", "nipa_lock_list",
		"nipa_lock_query", "nipa_tag_list", "nipa_conflicts", "nipa_mr_view", "nipa_mr_diff", "nipa_mr_threads",
		"nipa_mr_commits", "nipa_mr_timeline"}
	for capability := range groups {
		t.Run(capability, func(t *testing.T) {
			names := toolNames(t, connect(t, Options{Allow: map[string]bool{capability: true}}))
			for _, name := range reads {
				require.True(t, names[name], name)
			}
			for other, otherTools := range groups {
				for _, name := range otherTools {
					require.Equal(t, other == capability, names[name], name)
				}
			}
		})
	}
}

func TestToolAnnotations(t *testing.T) {
	session := connect(t, Options{Allow: AllCaps()})
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	destructiveTools := map[string]bool{"nipa_push": true, "nipa_merge": true, "nipa_mr_merge": true, "nipa_restore": true,
		"nipa_switch": true, "nipa_update": true, "nipa_unlock": true, "nipa_resolve_conflict": true}
	for _, tool := range res.Tools {
		require.NotNil(t, tool.Annotations, tool.Name)
		if tool.Annotations.ReadOnlyHint {
			continue
		}
		require.NotNil(t, tool.Annotations.DestructiveHint, tool.Name)
		require.Equal(t, destructiveTools[tool.Name], *tool.Annotations.DestructiveHint, tool.Name)
	}
}

func TestParseAllow(t *testing.T) {
	caps, err := ParseAllow(" stage, PUSH ,,mr")
	require.NoError(t, err)
	require.Equal(t, map[string]bool{CapStage: true, CapPush: true, CapMR: true}, caps)

	caps, err = ParseAllow("")
	require.NoError(t, err)
	require.Empty(t, caps)

	_, err = ParseAllow("stage,admin")
	require.ErrorContains(t, err, `unknown capability "admin"`)
	require.ErrorContains(t, err, "branch, lock, merge, mr, push, stage, sync")

	require.Len(t, AllCaps(), 7)
}

func TestCapDiff(t *testing.T) {
	line := func(text string) output.DiffLine { return output.DiffLine{Kind: "add", Text: text} }
	mk := func() output.Diff {
		return output.Diff{Changes: []output.DiffFile{
			{Path: "a.txt", Hunks: []output.DiffHunk{
				{Lines: []output.DiffLine{line(strings.Repeat("x", 100)), line(strings.Repeat("y", 100))}},
				{Lines: []output.DiffLine{line("z")}},
			}},
			{Path: "b.txt", Hunks: []output.DiffHunk{{Lines: []output.DiffLine{line("b")}}}},
			{Path: "c.txt"},
		}}
	}

	full := capDiff(mk(), 1<<20)
	require.Len(t, full.Changes, 3)
	require.Zero(t, full.OmittedFiles)
	require.False(t, full.Changes[0].Truncated)

	cut := capDiff(mk(), fileOverhead+len("a.txt")+hunkOverhead+lineOverhead+150)
	require.Len(t, cut.Changes, 1)
	require.True(t, cut.Changes[0].Truncated)
	require.Len(t, cut.Changes[0].Hunks, 1)
	require.Len(t, cut.Changes[0].Hunks[0].Lines, 1)
	require.Equal(t, 2, cut.OmittedFiles)

	headerOnly := capDiff(mk(), 10)
	require.Len(t, headerOnly.Changes, 1)
	require.Empty(t, headerOnly.Changes[0].Hunks)
	require.True(t, headerOnly.Changes[0].Truncated)
	require.Equal(t, 2, headerOnly.OmittedFiles)
}

func TestCapMRDiff(t *testing.T) {
	files := []*domain.MergeRequestDiffFile{
		{Path: "a.txt", Patch: []string{"@@ -1 +1 @@", strings.Repeat("+", 200)}},
		nil,
		{Path: "b.txt", Patch: []string{"+b"}},
	}
	full := capMRDiff(files, 1<<20)
	require.Len(t, full.Files, 2)
	require.Zero(t, full.OmittedFiles)

	cut := capMRDiff(files, fileOverhead+len("a.txt")+lineOverhead+20)
	require.Len(t, cut.Files, 1)
	require.True(t, cut.Files[0].Truncated)
	require.Len(t, cut.Files[0].Patch, 1)
	require.Equal(t, 1, cut.OmittedFiles)
}

func TestBudgetHelpers(t *testing.T) {
	b, err := diffBudget(0)
	require.NoError(t, err)
	require.Equal(t, defaultDiffBytes, b)
	b, err = diffBudget(10 << 20)
	require.NoError(t, err)
	require.Equal(t, maxDiffBytes, b)
	b, err = diffBudget(1000)
	require.NoError(t, err)
	require.Equal(t, 1000, b)
	_, err = diffBudget(-1)
	require.ErrorContains(t, err, "max_bytes must not be negative")

	require.Equal(t, 50, clampLimit(0, 50, 500))
	require.Equal(t, 500, clampLimit(9999, 50, 500))
	require.Equal(t, 7, clampLimit(7, 50, 500))
}

func TestDiff_MaxBytes(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n", "b.txt": "two\n"})
	writeFile(t, root, "a.txt", strings.Repeat("changed line\n", 50))
	writeFile(t, root, "b.txt", "TWO\n")
	session := connect(t, Options{NewUseCases: offlineUseCases})

	d := decode[output.Diff](t, call(t, session, "nipa_diff", map[string]any{"repo": root, "max_bytes": 300}))
	require.Len(t, d.Changes, 1)
	require.True(t, d.Changes[0].Truncated)
	require.Equal(t, 1, d.OmittedFiles)

	res := call(t, session, "nipa_diff", map[string]any{"repo": root, "max_bytes": -5})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "max_bytes must not be negative")
}

type pagingRepoClient struct {
	fakeRepoClient
}

func (pagingRepoClient) GetCommitLog(_ context.Context, _, _, _ string, _ *snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error) {
	page := make([]*serverDomain.CommitLogEntry, limit)
	for i := range page {
		page[i] = &serverDomain.CommitLogEntry{Commit: serverDomain.Commit{ID: snow.ID(i + 1)}}
	}
	return page, nil
}

func TestLog_DefaultAndMaxLimit(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(loggedInAuth(), pagingRepoClient{}, localrepo.NewLocalRepo()),
			Connector: fakeConnector{},
		}
	}})
	log := decode[output.Log](t, call(t, session, "nipa_log", map[string]any{"repo": root}))
	require.Len(t, log.Commits, defaultLogLimit)
	log = decode[output.Log](t, call(t, session, "nipa_log", map[string]any{"repo": root, "limit": 100000}))
	require.Len(t, log.Commits, maxLogLimit)
}

func TestMRReadTools(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingMRClient{diff: []*domain.MergeRequestDiffFile{
		{Path: "a.txt", Status: "M", Patch: []string{"@@ -1 +1 @@", "-a", "+b"}},
		{Path: "b.txt", Status: "A", Patch: []string{"+x"}},
	}}
	session := connect(t, Options{NewUseCases: mrUseCases(client)})

	view := decode[mrViewOutput](t, call(t, session, "nipa_mr_view", map[string]any{"repo": root, "number": 3}))
	require.Equal(t, int64(3), view.MergeRequest.Number)
	require.Equal(t, "status_checks", view.Mergeability.BlockedBy)
	require.Len(t, view.Reviews, 1)
	require.Equal(t, "ci", view.Checks[0].Name)

	d := decode[mrDiffOutput](t, call(t, session, "nipa_mr_diff", map[string]any{"repo": root, "number": 3}))
	require.Len(t, d.Files, 2)
	require.Equal(t, []string{"@@ -1 +1 @@", "-a", "+b"}, d.Files[0].Patch)

	d = decode[mrDiffOutput](t, call(t, session, "nipa_mr_diff", map[string]any{"repo": root, "number": 3, "max_bytes": 150}))
	require.Len(t, d.Files, 1)
	require.True(t, d.Files[0].Truncated)
	require.Equal(t, 1, d.OmittedFiles)

	threads := decode[output.MergeRequestThreads](t, call(t, session, "nipa_mr_threads", map[string]any{"repo": root, "number": 3}))
	require.Equal(t, "t1", threads.Threads[0].ID)

	commits := decode[output.Log](t, call(t, session, "nipa_mr_commits", map[string]any{"repo": root, "number": 3}))
	require.Equal(t, "6y1", commits.Commits[0].ID)

	timeline := decode[output.MergeRequestTimeline](t, call(t, session, "nipa_mr_timeline", map[string]any{"repo": root, "number": 3}))
	require.Equal(t, "opened", timeline.Timeline[0].Kind)

	res := call(t, session, "nipa_mr_view", map[string]any{"repo": root, "number": 0})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "invalid merge request number")

	res = call(t, session, "nipa_mr_diff", map[string]any{"repo": root, "number": 3, "max_bytes": -1})
	require.True(t, res.IsError)
}

func TestMRReadTools_Errors(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingMRClient{err: errors.New("server exploded")}
	session := connect(t, Options{NewUseCases: mrUseCases(client)})
	for _, tool := range []string{"nipa_mr_view", "nipa_mr_diff", "nipa_mr_threads", "nipa_mr_commits", "nipa_mr_timeline"} {
		res := call(t, session, tool, map[string]any{"repo": root, "number": 1})
		require.True(t, res.IsError, tool)
		require.Contains(t, toolText(t, res), "server exploded", tool)
	}

	unconfigured := connect(t, Options{Allow: AllCaps(), NewUseCases: func() UseCases { return UseCases{} }})
	extra := map[string]map[string]any{
		"nipa_mr_view": {}, "nipa_mr_diff": {}, "nipa_mr_threads": {}, "nipa_mr_commits": {}, "nipa_mr_timeline": {},
		"nipa_mr_update": {}, "nipa_mr_merge": {},
		"nipa_mr_comment":        {"body": "b"},
		"nipa_mr_reply":          {"thread_id": "t", "body": "b"},
		"nipa_mr_resolve":        {"thread_id": "t"},
		"nipa_mr_review":         {"state": "approved", "body": "b"},
		"nipa_mr_request_review": {"user_id": "u"},
		"nipa_mr_check":          {"name": "ci", "state": "success"},
	}
	for tool, args := range extra {
		args["repo"], args["number"] = root, 1
		res := call(t, unconfigured, tool, args)
		require.True(t, res.IsError, tool)
		require.Contains(t, toolText(t, res), "merge requests are not configured", tool)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	res := call(t, unconfigured, "nipa_mr_view", map[string]any{"repo": missing, "number": 1})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "not a nipa repository")
}

func TestMRWriteTools_AgentTrailer(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingMRClient{}
	session := connectAs(t, Options{Allow: map[string]bool{CapMR: true, CapMerge: true}, NewUseCases: mrUseCases(client)},
		&mcp.Implementation{Name: "claude-code", Version: "2.1"})
	const trailer = "\n\nNipa-Agent: claude-code/2.1"

	mr := decode[output.MergeRequest](t, call(t, session, "nipa_mr_create", map[string]any{"repo": root, "title": "T", "source": "feature", "description": "desc\n", "draft": true}))
	require.Equal(t, "desc"+trailer, mr.Description)
	require.True(t, mr.Draft)

	decode[output.MergeRequest](t, call(t, session, "nipa_mr_create", map[string]any{"repo": root, "title": "T", "source": "feature"}))
	require.Equal(t, "", client.descriptions[1])

	decode[output.MergeRequest](t, call(t, session, "nipa_mr_update", map[string]any{"repo": root, "number": 2, "draft": false}))
	require.Equal(t, "", client.descriptions[2])
	require.NotNil(t, client.draft)
	require.False(t, *client.draft)

	decode[output.MergeRequest](t, call(t, session, "nipa_mr_update", map[string]any{"repo": root, "number": 2, "description": "already\n\nNipa-Agent: other/1"}))
	require.Equal(t, "already\n\nNipa-Agent: other/1", client.descriptions[3])

	thread := decode[domain.MergeRequestThread](t, call(t, session, "nipa_mr_comment", map[string]any{"repo": root, "number": 2, "body": "nit", "file": "a.txt", "new_line": 4}))
	require.Equal(t, "a.txt", thread.FilePath)
	require.Equal(t, int64(4), *client.newLine)

	decode[domain.MergeRequestComment](t, call(t, session, "nipa_mr_reply", map[string]any{"repo": root, "number": 2, "thread_id": "t2", "body": "done"}))
	decode[domain.MergeRequestReview](t, call(t, session, "nipa_mr_review", map[string]any{"repo": root, "number": 2, "state": "approved", "body": "lgtm"}))
	require.Equal(t, []string{"nit" + trailer, "done" + trailer, "lgtm" + trailer}, client.bodies)

	resolved := decode[domain.MergeRequestThread](t, call(t, session, "nipa_mr_resolve", map[string]any{"repo": root, "number": 2, "thread_id": "t2"}))
	require.True(t, resolved.Resolved)
	decode[domain.MergeRequestThread](t, call(t, session, "nipa_mr_resolve", map[string]any{"repo": root, "number": 2, "thread_id": "t2", "unresolve": true}))
	require.False(t, *client.resolved)

	rr := decode[domain.MergeRequestReviewRequest](t, call(t, session, "nipa_mr_request_review", map[string]any{"repo": root, "number": 2, "user_id": "u9"}))
	require.Equal(t, "u9", rr.Reviewer.UserID)

	check := decode[output.MergeRequestCheck](t, call(t, session, "nipa_mr_check", map[string]any{"repo": root, "number": 2, "name": "ci", "state": "success", "commit": "6y1"}))
	require.Equal(t, "ci", check.Name)
	require.Equal(t, "6y1", client.commit)

	merged := decode[mrMergeOutput](t, call(t, session, "nipa_mr_merge", map[string]any{"repo": root, "number": 2, "strategy": "squash", "delete_source": true}))
	require.Equal(t, "merged", merged.MergeRequest.Status)
	require.Equal(t, "squash", client.strategy)
	require.True(t, client.deleteSource)
}

func TestMRWriteTools_Validation(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingMRClient{}
	session := connect(t, Options{Allow: AllCaps(), NewUseCases: mrUseCases(client)})
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"nipa_mr_update", map[string]any{"number": 1}, "pass --title"},
		{"nipa_mr_comment", map[string]any{"number": 1, "body": "x", "new_line": 3}, "need a file"},
		{"nipa_mr_comment", map[string]any{"number": 1, "body": " "}, "a comment needs a message"},
		{"nipa_mr_reply", map[string]any{"number": 1, "thread_id": "t", "body": ""}, "a reply needs a message"},
		{"nipa_mr_resolve", map[string]any{"number": 1, "thread_id": " "}, "a thread id is required"},
		{"nipa_mr_review", map[string]any{"number": 1, "state": "meh", "body": "b"}, "state must be one of"},
		{"nipa_mr_request_review", map[string]any{"number": 0, "user_id": "u"}, "invalid merge request number"},
		{"nipa_mr_check", map[string]any{"number": 1, "name": " ", "state": "success"}, "a check name is required"},
		{"nipa_mr_merge", map[string]any{"number": 1, "strategy": "octopus"}, "strategy must be one of"},
		{"nipa_mr_create", map[string]any{"title": ""}, "a title is required"},
	}
	for _, tc := range cases {
		tc.args["repo"] = root
		res := call(t, session, tc.tool, tc.args)
		require.True(t, res.IsError, tc.tool)
		require.Contains(t, toolText(t, res), tc.want, tc.tool)
	}
}

func TestAgentTrailer_Variants(t *testing.T) {
	root := setupRepo(t, nil)

	client := &recordingMRClient{}
	noVersion := connectAs(t, Options{Allow: AllCaps(), NewUseCases: mrUseCases(client)}, &mcp.Implementation{Name: "agent"})
	decode[output.MergeRequest](t, call(t, noVersion, "nipa_mr_create", map[string]any{"repo": root, "title": "T", "source": "feature", "description": "d"}))
	require.Equal(t, "d\n\nNipa-Agent: agent", client.descriptions[0])

	client = &recordingMRClient{}
	disabled := connect(t, Options{Allow: AllCaps(), NoAgentTrailer: true, NewUseCases: mrUseCases(client)})
	decode[output.MergeRequest](t, call(t, disabled, "nipa_mr_create", map[string]any{"repo": root, "title": "T", "source": "feature", "description": "d"}))
	require.Equal(t, "d", client.descriptions[0])

	client = &recordingMRClient{}
	anonymous := connectAs(t, Options{Allow: AllCaps(), NewUseCases: mrUseCases(client)}, &mcp.Implementation{Name: " "})
	decode[output.MergeRequest](t, call(t, anonymous, "nipa_mr_create", map[string]any{"repo": root, "title": "T", "source": "feature", "description": "d"}))
	require.Equal(t, "d", client.descriptions[0])

	s := &server{}
	require.Equal(t, "d", s.withAgentTrailer("d", nil))
	require.Equal(t, "", clientAgent(&mcp.CallToolRequest{}))
}

type recordingPushClient struct {
	fakePushClient
	message *string
}

func (r recordingPushClient) Push(ctx context.Context, org, project, branch, baseTree, message string, files []*serverDomain.PushFile, removed []string, parent2, baseCommit string) (*serverDomain.PushResult, error) {
	*r.message = message
	return r.fakePushClient.Push(ctx, org, project, branch, baseTree, message, files, removed, parent2, baseCommit)
}

func TestPush_AgentTrailer(t *testing.T) {
	root := setupRepo(t, nil)
	writeFile(t, root, "a.txt", "hello\n")
	stageFile(t, root, "a.txt")
	var message string
	session := connect(t, Options{Allow: map[string]bool{CapPush: true}, NewUseCases: func() UseCases {
		return UseCases{Push: usecase.NewPush(loggedInAuth(), recordingPushClient{message: &message}, localrepo.NewLocalRepo())}
	}})
	out := decode[headOutput](t, call(t, session, "nipa_push", map[string]any{"repo": root, "message": "add a"}))
	require.Equal(t, "main", out.Branch)
	require.Equal(t, "add a\n\nNipa-Agent: test-client/v0", message)
}

type fakeSyncClient struct {
	tags      []*domain.Tag
	err       error
	mergeBase *domain.MergeBaseInfo
}

func (fakeSyncClient) Connect(context.Context, string) error { return nil }

func (f fakeSyncClient) GetBranchByName(_ context.Context, _, _, name string) (*serverDomain.Branch, error) {
	if f.err != nil {
		return nil, f.err
	}
	id := snow.ID(9001)
	return &serverDomain.Branch{Name: name, CommitID: &id}, nil
}

func (fakeSyncClient) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func (fakeSyncClient) GetCommitTreeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return &serverDomain.TreeNode{}, nil
}

func (f fakeSyncClient) GetTagByName(_ context.Context, _, _, name string) (*domain.Tag, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Tag{Name: name, CommitID: "6y1"}, nil
}

func (fakeSyncClient) DownloadChunks(context.Context, domain.ChunkScope, []serverDomain.Hash, func(serverDomain.Hash, []byte) error) error {
	return nil
}

func (f fakeSyncClient) GetMergeBase(context.Context, string, string, domain.MergeRef, domain.MergeRef) (*domain.MergeBaseInfo, error) {
	return f.mergeBase, f.err
}

func (fakeSyncClient) MergeFastForward(context.Context, string, string, string, string) (*serverDomain.Branch, error) {
	return nil, errors.New("not expected")
}

func (fakeSyncClient) CreateTag(_ context.Context, _, _, name, message, _, _, _ string) (*domain.Tag, error) {
	return &domain.Tag{Name: name, CommitID: "6y1", Message: message}, nil
}

func (f fakeSyncClient) ListTags(context.Context, string, string) ([]*domain.Tag, error) {
	return f.tags, f.err
}

func (fakeSyncClient) DeleteTag(context.Context, string, string, string) error { return nil }

func syncUseCases(client fakeSyncClient) func() UseCases {
	return func() UseCases {
		return UseCases{
			Tag:    usecase.NewTag(loggedInAuth(), client, localrepo.NewLocalRepo()),
			Update: usecase.NewUpdate(loggedInAuth(), client, localrepo.NewLocalRepo()),
			Merge:  usecase.NewMerge(loggedInAuth(), client, localrepo.NewLocalRepo(), nil),
		}
	}
}

func TestTagTools(t *testing.T) {
	root := setupRepo(t, nil)
	client := fakeSyncClient{tags: []*domain.Tag{{Name: "v1.0.0", CommitID: "6y1"}}}
	session := connect(t, Options{Allow: map[string]bool{CapBranch: true}, NewUseCases: syncUseCases(client)})

	tags := decode[output.Tags](t, call(t, session, "nipa_tag_list", map[string]any{"repo": root}))
	require.Equal(t, "v1.0.0", tags.Tags[0].Name)

	tag := decode[output.Tag](t, call(t, session, "nipa_tag_create", map[string]any{"repo": root, "name": "v2", "message": "rel", "branch": "main"}))
	require.Equal(t, "v2", tag.Name)
	require.Equal(t, "rel", tag.Message)

	res := call(t, session, "nipa_tag_create", map[string]any{"repo": root, "name": " "})
	require.True(t, res.IsError)

	failing := connect(t, Options{NewUseCases: syncUseCases(fakeSyncClient{err: errors.New("server exploded")})})
	res = call(t, failing, "nipa_tag_list", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "server exploded")
}

func TestSyncTools(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{Allow: map[string]bool{CapSync: true}, NewUseCases: syncUseCases(fakeSyncClient{})})

	head := decode[headOutput](t, call(t, session, "nipa_update", map[string]any{"repo": root}))
	require.Equal(t, "main", head.Branch)
	require.Equal(t, "6y1", head.CommitID)

	head = decode[headOutput](t, call(t, session, "nipa_switch", map[string]any{"repo": root, "branch": "dev"}))
	require.Equal(t, "dev", head.Branch)
	require.Empty(t, head.Tag)

	head = decode[headOutput](t, call(t, session, "nipa_switch", map[string]any{"repo": root, "tag": "v1"}))
	require.Equal(t, "v1", head.Tag)
	require.Equal(t, "6y1", head.CommitID)

	for _, args := range []map[string]any{{}, {"branch": "a", "tag": "b"}} {
		args["repo"] = root
		res := call(t, session, "nipa_switch", args)
		require.True(t, res.IsError)
		require.Contains(t, toolText(t, res), "pass exactly one of branch or tag")
	}

	fresh := setupRepo(t, nil)
	failing := connect(t, Options{Allow: map[string]bool{CapSync: true}, NewUseCases: syncUseCases(fakeSyncClient{err: errors.New("server exploded")})})
	for _, args := range []map[string]any{{}, {"tag": "v2"}} {
		args["repo"] = fresh
		tool := "nipa_update"
		if len(args) > 1 {
			tool = "nipa_switch"
		}
		res := call(t, failing, tool, args)
		require.True(t, res.IsError)
		require.Contains(t, toolText(t, res), "server exploded")
	}
}

func TestMergeTool(t *testing.T) {
	root := setupRepo(t, nil)
	upToDate := fakeSyncClient{mergeBase: &domain.MergeBaseInfo{SourceCommitID: "a", MergeBaseCommitID: "a"}}
	session := connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: syncUseCases(upToDate)})

	out := decode[mergeOutput](t, call(t, session, "nipa_merge", map[string]any{"repo": root, "source": "feature"}))
	require.True(t, out.UpToDate)

	res := call(t, session, "nipa_merge", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "a source branch to merge is required")

	res = call(t, session, "nipa_merge", map[string]any{"repo": root, "abort": true})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "no merge in progress")

	saveMergeState(t, root, &domain.MergeState{SourceBranch: "feature", Conflicts: []string{"a.txt"}})
	out = decode[mergeOutput](t, call(t, session, "nipa_merge", map[string]any{"repo": root, "abort": true}))
	require.True(t, out.Aborted)
}

func saveMergeState(t *testing.T, root string, state *domain.MergeState) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(root))
	defer func() { _ = lr.Close() }()
	require.NoError(t, lr.SaveMergeState(state))
}

func TestConflictTools(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "c.txt": "c\n", "d.txt": "d\n"})
	session := connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: func() UseCases { return UseCases{} }})

	empty := decode[conflictsOutput](t, call(t, session, "nipa_conflicts", map[string]any{"repo": root}))
	require.Empty(t, empty.Op)
	require.Empty(t, empty.Files)

	saveMergeState(t, root, &domain.MergeState{
		SourceBranch:   "feature",
		SourceCommitID: "6y2",
		Conflicts:      []string{"a.txt", "b.txt", "c.txt", "d.txt"},
		ConflictKinds:  map[string]string{"a.txt": "modify"},
	})
	set := decode[conflictsOutput](t, call(t, session, "nipa_conflicts", map[string]any{"repo": root}))
	require.Equal(t, "merge", set.Op)
	require.Equal(t, "feature", set.Theirs.Label)
	require.Len(t, set.Files, 4)
	require.Equal(t, "modify", set.Files[0].Kind)

	st := decode[output.Status](t, call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "a.txt", "choice": "content", "content": "merged\n"}))
	require.Contains(t, st.Staged, "a.txt")
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "merged\n", string(data))

	decode[output.Status](t, call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "b.txt", "choice": "working"}))
	decode[output.Status](t, call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "c.txt", "choice": "delete"}))
	decode[output.Status](t, call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "c.txt", "choice": "ours"}))
	_, err = os.Stat(filepath.Join(root, "c.txt"))
	require.True(t, os.IsNotExist(err))

	res := call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "d.txt", "choice": "theirs"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "diff is not configured")

	res = call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "d.txt", "choice": "bogus"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "choice must be one of")

	res = call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "zzz.txt", "choice": "working"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "has no conflict to resolve")

	res = call(t, session, "nipa_conflicts", map[string]any{"repo": filepath.Join(t.TempDir(), "missing")})
	require.True(t, res.IsError)
	res = call(t, session, "nipa_resolve_conflict", map[string]any{"repo": filepath.Join(t.TempDir(), "missing"), "path": "a", "choice": "working"})
	require.True(t, res.IsError)
}

func TestResolveConflict_NothingPending(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "a\n"})
	session := connect(t, Options{Allow: map[string]bool{CapMerge: true}, NewUseCases: func() UseCases { return UseCases{} }})
	res := call(t, session, "nipa_resolve_conflict", map[string]any{"repo": root, "path": "a.txt", "choice": "theirs"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "no merge or revert is waiting")
}

func TestRestoreTool(t *testing.T) {
	root := setupRepo(t, map[string]string{"a.txt": "one\n"})
	writeFile(t, root, "a.txt", "changed\n")
	session := connect(t, Options{Allow: map[string]bool{CapStage: true}, NewUseCases: offlineUseCases})

	st := decode[output.Status](t, call(t, session, "nipa_restore", map[string]any{"repo": root, "paths": []string{"a.txt"}}))
	require.Empty(t, st.Modified)
	data, err := os.ReadFile(filepath.Join(root, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "one\n", string(data))

	res := call(t, session, "nipa_restore", map[string]any{"repo": root, "paths": []string{}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "at least one path is required")

	res = call(t, session, "nipa_restore", map[string]any{"repo": root, "paths": []string{"nope.txt"}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "not tracked")

	res = call(t, session, "nipa_restore", map[string]any{"repo": filepath.Join(t.TempDir(), "missing"), "paths": []string{"a.txt"}})
	require.True(t, res.IsError)
}

func TestLockQuery(t *testing.T) {
	root := setupRepo(t, nil)
	client := fakeLockClient{locks: []*domain.FileLock{
		{Path: "art", Global: true, HeldByName: "bob"},
		{Path: "art/tex.png", Branch: "dev", HeldByName: "alice"},
		{Path: "audio/loop.wav", HeldByName: "carol"},
		nil,
	}}
	session := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), client, localrepo.NewLocalRepo())}
	}})

	out := decode[lockQueryOutput](t, call(t, session, "nipa_lock_query", map[string]any{"repo": root, "path": "/art/tex.png"}))
	require.True(t, out.Locked)
	require.Equal(t, "art/tex.png", out.Path)
	require.Len(t, out.Locks, 2)
	require.Equal(t, "bob", out.Locks[0].HeldByName)
	require.Equal(t, "mainline", out.Locks[0].Scope)

	out = decode[lockQueryOutput](t, call(t, session, "nipa_lock_query", map[string]any{"repo": root, "path": "artwork.png"}))
	require.False(t, out.Locked)
	require.Empty(t, out.Locks)

	res := call(t, session, "nipa_lock_query", map[string]any{"repo": root, "path": " / "})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "a path is required")

	failing := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), fakeLockClient{listErr: errors.New("server exploded")}, localrepo.NewLocalRepo())}
	}})
	res = call(t, failing, "nipa_lock_query", map[string]any{"repo": root, "path": "a"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "server exploded")
}

func TestNewTools_NotConfigured(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{Allow: AllCaps(), NewUseCases: func() UseCases { return UseCases{} }})
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"nipa_lock_query", map[string]any{"path": "a"}, "locks are not configured"},
		{"nipa_tag_list", map[string]any{}, "tags are not configured"},
		{"nipa_tag_create", map[string]any{"name": "v1"}, "tags are not configured"},
		{"nipa_update", map[string]any{}, "update is not configured"},
		{"nipa_switch", map[string]any{"branch": "dev"}, "switch is not configured"},
		{"nipa_merge", map[string]any{"source": "dev"}, "merge is not configured"},
	}
	for _, tc := range cases {
		tc.args["repo"] = root
		res := call(t, session, tc.tool, tc.args)
		require.True(t, res.IsError, tc.tool)
		require.Contains(t, toolText(t, res), tc.want, tc.tool)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	noFactory := connect(t, Options{Allow: AllCaps()})
	for _, tool := range []string{"nipa_tag_list", "nipa_update", "nipa_merge", "nipa_mr_create"} {
		res := call(t, noFactory, tool, map[string]any{"repo": missing, "source": "x", "title": "t"})
		require.True(t, res.IsError, tool)
	}
	res := call(t, noFactory, "nipa_tag_list", map[string]any{"repo": root})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "no usecase factory configured")
}

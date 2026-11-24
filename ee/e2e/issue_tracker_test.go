package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	clientdomain "github.com/nipalab/nipa/internal/client/domain"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

func TestEnterpriseIssueTrackerJobs(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	fake := jiratest.New(jiratest.Options{})
	t.Cleanup(fake.Close)
	fake.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "Hero rig"})
	fake.AddIssue(jiratest.Issue{Key: "GAME-2", Summary: "Side quest"})

	host, uc := startEnterpriseServerWith(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), serverOptions{integrations: true})

	var superID, orgID, projectID int64
	require.NoError(t, dbConn.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1 OR name = $1 ORDER BY id LIMIT 1`, e2eSuperAdmin).Scan(&superID))
	require.NoError(t, dbConn.QueryRowContext(ctx, `SELECT o.id, p.id FROM organizations o JOIN projects p ON p.org_id = o.id WHERE o.slug = $1 AND p.slug = $2`, e2eOrgSlug, e2eProjectSlug).Scan(&orgID, &projectID))
	admin := serverDomain.ContextWithClaim(ctx, serverDomain.Claims{UserID: snow.ID(superID), IsAdmin: true, IsSuperAdmin: true})
	token := "jira-test-token"
	_, err = uc.IssueTracker.Put(admin, snow.ID(orgID), serverusecase.IssueTrackerInput{BaseURL: fake.URL, Account: "bot@example.com", Token: &token})
	require.NoError(t, err)
	_, err = uc.IssueTracker.PutProject(admin, snow.ID(orgID), snow.ID(projectID), serverusecase.ProjectIssueTrackerInput{Keys: []string{"GAME"}, LinkCommits: "all", MergedStatus: "Done"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = uc.IssueTracker.DeleteProject(admin, snow.ID(projectID))
		_ = uc.IssueTracker.Delete(admin, snow.ID(orgID))
	})

	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(store, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	push := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	branch := "issue-jobs-" + snow.ID(time.Now().UnixNano()).Base36()
	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, branch, "main", "", "")
	require.NoError(t, err)
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, branch, nil, work))
	writeFile(t, work, branch+".txt", "rig\n")
	stagePath(t, work, branch+".txt")
	require.NoError(t, push.Run(ctx, work, "Fixes GAME-1 in the rig"))
	require.Eventually(t, func() bool {
		for _, link := range fake.RemoteLinks("GAME-1") {
			if strings.HasPrefix(link.GlobalID, "nipa:default/default:commit:") {
				return true
			}
		}
		return false
	}, 15*time.Second, 25*time.Millisecond, "the commit link is delivered through the postgres outbox")

	mr, err := grpcClient.CreateMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, clientdomain.NewMergeRequest{Title: "Rig", Description: "See GAME-2", SourceBranch: branch, TargetBranch: "main"})
	require.NoError(t, err)
	mrLink := fmt.Sprintf("nipa:default/default:mr:%d", mr.Number)
	hasMRLink := func() bool {
		for _, link := range fake.RemoteLinks("GAME-2") {
			if link.GlobalID == mrLink {
				return true
			}
		}
		return false
	}
	require.Eventually(t, hasMRLink, 15*time.Second, 25*time.Millisecond)
	_, err = grpcClient.UpdateMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, mr.Number, "Rig", "No issue", nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !hasMRLink() }, 15*time.Second, 25*time.Millisecond, "the unlink job is stored and sent on postgres")
	_, _, err = grpcClient.MergeMergeRequest(ctx, e2eOrgSlug, e2eProjectSlug, mr.Number, "merge", false)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		issue, _ := fake.Issue("GAME-1")
		return issue.Status == "Done" && len(fake.Comments("GAME-1")) == 1
	}, 15*time.Second, 25*time.Millisecond, "the merge closes GAME-1 from its commit message")

	require.Eventually(t, func() bool {
		jobs, _, err := uc.IssueTracker.ListJobs(admin, snow.ID(projectID), serverusecase.IssueJobListOptions{State: serverDomain.IssueJobPending})
		return err == nil && len(jobs) == 0
	}, 15*time.Second, 25*time.Millisecond)
	failed, _, err := uc.IssueTracker.ListJobs(admin, snow.ID(projectID), serverusecase.IssueJobListOptions{State: serverDomain.IssueJobFailed})
	require.NoError(t, err)
	require.Empty(t, failed)
}

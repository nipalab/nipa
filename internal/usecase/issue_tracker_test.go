package usecase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/egress"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
	"github.com/nipalab/nipa/internal/sealbox"
	"github.com/nipalab/nipa/internal/snow"
)

const testTrackerKind = "usecasetest"

func init() {
	issuetracker.Register(testTrackerKind, func(cfg issuetracker.Config) (issuetracker.Tracker, error) {
		return nil, errors.New(testTrackerKind + ": not buildable")
	})
}

type issueTrackerFixture struct {
	repo  *MockissueTrackerRepository
	orgs  *MockissueTrackerOrgs
	perm  *MockissueTrackerAccess
	rec   *captureRecorder
	box   *sealbox.Box
	jira  *jiratest.Server
	uc    *IssueTracker
	now   time.Time
	ctx   context.Context
	orgID snow.ID
}

func newIssueTrackerFixture(t *testing.T) *issueTrackerFixture {
	ctrl := gomock.NewController(t)
	box, err := NewIssueTrackerBox([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	server := jiratest.New(jiratest.Options{})
	t.Cleanup(server.Close)
	f := &issueTrackerFixture{
		repo:  NewMockissueTrackerRepository(ctrl),
		orgs:  NewMockissueTrackerOrgs(ctrl),
		perm:  NewMockissueTrackerAccess(ctrl),
		rec:   &captureRecorder{},
		box:   box,
		jira:  server,
		now:   time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
		ctx:   domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 7}),
		orgID: 1,
	}
	f.uc = NewIssueTracker(f.repo, f.orgs, f.perm, box, newTestBranchNode(t)).
		WithAudit(f.rec).
		WithClock(func() time.Time { return f.now }).
		WithFactory(func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error) {
			cfg.Transport = http.DefaultTransport
			return issuetracker.New(kind, cfg)
		})
	return f
}

func (f *issueTrackerFixture) owner(ok bool) {
	f.orgs.EXPECT().IsOrgOwner(gomock.Any(), f.orgID).Return(ok, nil)
}

func (f *issueTrackerFixture) stored(t *testing.T, id snow.ID, token string) *domain.IssueTracker {
	sealed, err := f.box.Seal([]byte(token), issueTrackerAAD(id))
	require.NoError(t, err)
	return &domain.IssueTracker{
		ID: id, OrgID: f.orgID, Kind: "jira", Name: "Jira", BaseURL: f.jira.URL, AuthKind: issuetracker.AuthBasic,
		Account: "bot@example.com", SecretSealed: sealed, CreatedBy: 3, CreatedAt: f.now.Add(-time.Hour), UpdatedAt: f.now.Add(-time.Hour),
	}
}

func (f *issueTrackerFixture) input(token string) IssueTrackerInput {
	in := IssueTrackerInput{BaseURL: f.jira.URL + "/", Account: "bot@example.com"}
	if token != "" {
		in.Token = &token
	}
	return in
}

func requireDomainCode(t *testing.T, err error, code int) {
	t.Helper()
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, code, domErr.Code, domErr.Message)
}

func TestIssueTracker_Unavailable(t *testing.T) {
	uc := NewIssueTracker(nil, nil, nil, nil, nil)
	require.False(t, uc.Available())
	ctx := context.Background()
	_, err := uc.Get(ctx, 1)
	requireDomainCode(t, err, 503)
	_, err = uc.Put(ctx, 1, IssueTrackerInput{})
	requireDomainCode(t, err, 503)
	requireDomainCode(t, uc.Delete(ctx, 1), 503)
	_, err = uc.Test(ctx, 1)
	requireDomainCode(t, err, 503)
	_, err = uc.GetProject(ctx, 1)
	requireDomainCode(t, err, 503)
	_, err = uc.PutProject(ctx, 1, 2, ProjectIssueTrackerInput{})
	requireDomainCode(t, err, 503)
	requireDomainCode(t, uc.DeleteProject(ctx, 2), 503)
}

func TestIssueTracker_Get(t *testing.T) {
	f := newIssueTrackerFixture(t)
	require.True(t, f.uc.Available())
	stored := f.stored(t, 10, "tok")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	view, err := f.uc.Get(f.ctx, f.orgID)
	require.NoError(t, err)
	require.True(t, view.CanManage)
	require.Nil(t, view.Tracker.SecretSealed)
	require.Equal(t, "bot@example.com", view.Tracker.Account)
	require.NotNil(t, stored.SecretSealed, "the stored row is not modified")

	f.owner(false)
	f.orgs.EXPECT().IsOrgMember(gomock.Any(), f.orgID).Return(true, nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	view, err = f.uc.Get(f.ctx, f.orgID)
	require.NoError(t, err)
	require.False(t, view.CanManage)
	require.Equal(t, domain.IssueTracker{Kind: "jira", Name: "Jira", BaseURL: f.jira.URL}, *view.Tracker)

	f.owner(false)
	f.orgs.EXPECT().IsOrgMember(gomock.Any(), f.orgID).Return(false, nil)
	_, err = f.uc.Get(f.ctx, f.orgID)
	requireDomainCode(t, err, 403)

	f.owner(false)
	f.orgs.EXPECT().IsOrgMember(gomock.Any(), f.orgID).Return(false, errors.New("db"))
	_, err = f.uc.Get(f.ctx, f.orgID)
	require.EqualError(t, err, "db")

	f.orgs.EXPECT().IsOrgOwner(gomock.Any(), f.orgID).Return(false, errors.New("db"))
	_, err = f.uc.Get(f.ctx, f.orgID)
	require.EqualError(t, err, "db")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.Get(f.ctx, f.orgID)
	requireDomainCode(t, err, 404)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorDatabase("boom"))
	_, err = f.uc.Get(f.ctx, f.orgID)
	requireDomainCode(t, err, 500)
}

func TestIssueTracker_PutCreate(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound()).Times(2)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	var created domain.IssueTracker
	f.repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tr domain.IssueTracker) (*domain.IssueTracker, error) {
		created = tr
		return &tr, nil
	})

	res, err := f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	require.NoError(t, err)
	require.True(t, res.Created)
	require.Equal(t, "cloud", res.Site.Deployment)
	require.Nil(t, res.Tracker.SecretSealed)
	require.Equal(t, f.jira.URL, created.BaseURL)
	require.Equal(t, "jira", created.Kind)
	require.Equal(t, "Jira", created.Name)
	require.Equal(t, issuetracker.AuthBasic, created.AuthKind)
	require.Equal(t, snow.ID(7), created.CreatedBy)
	require.Equal(t, f.now, created.CreatedAt)
	require.NotZero(t, created.ID)
	plain, err := f.box.Open(created.SecretSealed, []byte("issue_tracker:"+created.ID.Base36()))
	require.NoError(t, err)
	require.Equal(t, "jira-test-token", string(plain))

	entry := f.rec.only(t)
	require.Equal(t, domain.AuditIssueTrackerCreated, entry.Action)
	require.Equal(t, domain.AuditTargetIssueTracker, entry.TargetType)
	require.Equal(t, created.ID.Base36(), entry.TargetID)
	require.Equal(t, f.orgID, *entry.OrgID)
	require.NotContains(t, entry.Metadata, "token")
	for _, v := range entry.Metadata {
		require.NotContains(t, v, "jira-test-token")
	}
}

func TestIssueTracker_PutUpdate(t *testing.T) {
	f := newIssueTrackerFixture(t)
	stored := f.stored(t, 10, "jira-test-token")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil).Times(2)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	var updated domain.IssueTracker
	f.repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tr domain.IssueTracker) (*domain.IssueTracker, error) {
		updated = tr
		return &tr, nil
	})
	in := f.input("")
	in.Name = "Studio Jira"
	res, err := f.uc.Put(f.ctx, f.orgID, in)
	require.NoError(t, err)
	require.False(t, res.Created)
	require.Equal(t, snow.ID(10), updated.ID)
	require.Equal(t, snow.ID(3), updated.CreatedBy)
	require.Equal(t, f.now, updated.UpdatedAt)
	plain, err := f.box.Open(updated.SecretSealed, issueTrackerAAD(10))
	require.NoError(t, err)
	require.Equal(t, "jira-test-token", string(plain))
	entry := f.rec.only(t)
	require.Equal(t, domain.AuditIssueTrackerUpdated, entry.Action)
	require.Equal(t, []string{"name"}, entry.Metadata["changed"])

	f.rec.entries = nil
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil).Times(2)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tr domain.IssueTracker) (*domain.IssueTracker, error) {
		return &tr, nil
	})
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	require.NoError(t, err)
	require.Equal(t, []string{"token"}, f.rec.only(t).Metadata["changed"])
}

func TestIssueTracker_PutConflict(t *testing.T) {
	f := newIssueTrackerFixture(t)
	stored := f.stored(t, 10, "jira-test-token")
	moved := *stored
	moved.UpdatedAt = f.now

	f.owner(true)
	gomock.InOrder(
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil),
		f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil),
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(&moved, nil),
	)
	_, err := f.uc.Put(f.ctx, f.orgID, f.input(""))
	requireDomainCode(t, err, 409)

	f.owner(true)
	gomock.InOrder(
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound()),
		f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil),
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil),
	)
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	requireDomainCode(t, err, 409)

	f.owner(true)
	gomock.InOrder(
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound()),
		f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(errors.New("lock")),
	)
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	require.EqualError(t, err, "lock")

	f.owner(true)
	gomock.InOrder(
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound()),
		f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil),
		f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, errors.New("read")),
	)
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	require.EqualError(t, err, "read")
	require.Empty(t, f.rec.entries)
}

func TestIssueTracker_PutProbeFailures(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err := f.uc.Put(f.ctx, f.orgID, f.input("wrong-token"))
	var failure *IssueTrackerProbeFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, IssueTrackerCheckAuth, failure.Check)
	require.NotContains(t, err.Error(), "wrong-token")
	require.ErrorIs(t, err, issuetracker.ErrUnauthorized)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	f.jira.Fail(jiratest.Failure{Status: http.StatusBadGateway})
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	require.ErrorAs(t, err, &failure)
	require.Equal(t, IssueTrackerCheckReach, failure.Check)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	in := f.input("tok")
	in.Kind = testTrackerKind
	in.BaseURL = "https://tracker.example.com/"
	_, err = f.uc.Put(f.ctx, f.orgID, in)
	requireDomainCode(t, err, 400)
	require.ErrorContains(t, err, "not buildable")
}

func TestIssueTracker_PutGuardedTransport(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.uc.WithFactory(GuardedIssueTrackerFactory(nil))
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err := f.uc.Put(f.ctx, f.orgID, f.input("jira-test-token"))
	var failure *IssueTrackerProbeFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, IssueTrackerCheckReach, failure.Check)
	require.True(t, egress.IsBlocked(err))

	tlsServer := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(tlsServer.Close)
	f.uc.WithFactory(GuardedIssueTrackerFactory([]string{"127.0.0.1"}))
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	in := f.input("jira-test-token")
	in.BaseURL = tlsServer.URL
	_, err = f.uc.Put(f.ctx, f.orgID, in)
	require.ErrorAs(t, err, &failure)
	require.Equal(t, IssueTrackerCheckTLS, failure.Check)
}

func TestIssueTracker_PutValidation(t *testing.T) {
	f := newIssueTrackerFixture(t)
	long := strings.Repeat("x", 9000)
	cases := []struct {
		name string
		in   IssueTrackerInput
		msg  string
	}{
		{"kind", IssueTrackerInput{Kind: "linear", BaseURL: "https://x.example"}, "unknown issue tracker kind"},
		{"name", IssueTrackerInput{Name: strings.Repeat("n", 101), BaseURL: "https://x.example"}, "name must be at most"},
		{"url length", IssueTrackerInput{BaseURL: "https://x.example/" + long}, "base_url is too long"},
		{"url", IssueTrackerInput{BaseURL: "ftp://x.example"}, "base url must be http or https"},
		{"basic account", IssueTrackerInput{BaseURL: "https://x.example", AuthKind: "basic"}, "basic auth needs the account email"},
		{"account length", IssueTrackerInput{BaseURL: "https://x.example", Account: long}, "account is too long"},
		{"auth kind", IssueTrackerInput{BaseURL: "https://x.example", AuthKind: "oauth"}, "auth_kind must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.owner(true)
			_, err := f.uc.Put(f.ctx, f.orgID, tc.in)
			requireDomainCode(t, err, 400)
			require.ErrorContains(t, err, tc.msg)
		})
	}

	f.owner(false)
	_, err := f.uc.Put(f.ctx, f.orgID, f.input("tok"))
	requireDomainCode(t, err, 403)

	f.orgs.EXPECT().IsOrgOwner(gomock.Any(), f.orgID).Return(false, errors.New("db"))
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("tok"))
	require.EqualError(t, err, "db")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, errors.New("read"))
	_, err = f.uc.Put(f.ctx, f.orgID, f.input("tok"))
	require.EqualError(t, err, "read")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	tooLong := strings.Repeat("t", maxIssueTrackerToken+1)
	in := f.input("")
	in.Token = &tooLong
	_, err = f.uc.Put(f.ctx, f.orgID, in)
	require.ErrorContains(t, err, "token is too long")

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.Put(f.ctx, f.orgID, f.input(""))
	require.ErrorContains(t, err, "a token is required")

	stored := f.stored(t, 10, "tok")
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	moved := f.input("")
	moved.BaseURL = "https://elsewhere.example"
	_, err = f.uc.Put(f.ctx, f.orgID, moved)
	require.ErrorContains(t, err, "enter the token again")

	broken := *stored
	broken.SecretSealed = []byte("garbage")
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(&broken, nil)
	_, err = f.uc.Put(f.ctx, f.orgID, f.input(""))
	require.ErrorContains(t, err, "cannot be decrypted")

	bearer, err := normalizeIssueTrackerInput(IssueTrackerInput{BaseURL: "https://x.example", AuthKind: "bearer", Account: "ignored"})
	require.NoError(t, err)
	require.Equal(t, issuetracker.AuthBearer, bearer.AuthKind)
	require.Empty(t, bearer.Account)
	implicit, err := normalizeIssueTrackerInput(IssueTrackerInput{BaseURL: "https://x.example"})
	require.NoError(t, err)
	require.Equal(t, issuetracker.AuthBearer, implicit.AuthKind)
	other, err := normalizeIssueTrackerInput(IssueTrackerInput{Kind: testTrackerKind, BaseURL: " https://x.example/ "})
	require.NoError(t, err)
	require.Equal(t, "https://x.example", other.BaseURL)
	require.Equal(t, "Usecasetest", other.Name)
}

func TestIssueTracker_Delete(t *testing.T) {
	f := newIssueTrackerFixture(t)
	stored := f.stored(t, 10, "tok")

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	f.repo.EXPECT().CountProjects(gomock.Any(), snow.ID(10)).Return(int64(0), nil)
	f.repo.EXPECT().Delete(gomock.Any(), f.orgID, snow.ID(10)).Return(true, nil)
	require.NoError(t, f.uc.Delete(f.ctx, f.orgID))
	entry := f.rec.only(t)
	require.Equal(t, domain.AuditIssueTrackerDeleted, entry.Action)
	require.Equal(t, snow.ID(10).Base36(), entry.TargetID)

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	f.repo.EXPECT().CountProjects(gomock.Any(), snow.ID(10)).Return(int64(2), nil)
	err := f.uc.Delete(f.ctx, f.orgID)
	requireDomainCode(t, err, 409)
	require.ErrorContains(t, err, "used by 2 project(s)")

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	requireDomainCode(t, f.uc.Delete(f.ctx, f.orgID), 404)

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(errors.New("lock"))
	require.EqualError(t, f.uc.Delete(f.ctx, f.orgID), "lock")

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, errors.New("read"))
	require.EqualError(t, f.uc.Delete(f.ctx, f.orgID), "read")

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	f.repo.EXPECT().CountProjects(gomock.Any(), snow.ID(10)).Return(int64(0), errors.New("count"))
	require.EqualError(t, f.uc.Delete(f.ctx, f.orgID), "count")

	f.owner(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	f.repo.EXPECT().CountProjects(gomock.Any(), snow.ID(10)).Return(int64(0), nil)
	f.repo.EXPECT().Delete(gomock.Any(), f.orgID, snow.ID(10)).Return(false, errors.New("delete"))
	require.EqualError(t, f.uc.Delete(f.ctx, f.orgID), "delete")

	f.owner(false)
	requireDomainCode(t, f.uc.Delete(f.ctx, f.orgID), 403)
	require.Len(t, f.rec.entries, 1)
}

func TestIssueTracker_Test(t *testing.T) {
	f := newIssueTrackerFixture(t)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(f.stored(t, 10, "jira-test-token"), nil)
	res, err := f.uc.Test(f.ctx, f.orgID)
	require.NoError(t, err)
	require.True(t, res.OK)
	require.Equal(t, "cloud", res.Site.Deployment)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(f.stored(t, 10, "stale"), nil)
	res, err = f.uc.Test(f.ctx, f.orgID)
	require.NoError(t, err)
	require.False(t, res.OK)
	require.Equal(t, IssueTrackerCheckAuth, res.Check)
	require.NotContains(t, res.Error, "stale")

	broken := f.stored(t, 10, "tok")
	broken.SecretSealed = []byte("garbage")
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(broken, nil)
	res, err = f.uc.Test(f.ctx, f.orgID)
	require.NoError(t, err)
	require.Equal(t, IssueTrackerCheckAuth, res.Check)
	require.Contains(t, res.Error, "cannot be decrypted")

	other := f.stored(t, 10, "tok")
	other.Kind = testTrackerKind
	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(other, nil)
	_, err = f.uc.Test(f.ctx, f.orgID)
	requireDomainCode(t, err, 400)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.Test(f.ctx, f.orgID)
	requireDomainCode(t, err, 404)

	f.owner(true)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, errors.New("read"))
	_, err = f.uc.Test(f.ctx, f.orgID)
	require.EqualError(t, err, "read")

	f.owner(false)
	_, err = f.uc.Test(f.ctx, f.orgID)
	requireDomainCode(t, err, 403)
}

func TestIssueTracker_Project(t *testing.T) {
	f := newIssueTrackerFixture(t)
	projectID := snow.ID(2)
	stored := f.stored(t, 10, "tok")
	admin := func(ok bool) { f.perm.EXPECT().AdminHasProject(gomock.Any(), projectID).Return(ok) }

	admin(true)
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(stored, nil)
	var saved domain.ProjectIssueTracker
	f.repo.EXPECT().UpsertProject(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, s domain.ProjectIssueTracker) (*domain.ProjectIssueTracker, error) {
		saved = s
		return &s, nil
	})
	off := false
	trackerID := snow.ID(10)
	got, err := f.uc.PutProject(f.ctx, f.orgID, projectID, ProjectIssueTrackerInput{
		TrackerID: &trackerID, Keys: []string{"game", "ART", "GAME"}, CommentOnMerge: &off,
		LinkCommits: "default_branch", MergedStatus: " Done ", OpenedStatus: "In Review",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"ART", "GAME"}, got.Keys)
	require.Equal(t, domain.ProjectIssueTracker{
		ProjectID: projectID, TrackerID: 10, Keys: []string{"ART", "GAME"}, LinkMergeRequests: true,
		LinkCommits: "default_branch", CommentOnMerge: false, MergedStatus: "Done", OpenedStatus: "In Review",
		IsActive: true, SetBy: 7, UpdatedAt: f.now,
	}, saved)
	entry := f.rec.only(t)
	require.Equal(t, domain.AuditProjectIssueTrackerSet, entry.Action)
	require.Equal(t, projectID, *entry.ProjectID)
	require.Equal(t, []string{"ART", "GAME"}, entry.Metadata["keys"])

	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(&saved, nil)
	read, err := f.uc.GetProject(f.ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, saved, *read)

	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(nil, domain.NewErrorRecordNotFound())
	_, err = f.uc.GetProject(f.ctx, projectID)
	requireDomainCode(t, err, 404)

	admin(false)
	_, err = f.uc.GetProject(f.ctx, projectID)
	requireDomainCode(t, err, 403)

	f.rec.entries = nil
	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(&saved, nil)
	f.repo.EXPECT().DeleteProject(gomock.Any(), projectID).Return(true, nil)
	require.NoError(t, f.uc.DeleteProject(f.ctx, projectID))
	require.Equal(t, domain.AuditProjectIssueTrackerCleared, f.rec.only(t).Action)

	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(nil, domain.NewErrorRecordNotFound())
	requireDomainCode(t, f.uc.DeleteProject(f.ctx, projectID), 404)

	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(nil, errors.New("read"))
	require.EqualError(t, f.uc.DeleteProject(f.ctx, projectID), "read")

	admin(true)
	f.repo.EXPECT().GetProject(gomock.Any(), projectID).Return(&saved, nil)
	f.repo.EXPECT().DeleteProject(gomock.Any(), projectID).Return(false, errors.New("delete"))
	require.EqualError(t, f.uc.DeleteProject(f.ctx, projectID), "delete")

	admin(false)
	requireDomainCode(t, f.uc.DeleteProject(f.ctx, projectID), 403)
}

func TestIssueTracker_PutProjectErrors(t *testing.T) {
	f := newIssueTrackerFixture(t)
	projectID := snow.ID(2)
	admin := func() { f.perm.EXPECT().AdminHasProject(gomock.Any(), projectID).Return(true) }

	tooMany := make([]string, 0, 21)
	for i := range 21 {
		tooMany = append(tooMany, "K"+string(rune('A'+i)))
	}
	for _, tc := range []struct {
		in  ProjectIssueTrackerInput
		msg string
	}{
		{ProjectIssueTrackerInput{}, "at least one project key"},
		{ProjectIssueTrackerInput{Keys: []string{"1BAD"}}, "invalid project key"},
		{ProjectIssueTrackerInput{Keys: tooMany}, "at most 20 project keys"},
		{ProjectIssueTrackerInput{Keys: []string{"A1"}, LinkCommits: "some"}, "link_commits must be"},
		{ProjectIssueTrackerInput{Keys: []string{"A1"}, MergedStatus: strings.Repeat("s", 101)}, "statuses must be at most"},
	} {
		admin()
		_, err := f.uc.PutProject(f.ctx, f.orgID, projectID, tc.in)
		requireDomainCode(t, err, 400)
		require.ErrorContains(t, err, tc.msg)
	}

	valid := ProjectIssueTrackerInput{Keys: []string{"GAME"}}
	admin()
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, domain.NewErrorRecordNotFound())
	_, err := f.uc.PutProject(f.ctx, f.orgID, projectID, valid)
	require.ErrorContains(t, err, "has no issue tracker connection")

	otherID := snow.ID(99)
	wrong := valid
	wrong.TrackerID = &otherID
	admin()
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(f.stored(t, 10, "tok"), nil)
	_, err = f.uc.PutProject(f.ctx, f.orgID, projectID, wrong)
	require.ErrorContains(t, err, "tracker_id must be")

	admin()
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(errors.New("lock"))
	_, err = f.uc.PutProject(f.ctx, f.orgID, projectID, valid)
	require.EqualError(t, err, "lock")

	admin()
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(nil, errors.New("read"))
	_, err = f.uc.PutProject(f.ctx, f.orgID, projectID, valid)
	require.EqualError(t, err, "read")

	admin()
	f.repo.EXPECT().LockOrg(gomock.Any(), f.orgID).Return(nil)
	f.repo.EXPECT().GetByOrg(gomock.Any(), f.orgID).Return(f.stored(t, 10, "tok"), nil)
	f.repo.EXPECT().UpsertProject(gomock.Any(), gomock.Any()).Return(nil, errors.New("write"))
	_, err = f.uc.PutProject(f.ctx, f.orgID, projectID, valid)
	require.EqualError(t, err, "write")

	f.perm.EXPECT().AdminHasProject(gomock.Any(), projectID).Return(false)
	_, err = f.uc.PutProject(f.ctx, f.orgID, projectID, valid)
	requireDomainCode(t, err, 403)
	require.Empty(t, f.rec.entries)
}

func TestIssueTrackerProbeFailure(t *testing.T) {
	cause := errors.New("x")
	failure := &IssueTrackerProbeFailure{Check: IssueTrackerCheckReach, Err: cause}
	require.Equal(t, "issue tracker check reach failed: x", failure.Error())
	require.ErrorIs(t, failure, cause)
	require.Equal(t, IssueTrackerCheckReach, probeCheck(cause))
	require.True(t, sameIssueTracker(nil, nil))
	require.False(t, sameIssueTracker(&domain.IssueTracker{}, nil))
}

package byostorage

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var errBoom = errors.New("boom")

type stubRepo struct {
	mu       sync.Mutex
	servers  map[snow.ID]*domain.StorageServer
	defaults map[snow.ID]snow.ID
	projects map[snow.ID]snow.ID
	inUse    map[snow.ID]bool
	fail     map[string]error
	calls    map[string]int
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		servers:  map[snow.ID]*domain.StorageServer{},
		defaults: map[snow.ID]snow.ID{},
		projects: map[snow.ID]snow.ID{},
		inUse:    map[snow.ID]bool{},
		fail:     map[string]error{},
		calls:    map[string]int{},
	}
}

func (r *stubRepo) hit(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[name]++
	return r.fail[name]
}

func (r *stubRepo) LockOwner(context.Context, snow.ID) error { return r.hit("LockOwner") }

func (r *stubRepo) CountServers(_ context.Context, owner snow.ID) (int64, error) {
	if err := r.hit("CountServers"); err != nil {
		return 0, err
	}
	var n int64
	for _, s := range r.servers {
		if s.OwnerUserID == owner {
			n++
		}
	}
	return n, nil
}

func (r *stubRepo) CreateServer(_ context.Context, server domain.StorageServer) (*domain.StorageServer, error) {
	if err := r.hit("CreateServer"); err != nil {
		return nil, err
	}
	for _, s := range r.servers {
		if s.OwnerUserID == server.OwnerUserID && s.Name == server.Name {
			return nil, domain.NewErrorConflict("record already exists")
		}
	}
	server.CreatedAt = time.Unix(100, 0)
	server.UpdatedAt = server.CreatedAt
	r.servers[server.ID] = &server
	copied := server
	return &copied, nil
}

func (r *stubRepo) UpdateServer(_ context.Context, server domain.StorageServer) (*domain.StorageServer, error) {
	if err := r.hit("UpdateServer"); err != nil {
		return nil, err
	}
	server.UpdatedAt = server.UpdatedAt.Add(time.Second)
	r.servers[server.ID] = &server
	copied := server
	return &copied, nil
}

func (r *stubRepo) Server(_ context.Context, id snow.ID) (*domain.StorageServer, error) {
	if err := r.hit("Server"); err != nil {
		return nil, err
	}
	s, ok := r.servers[id]
	if !ok {
		return nil, domain.NewErrorNotFound("record not found")
	}
	copied := *s
	return &copied, nil
}

func (r *stubRepo) ListServers(_ context.Context, owner snow.ID) ([]domain.StorageServer, error) {
	if err := r.hit("ListServers"); err != nil {
		return nil, err
	}
	var out []domain.StorageServer
	for _, s := range r.servers {
		if s.OwnerUserID == owner {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *stubRepo) ServerInUse(_ context.Context, id snow.ID) (bool, error) {
	if err := r.hit("ServerInUse"); err != nil {
		return false, err
	}
	return r.inUse[id], nil
}

func (r *stubRepo) DeleteServer(_ context.Context, id snow.ID) error {
	if err := r.hit("DeleteServer"); err != nil {
		return err
	}
	delete(r.servers, id)
	return nil
}

func (r *stubRepo) OrgDefault(_ context.Context, orgID snow.ID) (*snow.ID, error) {
	if err := r.hit("OrgDefault"); err != nil {
		return nil, err
	}
	id, ok := r.defaults[orgID]
	if !ok {
		return nil, nil
	}
	return &id, nil
}

func (r *stubRepo) SetOrgDefault(_ context.Context, orgID, serverID, _ snow.ID) error {
	if err := r.hit("SetOrgDefault"); err != nil {
		return err
	}
	r.defaults[orgID] = serverID
	return nil
}

func (r *stubRepo) ClearOrgDefault(_ context.Context, orgID snow.ID) (bool, error) {
	if err := r.hit("ClearOrgDefault"); err != nil {
		return false, err
	}
	_, ok := r.defaults[orgID]
	delete(r.defaults, orgID)
	return ok, nil
}

func (r *stubRepo) AssignProject(_ context.Context, projectID, serverID snow.ID) error {
	if err := r.hit("AssignProject"); err != nil {
		return err
	}
	r.projects[projectID] = serverID
	return nil
}

func (r *stubRepo) ProjectServer(_ context.Context, projectID snow.ID) (*domain.StorageServer, error) {
	if err := r.hit("ProjectServer"); err != nil {
		return nil, err
	}
	id, ok := r.projects[projectID]
	if !ok {
		return nil, nil
	}
	copied := *r.servers[id]
	return &copied, nil
}

type stubOwners struct {
	billing map[snow.ID]snow.ID
	err     error
	seeded  int
}

func (o *stubOwners) BillingOwner(_ context.Context, orgID snow.ID) (snow.ID, error) {
	o.seeded++
	return o.billing[orgID], o.err
}

func (o *stubOwners) LookupBillingOwner(_ context.Context, orgID snow.ID) (snow.ID, error) {
	return o.billing[orgID], o.err
}

type stubOrgs struct {
	owner  bool
	member bool
	err    error
}

func (o stubOrgs) IsOrgOwner(context.Context, snow.ID) (bool, error)  { return o.owner, o.err }
func (o stubOrgs) IsOrgMember(context.Context, snow.ID) (bool, error) { return o.member, o.err }

type stubTx struct{ runs int }

func (t *stubTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	t.runs++
	return fn(ctx)
}

type stubAudit struct {
	entries []domain.AuditEntry
	err     error
}

func (a *stubAudit) Record(_ context.Context, entry domain.AuditEntry) error {
	if a.err != nil {
		return a.err
	}
	a.entries = append(a.entries, entry)
	return nil
}

func (a *stubAudit) actions() []string {
	out := make([]string, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, e.Action)
	}
	return out
}

type stubEntitlement struct {
	active bool
	err    error
}

func (e stubEntitlement) OwnStorageActive(context.Context, snow.ID) (bool, error) {
	return e.active, e.err
}

type stubStore struct {
	cfg      s3store.Config
	probeErr error
	closed   bool
}

func (s *stubStore) Put(context.Context, snow.ID, domain.Hash, []byte) error    { return nil }
func (s *stubStore) Get(context.Context, snow.ID, domain.Hash) ([]byte, error)  { return nil, nil }
func (s *stubStore) Size(context.Context, snow.ID, domain.Hash) (int64, error)  { return 0, nil }
func (s *stubStore) Exists(context.Context, snow.ID, domain.Hash) (bool, error) { return false, nil }
func (s *stubStore) Close() error                                               { s.closed = true; return nil }
func (s *stubStore) Probe(context.Context) error                                { return s.probeErr }

type stubOpener struct {
	opened   []*stubStore
	openErr  error
	probeErr error
}

func (o *stubOpener) open(_ context.Context, cfg s3store.Config) (Store, error) {
	if o.openErr != nil {
		return nil, o.openErr
	}
	store := &stubStore{cfg: cfg, probeErr: o.probeErr}
	o.opened = append(o.opened, store)
	return store, nil
}

const (
	ownerID  = snow.ID(10)
	otherID  = snow.ID(11)
	orgID    = snow.ID(20)
	project1 = snow.ID(30)
)

type fixture struct {
	svc    *Service
	repo   *stubRepo
	owners *stubOwners
	tx     *stubTx
	audit  *stubAudit
	opener *stubOpener
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	f := &fixture{
		repo:   newStubRepo(),
		owners: &stubOwners{billing: map[snow.ID]snow.ID{orgID: ownerID}},
		tx:     &stubTx{},
		audit:  &stubAudit{},
		opener: &stubOpener{},
	}
	f.svc, err = New(f.repo, f.owners, f.tx, node, Config{Key: []byte("0123456789abcdef0123456789abcdef")})
	require.NoError(t, err)
	f.svc.open = f.opener.open
	f.svc.WithAudit(f.audit).WithOrgs(stubOrgs{owner: true, member: true})
	return f
}

func as(userID snow.ID) context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: userID})
}

func validInput() ServerInput {
	return ServerInput{
		Name:            "R2",
		Endpoint:        "https://acct.r2.cloudflarestorage.com",
		Region:          "auto",
		Bucket:          "assets",
		Prefix:          "/nipa/",
		AccessKeyID:     "AKID",
		SecretAccessKey: "secret",
	}
}

func (f *fixture) create(t *testing.T) *domain.StorageServer {
	t.Helper()
	server, err := f.svc.Create(as(ownerID), validInput())
	require.NoError(t, err)
	return server
}

func errorCode(err error) int {
	var e *domain.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

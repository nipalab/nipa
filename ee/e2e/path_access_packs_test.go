package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/repository/postgres"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/snow"
)

// transferGET is one chunk transfer GET a client made.
type transferGET struct {
	url      string
	hasRange bool
}

// transferRecorder records the chunk transfer GETs of one client.
type transferRecorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	gets []transferGET
}

func (r *transferRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet {
		r.mu.Lock()
		r.gets = append(r.gets, transferGET{url: req.URL.String(), hasRange: req.Header.Get("Range") != ""})
		r.mu.Unlock()
	}
	return r.base.RoundTrip(req)
}

func (r *transferRecorder) take() []transferGET {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.gets
	r.gets = nil
	return out
}

// packMember is a non-admin account of the packs access test with its own
// client and the transfers that client made.
type packMember struct {
	repo      *clientusecase.Repo
	transfers *transferRecorder
}

// newPackMember creates a plain user holding read on prefix of the default
// project and logs it in.
func newPackMember(t *testing.T, ctx context.Context, dbConn *sql.DB, host string, project snow.ID, name, prefix string) packMember {
	t.Helper()
	passwordHasher := hasher.NewHasher(1)
	t.Cleanup(passwordHasher.Close)
	passwordHash, err := passwordHasher.Hash("member-pass")
	require.NoError(t, err)
	userID := snow.ID(time.Now().UnixNano())
	email := name + "-" + userID.Base36() + "@example.com"
	_, err = dbConn.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password, is_admin) VALUES ($1, $2, $3, $4, false)`,
		userID.Int64(), name, email, passwordHash)
	require.NoError(t, err)
	var orgID int64
	require.NoError(t, dbConn.QueryRowContext(ctx, `SELECT org_id FROM projects WHERE id = $1`, project.Int64()).Scan(&orgID))
	_, err = postgres.NewPBACRepository(dbConn).CreateRule(ctx, domain.PBACRule{
		UserID: &userID, OrgID: snow.ID(orgID), ProjectID: &project, PathPrefix: prefix, Permission: domain.PermissionRead,
	})
	require.NoError(t, err)

	recorder := &transferRecorder{base: http.DefaultTransport}
	tokens := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(tokens, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session, clientgrpc.WithHTTPClient(&http.Client{Transport: recorder}))
	auth := clientusecase.NewAuth(grpcClient, tokens, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, email, "member-pass")
	require.NoError(t, err)
	require.NoError(t, tokens.SaveToken(login))
	return packMember{repo: clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()), transfers: recorder}
}

// replay sends the GET again the way an attacker holding the URL would:
// without a Range, and asking for everything from the first byte.
func replay(t *testing.T, url string) [][]byte {
	t.Helper()
	var bodies [][]byte
	for _, rangeHeader := range []string{"", "bytes=0-"} {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, res.Body.Close())
		require.NoError(t, err)
		require.Less(t, res.StatusCode, 300, "replayed transfer URL: %s", body)
		bodies = append(bodies, body)
	}
	return bodies
}

// TestEnterprisePacksPathAccess pushes a visible and a hidden folder in one
// push with CHUNK_PACKS=on, so their chunks share a pack, and checks what
// the download URLs of three readers let them read: a reader limited to the
// visible folder must not be able to read the hidden folder's bytes with any
// URL it was given, whatever Range it sends, while readers of the whole
// project keep ranged GETs straight from the pack object.
func TestEnterprisePacksPathAccess(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	store := newS3ChunkStore(t).(*s3store.Store)
	host, uc := startEnterpriseServerWith(t, dbConn, store, mustSnowNode(t, 0), serverOptions{uploadMode: config.ChunkUploadModeProxy, packs: true})
	adminRecorder := &transferRecorder{base: http.DefaultTransport}
	adminRepo, pusher := connectClient(t, host, clientgrpc.WithHTTPClient(&http.Client{Transport: adminRecorder}))
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	project := defaultProjectID(t, dbConn)

	suffix := snow.ID(time.Now().UnixNano()).Base36()
	pubDir, secretDir := "pub-"+suffix, "secret-"+suffix
	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, adminRepo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, work))
	contents := map[string][]byte{}
	var paths []string
	for _, path := range []string{pubDir + "/a.bin", secretDir + "/a.bin", pubDir + "/b.bin", secretDir + "/b.bin"} {
		raw := make([]byte, 150<<10)
		_, err := rand.Read(raw)
		require.NoError(t, err)
		contents[path] = raw
		fp := filepath.Join(work, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(fp), 0o755))
		require.NoError(t, os.WriteFile(fp, raw, 0o644))
		paths = append(paths, path)
	}
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(work))
	require.NoError(t, lr.StageAddMany(paths))
	require.NoError(t, lr.Close())
	known := map[snow.ID]bool{}
	for _, id := range newPackIDs(t, dbConn, project, time.Time{}) {
		known[id] = true
	}
	require.NoError(t, pusher.Run(ctx, work, "a visible and a hidden folder"))
	var packIDs []snow.ID
	for _, id := range newPackIDs(t, dbConn, project, time.Time{}) {
		if !known[id] {
			packIDs = append(packIDs, id)
		}
	}
	require.Len(t, packIDs, 1, "one push, one bundle, one pack holding both folders")
	packPath := "/packs/" + project.Base36() + "/" + packIDs[0].Base36()

	restricted := newPackMember(t, ctx, dbConn, host, project, "restricted", pubDir)
	whole := newPackMember(t, ctx, dbConn, host, project, "whole", "")
	uc.Permission.InvalidateAll()

	// secrets are windows of the hidden files' bytes; random content is
	// stored raw, so they appear verbatim wherever the content is served.
	secrets := [][]byte{contents[secretDir+"/a.bin"][1000:1064], contents[secretDir+"/b.bin"][70000:70064]}
	leaks := func(body []byte) bool {
		for _, secret := range secrets {
			if bytes.Contains(body, secret) {
				return true
			}
		}
		return false
	}
	ourTransfers := func(gets []transferGET) (pack, server []transferGET) {
		for _, get := range gets {
			switch {
			case strings.Contains(get.url, packPath):
				pack = append(pack, get)
			case strings.Contains(get.url, "/api/chunks/"):
				server = append(server, get)
			}
		}
		return pack, server
	}
	requireFile := func(dir, path string) {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		require.NoError(t, err, path)
		require.True(t, bytes.Equal(contents[path], got), path)
	}

	t.Run("a reader of the whole project reads ranges of the pack object", func(t *testing.T) {
		for name, clone := range map[string]func(target string) []transferGET{
			"admin": func(target string) []transferGET {
				adminRecorder.take()
				require.NoError(t, adminRepo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
				return adminRecorder.take()
			},
			"member with a rule on the root": func(target string) []transferGET {
				require.NoError(t, whole.repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
				return whole.transfers.take()
			},
		} {
			checkout := filepath.Join(t.TempDir(), "checkout")
			pack, _ := ourTransfers(clone(checkout))
			require.NotEmpty(t, pack, name)
			require.LessOrEqual(t, len(pack), 4, "%s: merged range GETs", name)
			for _, get := range pack {
				require.True(t, get.hasRange, name)
			}
			for _, path := range paths {
				requireFile(checkout, path)
			}
			// The pack does hold the hidden bytes: the check below means
			// something.
			leaked := false
			for _, body := range replay(t, pack[0].url) {
				leaked = leaked || leaks(body)
			}
			require.True(t, leaked, "%s: the pack object holds both folders", name)
		}
	})

	t.Run("a reader of one folder cannot read the other folder's bytes", func(t *testing.T) {
		checkout := filepath.Join(t.TempDir(), "checkout")
		require.NoError(t, restricted.repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
		gets := restricted.transfers.take()
		requireFile(checkout, pubDir+"/a.bin")
		requireFile(checkout, pubDir+"/b.bin")
		_, err := os.Stat(filepath.Join(checkout, secretDir))
		require.True(t, os.IsNotExist(err), "the hidden folder is not checked out")

		pack, server := ourTransfers(gets)
		require.NotEmpty(t, gets)
		for _, get := range gets {
			for _, body := range replay(t, get.url) {
				require.False(t, leaks(body), "a transfer URL of the restricted reader serves hidden bytes: %s", get.url)
			}
		}
		require.Empty(t, pack, "no URL of the pack object reaches a path-restricted reader")
		require.NotEmpty(t, server, "its packed chunks come through the signed server route")
		for _, get := range server {
			require.False(t, get.hasRange)
			require.True(t, strings.HasPrefix(get.url, "http://"+host+"/api/chunks/"), get.url)
		}
	})
}

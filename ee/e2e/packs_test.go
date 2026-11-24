package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	serverusecase "github.com/nipalab/nipa/internal/usecase"
)

// packCountingStore is the S3 store with its pack writes, range reads and
// loose writes and reads counted.
type packCountingStore struct {
	*s3store.Store
	packPuts   atomic.Int64
	ranges     atomic.Int64
	overwrites atomic.Int64
	reads      atomic.Int64
	exists     atomic.Int64
}

func (s *packCountingStore) PutPack(ctx context.Context, key string, body io.ReadSeeker, size int64) error {
	s.packPuts.Add(1)
	return s.Store.PutPack(ctx, key, body, size)
}

func (s *packCountingStore) GetRange(ctx context.Context, key string, offset, length int64) ([]byte, error) {
	s.ranges.Add(1)
	return s.Store.GetRange(ctx, key, offset, length)
}

func (s *packCountingStore) Overwrite(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	s.overwrites.Add(1)
	return s.Store.Overwrite(ctx, projectID, hash, data)
}

func (s *packCountingStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.Store.Get(ctx, projectID, hash)
}

func (s *packCountingStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.Store.Open(ctx, projectID, hash)
}

func (s *packCountingStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	s.exists.Add(1)
	return s.Store.Exists(ctx, projectID, hash)
}

// rangeCountingTransport counts the client's ranged GETs per URL path.
type rangeCountingTransport struct {
	base   http.RoundTripper
	ranged atomic.Int64
	paths  chan string
}

func (c *rangeCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet && req.Header.Get("Range") != "" {
		c.ranged.Add(1)
		select {
		case c.paths <- req.URL.Path:
		default:
		}
	}
	return c.base.RoundTrip(req)
}

// newPackIDs returns the ids of the packs created since before.
func newPackIDs(t *testing.T, dbConn *sql.DB, project snow.ID, before time.Time) []snow.ID {
	t.Helper()
	rows, err := dbConn.QueryContext(context.Background(),
		`SELECT id FROM chunk_packs WHERE project_id = $1 AND created_at >= $2 ORDER BY id`, project.Int64(), before)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var ids []snow.ID
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, snow.ID(id))
	}
	require.NoError(t, rows.Err())
	return ids
}

// TestEnterprisePacks pushes ~1000 chunks through MinIO with CHUNK_PACKS=on:
// the bundle is written as one pack object (no loose object, no read-back),
// confirm records the pack locations, and a clone reads the chunks back with
// a few merged Range requests and checks every byte. A merge then writes its
// text merge as a pack, readable through the web/browser path.
func TestEnterprisePacks(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN, eedb.PoolConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	store := &packCountingStore{Store: newS3ChunkStore(t).(*s3store.Store)}
	host, uc := startEnterpriseServerWith(t, dbConn, store, mustSnowNode(t, 0), serverOptions{uploadMode: config.ChunkUploadModeProxy, packs: true})
	ranges := &rangeCountingTransport{base: http.DefaultTransport, paths: make(chan string, 1024)}
	repo, pusher := connectClient(t, host, clientgrpc.WithHTTPClient(&http.Client{Transport: ranges}))
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	project := defaultProjectID(t, dbConn)

	work := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, work))
	const files = 1000
	dir := "packed-" + snow.ID(time.Now().UnixNano()).Base36()
	contents := make(map[string]string, files)
	paths := make([]string, 0, files)
	for i := 0; i < files; i++ {
		raw := make([]byte, 600)
		_, err := rand.Read(raw)
		require.NoError(t, err)
		path := fmt.Sprintf("%s/f%04d.txt", dir, i)
		contents[path] = hex.EncodeToString(raw) + "\n"
		writeFile(t, work, path, contents[path])
		paths = append(paths, path)
	}
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(work))
	require.NoError(t, lr.StageAddMany(paths))
	require.NoError(t, lr.Close())

	before := time.Now().Add(-time.Second)
	rowsBefore := projectChunkRows(t, dbConn, project)
	require.NoError(t, pusher.Run(ctx, work, "a thousand packed chunks"))
	newRows := projectChunkRows(t, dbConn, project) - rowsBefore
	require.GreaterOrEqual(t, newRows, int64(files))

	packIDs := newPackIDs(t, dbConn, project, before)
	require.NotEmpty(t, packIDs)
	require.LessOrEqual(t, store.packPuts.Load(), int64(2), "one store write per bundle")
	require.Equal(t, int64(len(packIDs)), store.packPuts.Load())
	require.Zero(t, store.overwrites.Load(), "no loose object in packs mode")
	require.Zero(t, store.reads.Load(), "receipts replace every confirm read-back")
	require.Zero(t, store.exists.Load(), "a fresh push makes no existence checks")
	var packedRows, stored int64
	require.NoError(t, dbConn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks WHERE project_id = $1 AND pack_id = ANY($2)`, project.Int64(), snowIDs(packIDs)).Scan(&packedRows))
	require.Equal(t, newRows, packedRows, "every new row points into a pack")
	require.NoError(t, dbConn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunk_packs WHERE id = ANY($1) AND state = 'stored'`, snowIDs(packIDs)).Scan(&stored))
	require.Equal(t, int64(len(packIDs)), stored)

	// The clone reads the new chunks with merged Range requests.
	for len(ranges.paths) > 0 {
		<-ranges.paths
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	close(ranges.paths)
	ourRanges := 0
	for path := range ranges.paths {
		for _, id := range packIDs {
			if strings.HasSuffix(path, "/packs/"+project.Base36()+"/"+id.Base36()) {
				ourRanges++
			}
		}
	}
	t.Logf("push: %d new chunk rows, %d pack writes; clone: %d range GETs of the new packs", newRows, store.packPuts.Load(), ourRanges)
	require.Positive(t, ourRanges)
	require.LessOrEqual(t, ourRanges, 4, "about 1000 chunks come in a few range GETs")
	for _, path := range paths {
		require.Equal(t, contents[path], readFile(t, checkout, path), path)
	}

	t.Run("merge output is packed and readable in the browser", func(t *testing.T) {
		mergePacked(t, ctx, dbConn, host, uc, store, project)
	})
}

func snowIDs(ids []snow.ID) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.Int64())
	}
	return out
}

// mergePacked diverges a text file on a branch and on main, merges with the
// merge strategy (a server-side text merge written through the pack path)
// and reads the merged file through the browser usecase.
func mergePacked(t *testing.T, ctx context.Context, dbConn *sql.DB, host string, uc serverapp.Usecases, store *packCountingStore, project snow.ID) {
	var superID int64
	require.NoError(t, dbConn.QueryRowContext(ctx, `SELECT id FROM users WHERE email = $1 OR name = $1 ORDER BY id LIMIT 1`, e2eSuperAdmin).Scan(&superID))
	admin := domain.ContextWithClaim(ctx, domain.Claims{UserID: snow.ID(superID), IsAdmin: true, IsSuperAdmin: true})

	tokens := newMemoryStore()
	transport := clientgrpc.NewTransport()
	session := clientusecase.NewSession(tokens, transport, failPrompt{})
	grpcClient := clientgrpc.NewClient(transport, session)
	auth := clientusecase.NewAuth(grpcClient, tokens, failPrompt{})
	require.NoError(t, grpcClient.Connect(ctx, host))
	login, err := grpcClient.LoginWithUsernamePassword(ctx, host, e2eSuperAdmin, e2eSuperPass)
	require.NoError(t, err)
	require.NoError(t, tokens.SaveToken(login))
	repo := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo())
	push := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug

	suffix := snow.ID(time.Now().UnixNano()).Base36()
	path := "notes-" + suffix + ".txt"
	feature := "packed-merge-" + suffix
	base := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\n"

	main := filepath.Join(t.TempDir(), "main")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, main))
	writeFile(t, main, path, base)
	stagePath(t, main, path)
	require.NoError(t, push.Run(ctx, main, "notes"))

	_, err = grpcClient.CreateBranch(ctx, e2eOrgSlug, e2eProjectSlug, feature, "main", "", "")
	require.NoError(t, err)
	side := filepath.Join(t.TempDir(), "side")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, feature, nil, side))
	writeFile(t, side, path, strings.Replace(base, "line 1\n", "line 1 (feature)\n", 1))
	stagePath(t, side, path)
	require.NoError(t, push.Run(ctx, side, "feature edit"))

	writeFile(t, main, path, strings.Replace(base, "line 8\n", "line 8 (main)\n", 1))
	stagePath(t, main, path)
	require.NoError(t, push.Run(ctx, main, "main edit"))

	packsBefore := store.packPuts.Load()
	_, err = uc.Branch.MergeForMergeRequest(admin, project, "main", feature, serverusecase.MergeCommitOptions{
		Strategy: domain.MergeStrategyMerge,
		Message:  "merge " + feature,
		Author:   snow.ID(superID),
	})
	require.NoError(t, err)
	require.Greater(t, store.packPuts.Load(), packsBefore, "the text merge is written as a pack")

	want := strings.Replace(strings.Replace(base, "line 1\n", "line 1 (feature)\n", 1), "line 8\n", "line 8 (main)\n", 1)
	rangesBefore := store.ranges.Load()
	content, _, err := uc.Branch.FileContent(admin, project, "main", path)
	require.NoError(t, err)
	require.Equal(t, want, string(content))
	require.Greater(t, store.ranges.Load(), rangesBefore, "the browser reads the merged chunk from its pack")
}

package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/ee/chunkgc"
	eedb "github.com/nipalab/nipa/ee/db"
	"github.com/nipalab/nipa/ee/projectpurge"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
)

// TestEnterpriseDeleteProjectReleasesStorage drives the real wiring: deleting
// a project through the usecase graph gives its bytes back to the owner's
// quota, in the same transaction as the audit entry, and a recount agrees.
func TestEnterpriseDeleteProjectReleasesStorage(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	host, uc := startEnterpriseServerWith(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), serverOptions{ledger: ledger})

	suffix := randomSuffix(t)
	admin := domain.ContextWithClaim(ctx, domain.Claims{UserID: 1, IsSuperAdmin: true})
	org, err := uc.Org.Create(admin, "Reclaim "+suffix, "reclaim-"+suffix)
	require.NoError(t, err)
	_, err = uc.Project.Create(admin, org.ID, "Keep", "", "keep", nil)
	require.NoError(t, err)
	drop, err := uc.Project.Create(admin, org.ID, "Drop", "", "drop", nil)
	require.NoError(t, err)

	repo, pusher := connectClient(t, host)
	push := func(slug, content string) {
		url := "http://" + host + "/" + org.Slug + "/" + slug
		target := filepath.Join(t.TempDir(), slug)
		require.NoError(t, repo.Clone(ctx, url, host, org.Slug, slug, "", nil, target))
		writeFile(t, target, "asset.txt", content)
		stagePath(t, target, "asset.txt")
		require.NoError(t, pusher.Run(ctx, target, "add asset"))
	}
	push("keep", "kept content "+suffix+"\n")
	push("drop", "dropped content, a little longer so the sizes differ "+suffix+"\n")

	usage := func() (int64, map[string]storageusage.ProjectUsage) {
		summary, err := ledger.OrgStorage(ctx, org.ID)
		require.NoError(t, err)
		byProject := map[string]storageusage.ProjectUsage{}
		for _, project := range summary.Projects {
			byProject[project.Slug] = project
		}
		return summary.UsedBytes, byProject
	}
	before, projects := usage()
	keepBytes, dropBytes := projects["keep"].UsedBytes, projects["drop"].UsedBytes
	require.Positive(t, keepBytes)
	require.Positive(t, dropBytes)
	require.Equal(t, keepBytes+dropBytes, before)

	require.NoError(t, uc.Project.Delete(admin, drop.ID))

	after, projects := usage()
	require.Equal(t, keepBytes, after, "the deleted project's bytes are released")
	require.True(t, projects["drop"].Deleted)
	require.Zero(t, projects["drop"].UsedBytes)

	var released int64
	require.NoError(t, dbConn.QueryRowContext(ctx,
		`SELECT (metadata->>'released_bytes')::bigint FROM audit_log WHERE action = $1 AND target_id = $2`,
		domain.AuditProjectDeleted, drop.ID.Base36(),
	).Scan(&released))
	require.Equal(t, dropBytes, released, "the audit entry records the released bytes")

	require.NoError(t, ledger.RecountOrg(ctx, org.ID))
	recounted, _ := usage()
	require.Equal(t, keepBytes, recounted, "a recount does not bill the deleted project again")
}

// TestEnterpriseRestoreAndPurgeProject drives restore and purge against a real
// server: a restored project is charged again and accepts pushes; a purged one
// is gone for good, frees its slug, and leaves the other projects intact.
func TestEnterpriseRestoreAndPurgeProject(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	host, uc := startEnterpriseServerWith(t, dbConn, newS3ChunkStore(t), mustSnowNode(t, 0), serverOptions{ledger: ledger})

	suffix := randomSuffix(t)
	admin := domain.ContextWithClaim(ctx, domain.Claims{UserID: 1, IsSuperAdmin: true})
	org, err := uc.Org.Create(admin, "Purge "+suffix, "purge-"+suffix)
	require.NoError(t, err)
	_, err = uc.Project.Create(admin, org.ID, "Keep", "", "keep", nil)
	require.NoError(t, err)
	game, err := uc.Project.Create(admin, org.ID, "Game", "", "game", nil)
	require.NoError(t, err)

	repo, pusher := connectClient(t, host)
	push := func(slug, content string) {
		url := "http://" + host + "/" + org.Slug + "/" + slug
		target := filepath.Join(t.TempDir(), slug)
		require.NoError(t, repo.Clone(ctx, url, host, org.Slug, slug, "", nil, target))
		writeFile(t, target, "asset.txt", content)
		stagePath(t, target, "asset.txt")
		require.NoError(t, pusher.Run(ctx, target, "add asset"))
	}
	push("keep", "kept content "+suffix+"\n")
	push("game", "game content, sized differently "+suffix+"\n")

	used := func() int64 {
		summary, err := ledger.OrgStorage(ctx, org.ID)
		require.NoError(t, err)
		return summary.UsedBytes
	}
	full := used()

	require.NoError(t, uc.Project.Delete(admin, game.ID))
	afterDelete := used()
	require.Less(t, afterDelete, full)

	listed, err := uc.Project.ListDeleted(admin, org.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	restored, err := uc.Project.Restore(admin, org.ID, game.ID)
	require.NoError(t, err)
	require.Equal(t, "game", restored.Slug)
	require.Equal(t, full, used(), "the restore charges the project's history back")
	push("game", "pushed after the restore "+suffix+"\n")

	require.NoError(t, uc.Project.Delete(admin, game.ID))
	purger, err := projectpurge.New(dbConn, projectpurge.Config{Retention: time.Nanosecond, BatchSize: 1})
	require.NoError(t, err)
	purger.WithAudit(uc.Audit).WithReleaser(ledger)
	stats, err := purger.PurgeProject(ctx, game.ID, time.Now().Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, game.ID, stats.ProjectID)
	require.Positive(t, stats.Commits)

	var rows int
	require.NoError(t, dbConn.QueryRowContext(ctx, `SELECT count(*) FROM commits WHERE project_id = $1`, game.ID.Int64()).Scan(&rows))
	require.Zero(t, rows)
	_, err = uc.Project.Restore(admin, org.ID, game.ID)
	require.True(t, domain.IsErrorNotFound(err), "a purged project cannot come back, got %v", err)

	again, err := uc.Project.Create(admin, org.ID, "Game", "", "game", nil)
	require.NoError(t, err, "the purge frees the slug")
	require.NotEqual(t, game.ID, again.ID)

	checkout := filepath.Join(t.TempDir(), "keep-checkout")
	url := "http://" + host + "/" + org.Slug + "/keep"
	require.NoError(t, repo.Clone(ctx, url, host, org.Slug, "keep", "", nil, checkout))
	require.Equal(t, "kept content "+suffix+"\n", readFile(t, checkout, "asset.txt"))
	require.Equal(t, afterDelete, used(), "the other project keeps its usage")
}

// TestEnterpriseChunkGCReclaimsPurgedContent closes the loop: content only a
// purged project referenced is deleted from the bucket by the collector, and
// pushing the same content again later uploads it afresh.
func TestEnterpriseChunkGCReclaimsPurgedContent(t *testing.T) {
	ctx := context.Background()

	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	store := newS3ChunkStore(t)
	objects, ok := store.(chunkgc.ObjectStore)
	require.True(t, ok)
	ledger := storageusage.NewLedger(dbConn, storageusage.Config{})
	host, uc := startEnterpriseServerWith(t, dbConn, store, mustSnowNode(t, 0), serverOptions{
		ledger:  ledger,
		gcGuard: chunkgc.NewGuard(dbConn),
	})

	suffix := randomSuffix(t)
	admin := domain.ContextWithClaim(ctx, domain.Claims{UserID: 1, IsSuperAdmin: true})
	org, err := uc.Org.Create(admin, "GC "+suffix, "gc-"+suffix)
	require.NoError(t, err)
	gone, err := uc.Project.Create(admin, org.ID, "Gone", "", "gone", nil)
	require.NoError(t, err)
	_, err = uc.Project.Create(admin, org.ID, "Later", "", "later", nil)
	require.NoError(t, err)

	repo, pusher := connectClient(t, host)
	content := "content only the purged project held " + suffix + "\n"
	push := func(slug string) {
		url := "http://" + host + "/" + org.Slug + "/" + slug
		target := filepath.Join(t.TempDir(), slug)
		require.NoError(t, repo.Clone(ctx, url, host, org.Slug, slug, "", nil, target))
		writeFile(t, target, "asset.txt", content)
		stagePath(t, target, "asset.txt")
		require.NoError(t, pusher.Run(ctx, target, "add asset"))
	}
	push("gone")

	var hashes []domain.Hash
	rows, err := dbConn.QueryContext(ctx, `SELECT hash FROM project_chunk_uploads WHERE project_id = $1`, gone.ID.Int64())
	require.NoError(t, err)
	for rows.Next() {
		var raw []byte
		require.NoError(t, rows.Scan(&raw))
		var hash domain.Hash
		copy(hash[:], raw)
		hashes = append(hashes, hash)
	}
	require.NoError(t, rows.Close())
	require.NotEmpty(t, hashes)

	require.NoError(t, uc.Project.Delete(admin, gone.ID))
	purger, err := projectpurge.New(dbConn, projectpurge.Config{Retention: time.Nanosecond})
	require.NoError(t, err)
	_, err = purger.WithReleaser(ledger).PurgeProject(ctx, gone.ID, time.Now().Add(time.Second))
	require.NoError(t, err)

	collector, err := chunkgc.New(dbConn, objects, chunkgc.Config{Mode: chunkgc.ModeOn, Grace: time.Millisecond})
	require.NoError(t, err)
	_, err = collector.Run(ctx)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)
	report, err := collector.Run(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, report.Deleted, int64(len(hashes)))
	for _, hash := range hashes {
		exists, err := store.Exists(ctx, hash)
		require.NoError(t, err)
		require.False(t, exists, "the bucket no longer holds %s", hash)
	}

	push("later")
	checkout := filepath.Join(t.TempDir(), "later-checkout")
	url := "http://" + host + "/" + org.Slug + "/later"
	require.NoError(t, repo.Clone(ctx, url, host, org.Slug, "later", "", nil, checkout))
	require.Equal(t, content, readFile(t, checkout, "asset.txt"), "the same content is uploaded again and readable")
}

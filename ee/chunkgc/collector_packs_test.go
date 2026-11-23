package chunkgc

import (
	"context"
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// pack records a pack row of project in state with its object; it returns
// the object's key.
func (s *gcSuite) pack(project snow.ID, id int64, state string, size int64) string {
	s.T().Helper()
	s.exec(`INSERT INTO chunk_packs (id, project_id, size_bytes, chunk_count, state) VALUES ($1, $2, $3, 1, $4)`,
		id, project.Int64(), size, state)
	key := storage.PackKey(project, snow.ID(id))
	s.store.putPack(key)
	return key
}

// packedChunk records a chunk row of project whose bytes are in pack id.
func (s *gcSuite) packedChunk(project snow.ID, packID int64, size int64) (domain.Hash, int64) {
	s.T().Helper()
	hash := newHash()
	var id int64
	s.Require().NoError(s.db.QueryRowContext(context.Background(),
		`INSERT INTO chunks (project_id, hash, size_bytes, pack_id, pack_offset) VALUES ($1, $2, $3, $4, 4) RETURNING id`,
		project.Int64(), hash[:], size, packID).Scan(&id))
	return hash, id
}

func (s *gcSuite) hasPackRow(id int64) bool {
	return s.count(`SELECT count(*) FROM chunk_packs WHERE id = $1`, id) == 1
}

// TestPackedRowsDeleteNoObjects: an unreferenced packed row is swept like a
// loose one, but only the row goes; its pack is then unreferenced, marked,
// and deleted (object and row) a grace period later.
func (s *gcSuite) TestPackedRowsDeleteNoObjects() {
	ctx := context.Background()
	key := s.pack(defaultProject, 101, "stored", 500)
	garbage, _ := s.packedChunk(defaultProject, 101, 40)
	keptKey := s.pack(defaultProject, 102, "stored", 300)
	_, keptID := s.packedChunk(defaultProject, 102, 10)
	s.reference(keptID)

	report, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Marked)
	s.Zero(report.PacksMarked, "both packs still have rows")

	report, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.Deleted)
	s.Equal(int64(40), report.DeletedBytes)
	s.False(s.hasRow(garbage))
	s.Empty(s.store.deleted, "a packed row has no object of its own")
	s.True(s.store.hasPack(key))
	s.Equal(int64(1), report.PacksMarked, "the emptied pack is marked in the same run")
	s.Equal(int64(500), report.PacksMarkedBytes)
	s.Zero(report.PacksDeleted, "and waits out the grace")

	report, err = s.collector(ModeOn, s.now.Add(50*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.PacksDeleted)
	s.Equal(int64(500), report.PacksDeletedBytes)
	s.False(s.store.hasPack(key))
	s.False(s.hasPackRow(101))
	s.True(s.store.hasPack(keptKey), "a referenced pack stays")
	s.True(s.hasPackRow(102))
	s.Equal([]string{key}, s.store.packDels)
}

// TestUnreferencedAndPendingPacksAfterGrace: a pack nothing points at (a
// bundle whose confirm never ran) and a pending pack (a failed write) are
// swept only after the grace period; a pack that gains rows meanwhile loses
// its mark and stays.
func (s *gcSuite) TestUnreferencedAndPendingPacksAfterGrace() {
	ctx := context.Background()
	orphan := s.pack(defaultProject, 201, "stored", 100)
	pending := s.pack(secondProject, 202, "pending", 200)
	confirmed := s.pack(defaultProject, 203, "pending", 300)

	report, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(3), report.PacksMarked)
	s.Equal(int64(600), report.PacksMarkedBytes)

	// The pending pack finished and its confirm recorded a row.
	s.exec(`UPDATE chunk_packs SET state = 'stored' WHERE id = 203`)
	_, rowID := s.packedChunk(defaultProject, 203, 10)
	s.reference(rowID)

	report, err = s.collector(ModeOn, s.now.Add(time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Zero(report.PacksDeleted, "inside the grace nothing goes")
	s.Zero(report.PacksMarked, "marks are not renewed")
	s.Equal(0, s.count(`SELECT count(*) FROM chunk_packs WHERE id = 203 AND gc_marked_at IS NOT NULL`), "a referenced pack loses its mark")

	report, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(2), report.PacksDeleted)
	s.Equal(int64(300), report.PacksDeletedBytes)
	s.False(s.store.hasPack(orphan))
	s.False(s.store.hasPack(pending))
	s.True(s.store.hasPack(confirmed))
	s.False(s.hasPackRow(201))
	s.False(s.hasPackRow(202))
	s.True(s.hasPackRow(203))
}

func (s *gcSuite) TestPackDryRunDeletesNothing() {
	ctx := context.Background()
	key := s.pack(defaultProject, 301, "stored", 70)

	_, err := s.collector(ModeDryRun, s.now).Run(ctx)
	s.Require().NoError(err)
	report, err := s.collector(ModeDryRun, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.PackCandidates)
	s.Equal(int64(70), report.PackCandidatesBytes)
	s.Zero(report.PacksDeleted)
	s.True(s.store.hasPack(key))
	s.True(s.hasPackRow(301))
}

func (s *gcSuite) TestPackDeleteFailureKeepsTheRow() {
	ctx := context.Background()
	key := s.pack(defaultProject, 401, "pending", 70)
	_, err := s.collector(ModeOn, s.now).Run(ctx)
	s.Require().NoError(err)

	s.store.failPack = errors.New("bucket down")
	_, err = s.collector(ModeOn, s.now.Add(25*time.Hour)).Run(ctx)
	s.Require().ErrorContains(err, "bucket down")
	s.True(s.hasPackRow(401), "the batch rolls back")
	s.True(s.store.hasPack(key))

	s.store.failPack = nil
	report, err := s.collector(ModeOn, s.now.Add(26*time.Hour)).Run(ctx)
	s.Require().NoError(err)
	s.Equal(int64(1), report.PacksDeleted, "the next run retries")
}

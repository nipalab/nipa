package projectpurge

import (
	"context"
	"time"
)

// TestPurgeProject_RemovesPacks: the purge deletes the project's pack rows
// (after its chunk rows) and leaves other projects' packs alone; the
// objects go with the store's prefix delete (p/<project>/ and
// packs/<project>/, see ee/storage/s3).
func (s *purgeSuite) TestPurgeProject_RemovesPacks() {
	ctx := context.Background()
	purged := s.project("packed-game")
	live := s.project("packed-live")
	for _, project := range []int64{purged.Int64(), live.Int64()} {
		packID := nextID()
		s.exec(`INSERT INTO chunk_packs (id, project_id, size_bytes, chunk_count, state) VALUES ($1, $2, 100, 2, 'stored')`, packID, project)
		s.exec(`INSERT INTO chunk_packs (id, project_id, size_bytes, chunk_count, state) VALUES ($1, $2, 100, 0, 'pending')`, nextID(), project)
		for j := 0; j < 2; j++ {
			s.exec(`INSERT INTO chunks (project_id, hash, size_bytes, pack_id, pack_offset) VALUES ($1, $2, 10, $3, $4)`,
				project, uniqueHash(), packID, 4+10*j)
		}
	}
	s.deleteProject(purged, 31*24*time.Hour)

	store := &fakeStore{}
	stats, err := s.newPurger(1).WithStore(store).PurgeProject(ctx, purged, s.now.Add(-30*24*time.Hour))
	s.Require().NoError(err)
	s.Equal(int64(2), stats.Chunks)
	s.Equal(int64(2), stats.Packs)
	s.Zero(s.count(`SELECT count(*) FROM chunk_packs WHERE project_id = $1`, purged.Int64()))
	s.Equal(2, s.count(`SELECT count(*) FROM chunk_packs WHERE project_id = $1`, live.Int64()))
	s.Equal(2, s.count(`SELECT count(*) FROM chunks WHERE project_id = $1`, live.Int64()))
	s.Zero(s.count(`SELECT count(*) FROM projects WHERE id = $1`, purged.Int64()))
}

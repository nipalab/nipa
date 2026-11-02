// Enterprise Edition: see ee/LICENSE.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

// TestApplyPushScale times one ApplyPush of NIPA_BENCH_FILES single-chunk files
// spread over ten directories. Skipped unless the variable is set; chunk setup
// is not part of the measurement. NIPA_BENCH_LATENCY (a duration such as 500us)
// puts that one-way delay between the repository and postgres, which is what a
// pod in another host or zone sees; each statement then costs a round trip.
func (s *PushRepositorySuite) TestApplyPushScale() {
	total, err := strconv.Atoi(os.Getenv("NIPA_BENCH_FILES"))
	if err != nil || total <= 0 {
		s.T().Skip("set NIPA_BENCH_FILES=<count> to run the ApplyPush scale harness")
	}
	const dirs = 10
	ctx := context.Background()
	repo, _, projectID, branchID := s.newPushTestEnv()

	nodes := []usecase.PushNodeRow{{ID: 1, Hash: chunker.Sum([]byte("root")), Name: "root", Mode: 0o444}}
	for d := 0; d < dirs; d++ {
		name := fmt.Sprintf("d%02d", d)
		parent := int64(1)
		nodes = append(nodes, usecase.PushNodeRow{ID: int64(d + 2), Hash: chunker.Sum([]byte(name)), Name: name, Mode: 0o444, ParentID: &parent})
	}
	files := make([]usecase.PushFileRow, 0, total)
	for i := 0; i < total; i++ {
		chunkHash, size, fileHash := chunkRow(s.T(), fmt.Sprintf("scale file %06d", i))
		s.Require().NoError(repo.InsertChunkIfNotExists(ctx, chunkHash, size))
		s.Require().NoError(repo.LinkProjectChunk(ctx, projectID, chunkHash))
		files = append(files, usecase.PushFileRow{
			Name: fmt.Sprintf("f%06d.txt", i), Mode: 0o644, SizeBytes: size, Hash: fileHash,
			TreeID: int64(i%dirs + 2), ChunkHashes: []domain.Hash{chunkHash},
		})
	}

	applyRepo := repo
	if raw := os.Getenv("NIPA_BENCH_LATENCY"); raw != "" {
		oneWay, err := time.ParseDuration(raw)
		s.Require().NoError(err)
		applyRepo = NewPushRepository(s.openThroughLatency(oneWay))
		s.T().Logf("one-way latency %s (round trip %s)", oneWay, 2*oneWay)
	}

	start := time.Now()
	s.Require().NoError(applyRepo.ApplyPush(ctx, usecase.ApplyPushRequest{
		ProjectID:  projectID,
		BranchID:   branchID,
		CommitID:   snow.ID(777),
		CommitHash: treehash.CommitHash(treehash.TreeHash(nil, nil), nil, "scale"),
		UserID:     snow.ID(1),
		Message:    "scale",
		Nodes:      nodes,
		Files:      files,
	}))
	s.T().Logf("ApplyPush %d files: %s", total, time.Since(start).Round(time.Millisecond))
}

func (s *PushRepositorySuite) openThroughLatency(oneWay time.Duration) *sql.DB {
	var schema string
	s.Require().NoError(s.db.QueryRow("SELECT current_schema()").Scan(&schema))

	target, err := url.Parse(testDSN)
	s.Require().NoError(err)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = lis.Close() })
	go serveLatencyProxy(lis, target.Host, oneWay)

	proxied := *target
	proxied.Host = lis.Addr().String()
	db, err := sql.Open("pgx", withSearchPath(proxied.String(), schema))
	s.Require().NoError(err)
	db.SetMaxOpenConns(4)
	s.T().Cleanup(func() { _ = db.Close() })
	return db
}

func serveLatencyProxy(lis net.Listener, backend string, oneWay time.Duration) {
	for {
		client, err := lis.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", backend)
		if err != nil {
			_ = client.Close()
			continue
		}
		go delayCopy(server, client, oneWay)
		go delayCopy(client, server, oneWay)
	}
}

// delayCopy forwards src to dst, releasing every read buffer oneWay after it
// arrived. Buffers queue up instead of sleeping in the read loop, so latency is
// added without limiting throughput.
func delayCopy(dst, src net.Conn, oneWay time.Duration) {
	type packet struct {
		data []byte
		due  time.Time
	}
	queue := make(chan packet, 1024)
	go func() {
		defer func() { _ = dst.Close() }()
		for p := range queue {
			waitUntil(p.due)
			if _, err := dst.Write(p.data); err != nil {
				return
			}
		}
	}()
	defer close(queue)
	buf := make([]byte, 64<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			queue <- packet{data: append([]byte(nil), buf[:n]...), due: time.Now().Add(oneWay)}
		}
		if err != nil {
			if err != io.EOF {
				_ = src.Close()
			}
			return
		}
	}
}

// waitUntil sleeps for long waits and spins for the last stretch: timers are
// only about a millisecond accurate, which would swamp a sub-millisecond link.
func waitUntil(due time.Time) {
	if wait := time.Until(due); wait > 2*time.Millisecond {
		time.Sleep(wait - 2*time.Millisecond)
	}
	for time.Now().Before(due) {
		runtime.Gosched()
	}
}

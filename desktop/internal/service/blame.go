package service

import (
	"context"
	"errors"
	"io"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

// BlameLine names the commit that last changed one line. CommitID is empty
// when the line is not pushed yet (Uncommitted) or older than the history
// that could be read.
type BlameLine struct {
	CommitID    string `json:"commitId"`
	Uncommitted bool   `json:"uncommitted"`
}

// Blame attributes the lines of a file version, in line order.
type Blame struct {
	Path      string      `json:"path"`
	Revision  string      `json:"revision"`
	Lines     []BlameLine `json:"lines"`
	Commits   []Commit    `json:"commits"`
	Truncated bool        `json:"truncated"`
}

// Blame attributes every line of a text file to the commit that last changed
// it: the working file when revision is empty, otherwise the file as of that
// commit, branch or tag.
func (s *Service) Blame(ctx context.Context, root, path, revision string) (*Blame, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	stream, err := client.Blame(ctx, &daemonpb.BlameRequest{Root: root, Path: path, Revision: revision})
	if err != nil {
		s.dropIfDisconnected(err)
		return nil, cleanError(err)
	}
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("blame ended without a result")
		}
		if err != nil {
			s.dropIfDisconnected(err)
			return nil, cleanError(err)
		}
		if f := ev.GetFailure(); f != nil {
			return nil, errors.New(f.GetMessage())
		}
		if res := ev.GetResult(); res != nil {
			return toBlame(res), nil
		}
	}
}

func toBlame(res *daemonpb.BlameResult) *Blame {
	out := &Blame{Path: res.GetPath(), Revision: res.GetRevision(), Truncated: res.GetTruncated(), Lines: make([]BlameLine, len(res.GetLines())), Commits: []Commit{}}
	for i, l := range res.GetLines() {
		out.Lines[i] = BlameLine{CommitID: l.GetCommitId(), Uncommitted: l.GetUncommitted()}
	}
	for _, c := range res.GetCommits() {
		out.Commits = append(out.Commits, Commit{ID: c.GetId(), Hash: c.GetHash(), Parents: []string{}, Author: c.GetAuthorName(), Email: c.GetAuthorEmail(), Message: c.GetMessage(), Date: c.GetCreatedAt()})
	}
	return out
}

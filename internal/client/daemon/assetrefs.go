package daemon

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/assetrefs"
	"github.com/nipalab/nipa/internal/client/assetscan"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// graphTTL bounds how long AssetRefs reuses a graph no change notice has
// invalidated, in case the watcher missed an event.
const graphTTL = 2 * time.Minute

// GraphBuilder builds the asset reference graph of a working copy.
type GraphBuilder interface {
	Graph(ctx context.Context, root string) (*assetrefs.Graph, error)
}

type graphBuilder struct {
	mu   sync.Mutex
	diff *usecase.Diff
}

// NewGraphBuilder serializes graph builds over a Diff usecase with its own
// localrepo handle, like NewPushChecker.
func NewGraphBuilder(diff *usecase.Diff) GraphBuilder {
	return &graphBuilder{diff: diff}
}

func (g *graphBuilder) Graph(ctx context.Context, root string) (*assetrefs.Graph, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return assetscan.Graph(ctx, g.diff, root, assetrefs.Options{})
}

// cachedGraph returns the reference graph, rebuilt only after a notice that
// can affect references or once graphTTL passed. Concurrent callers wait for
// one build.
func (r *repo) cachedGraph(ctx context.Context) (*assetrefs.Graph, error) {
	r.graphMu.Lock()
	defer r.graphMu.Unlock()
	gen := r.changes.refsGeneration()
	if r.graph != nil && r.graphGen == gen && time.Since(r.graphAt) < graphTTL {
		return r.graph, nil
	}
	r.graph = nil
	g, err := r.refGraph.Graph(ctx, r.root)
	if err != nil {
		return nil, err
	}
	r.graph, r.graphGen, r.graphAt = g, gen, time.Now()
	return g, nil
}

// AssetRefs reports what an engine asset uses and which files use it, from
// the cached reference graph of the clone. Files that cannot take part in a
// reference come back with asset false without a scan.
func (s *Server) AssetRefs(ctx context.Context, req *daemonpb.AssetRefsRequest) (*daemonpb.AssetRefsResponse, error) {
	p, err := serverDomain.NormalizePathPrefix(req.GetPath())
	if err == nil && p == "" {
		err = clientDomain.NewUserError("path is required")
	}
	if err != nil {
		return nil, toStatusError(clientDomain.NewUserError(err.Error()))
	}
	res := &daemonpb.AssetRefsResponse{Path: p}
	if !assetrefs.Relevant(p) {
		_, unref, err := s.repos.ref(req.GetRoot())
		if err != nil {
			return nil, toStatusError(err)
		}
		unref()
		return res, nil
	}
	err = s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		if rp.refGraph == nil {
			return status.Error(codes.FailedPrecondition, "asset references are not configured")
		}
		g, err := rp.cachedGraph(ctx)
		if err != nil {
			return err
		}
		fillRefs(res, g, p)
		return nil
	})
	if err != nil {
		return nil, toStatusError(err)
	}
	return res, nil
}

func fillRefs(res *daemonpb.AssetRefsResponse, g *assetrefs.Graph, p string) {
	res.Asset = true
	res.Scanned = int32(g.Scanned)
	res.Truncated = g.Truncated
	res.Partial = g.Partial()
	for _, r := range g.Uses(p) {
		ref := &daemonpb.AssetRef{Path: r.Path, Raw: r.Raw, Resolved: r.Resolved}
		if r.Unknown {
			res.Unknown = append(res.Unknown, ref)
		} else {
			res.Uses = append(res.Uses, ref)
		}
	}
	res.UsedBy = append(res.UsedBy, g.UsedBy(p)...)
}

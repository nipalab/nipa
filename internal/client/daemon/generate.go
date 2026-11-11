package daemon

import (
	"context"
	"errors"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/generate"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const notConfiguredMessage = "AI generation is not configured: set an endpoint and a model in Settings > AI assistance, or NIPA_LLM_ENDPOINT and NIPA_LLM_MODEL"

// GenerateRunner resolves the configured model on every call, so changed
// settings apply without a restart, and gathers the prompt input of a clone.
type GenerateRunner interface {
	Model() (generate.LLM, error)
	CommitInput(ctx context.Context, root string) (*aiprompt.CommitInput, error)
	MRInput(ctx context.Context, root, source, target string) (*aiprompt.MRInput, error)
}

type serialGenerator struct {
	mu  sync.Mutex
	gen GenerateRunner
}

// SerialGenerator serializes input gathering over usecases with their own
// localrepo handles, like SerialDiff.
func SerialGenerator(gen GenerateRunner) GenerateRunner {
	return &serialGenerator{gen: gen}
}

func (g *serialGenerator) Model() (generate.LLM, error) { return g.gen.Model() }

func (g *serialGenerator) CommitInput(ctx context.Context, root string) (*aiprompt.CommitInput, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen.CommitInput(ctx, root)
}

func (g *serialGenerator) MRInput(ctx context.Context, root, source, target string) (*aiprompt.MRInput, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gen.MRInput(ctx, root, source, target)
}

// Generate drafts a commit message for the staged changes or a merge request
// title and description. The input is gathered under the shared slot; the
// model is asked after the slot is released, so a slow endpoint never holds
// up a push.
func (s *Server) Generate(ctx context.Context, req *daemonpb.GenerateRequest) (*daemonpb.GenerateResponse, error) {
	kind := strings.TrimSpace(req.GetKind())
	if kind != "commit" && kind != "mr" {
		return nil, status.Error(codes.InvalidArgument, `kind must be "commit" or "mr"`)
	}
	var model generate.LLM
	var commitIn *aiprompt.CommitInput
	var mrIn *aiprompt.MRInput
	err := s.repos.withRepo(ctx, req.GetRoot(), false, func(rp *repo) error {
		if rp.generate == nil {
			return status.Error(codes.FailedPrecondition, "generation is not available in this daemon")
		}
		var err error
		if model, err = rp.generate.Model(); err != nil {
			return err
		}
		if kind == "commit" {
			commitIn, err = rp.generate.CommitInput(ctx, rp.root)
		} else {
			mrIn, err = rp.generate.MRInput(ctx, rp.root, req.GetSource(), req.GetTarget())
		}
		return err
	})
	if err != nil {
		return nil, generateError(err)
	}
	var res *generate.Result
	if kind == "commit" {
		res, err = generate.CommitMessage(ctx, model, *commitIn)
	} else {
		res, err = generate.MergeRequestDraft(ctx, model, *mrIn)
	}
	if err != nil {
		return nil, generateError(err)
	}
	return &daemonpb.GenerateResponse{Title: res.Title, Text: res.Text, Model: res.Model, Source: res.Source, Target: res.Target}, nil
}

func generateError(err error) error {
	if errors.Is(err, generate.ErrNotConfigured) {
		return status.Error(codes.FailedPrecondition, notConfiguredMessage)
	}
	return toStatusError(err)
}

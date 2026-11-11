// Package generate drafts commit messages and merge request descriptions
// with the configured OpenAI-compatible endpoint. It never asks for
// confirmation: the CLI and the daemon decide what happens to the draft.
package generate

import (
	"context"
	"errors"
	"strings"

	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/llm"
	"github.com/nipalab/nipa/internal/client/securestorage"
)

var ErrNotConfigured = errors.New("generation is not configured")

type LLM interface {
	Complete(ctx context.Context, p aiprompt.Prompt) (string, error)
	Model() string
}

type KeyStore interface {
	LoadLLMKey() (string, error)
}

type Sources interface {
	Commit(ctx context.Context, root string) (*aiprompt.CommitInput, error)
	MRDescription(ctx context.Context, root, source, target string) (*aiprompt.MRInput, error)
}

// Result is a draft without the Nipa-Generated trailer; callers add it where
// the text is used. Source and Target are the branches an MR draft describes.
type Result struct {
	Title  string
	Text   string
	Model  string
	Source string
	Target string
}

type notConfigured struct{ error }

func (e notConfigured) Unwrap() error        { return e.error }
func (e notConfigured) Is(target error) bool { return target == ErrNotConfigured }

func Configured(s config.LLMSettings) bool {
	return strings.TrimSpace(s.Endpoint) != "" && strings.TrimSpace(s.Model) != ""
}

// Settings resolves the endpoint, model and key: NIPA_LLM_API_KEY, then the
// variable named by apiKeyEnv, then the key saved in the OS keyring. The
// keyring is only read once an endpoint and model are configured, and an
// unreadable keyring counts as no key.
func Settings(keys KeyStore) (config.LLMSettings, error) {
	s, err := config.ResolveLLM()
	if err != nil {
		return s, err
	}
	if s.APIKey == "" && keys != nil && Configured(s) {
		if key, err := keys.LoadLLMKey(); err == nil {
			s.APIKey = key
		}
	}
	return s, nil
}

func NewLLM(keys KeyStore) (LLM, error) {
	s, err := Settings(keys)
	if err != nil {
		return nil, err
	}
	c, err := llm.New(s)
	if err != nil {
		if !Configured(s) {
			return nil, notConfigured{err}
		}
		return nil, err
	}
	return c, nil
}

// Default resolves the model from the config file, the environment and the
// OS keyring.
func Default() (LLM, error) {
	return NewLLM(securestorage.New())
}

type Generator struct {
	LLM     func() (LLM, error)
	Sources func(root string) (Sources, error)
}

func (g *Generator) Model() (LLM, error) {
	if g.LLM == nil {
		return Default()
	}
	return g.LLM()
}

func (g *Generator) sources(root string) (Sources, error) {
	if g.Sources == nil {
		return nil, domain.NewUserError("diff is not configured")
	}
	return g.Sources(root)
}

func (g *Generator) CommitInput(ctx context.Context, root string) (*aiprompt.CommitInput, error) {
	src, err := g.sources(root)
	if err != nil {
		return nil, err
	}
	return src.Commit(ctx, root)
}

func (g *Generator) MRInput(ctx context.Context, root, source, target string) (*aiprompt.MRInput, error) {
	src, err := g.sources(root)
	if err != nil {
		return nil, err
	}
	return src.MRDescription(ctx, root, source, target)
}

func (g *Generator) Commit(ctx context.Context, root string) (*Result, error) {
	model, err := g.Model()
	if err != nil {
		return nil, err
	}
	in, err := g.CommitInput(ctx, root)
	if err != nil {
		return nil, err
	}
	return CommitMessage(ctx, model, *in)
}

func (g *Generator) MergeRequest(ctx context.Context, root, source, target string) (*Result, error) {
	model, err := g.Model()
	if err != nil {
		return nil, err
	}
	in, err := g.MRInput(ctx, root, source, target)
	if err != nil {
		return nil, err
	}
	return MergeRequestDraft(ctx, model, *in)
}

func CommitMessage(ctx context.Context, model LLM, in aiprompt.CommitInput) (*Result, error) {
	text, err := Text(ctx, model, aiprompt.CommitMessage(in))
	if err != nil {
		return nil, err
	}
	return &Result{Text: text, Model: model.Model()}, nil
}

func MergeRequestDraft(ctx context.Context, model LLM, in aiprompt.MRInput) (*Result, error) {
	text, err := Text(ctx, model, aiprompt.MRDescription(in))
	if err != nil {
		return nil, err
	}
	title, body := aiprompt.SplitTitle(text)
	return &Result{Title: title, Text: body, Model: model.Model(), Source: in.Source, Target: in.Target}, nil
}

// Text asks the model for one answer, cleaned of reasoning blocks and fences.
func Text(ctx context.Context, model LLM, p aiprompt.Prompt) (string, error) {
	text, err := model.Complete(ctx, p)
	if err != nil {
		return "", err
	}
	text = aiprompt.CleanOutput(text)
	if text == "" {
		return "", domain.NewUserError("the LLM returned an empty answer")
	}
	return text, nil
}

// Ping sends one tiny request to check the endpoint, model and key.
func Ping(ctx context.Context, model LLM) error {
	_, err := model.Complete(ctx, aiprompt.Prompt{System: "You are a connectivity check.", User: "Reply with OK."})
	return err
}

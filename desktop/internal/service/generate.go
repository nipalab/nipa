package service

import (
	"context"
	"errors"
	"strings"
	"time"

	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/generate"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/llm"
	"github.com/nipalab/nipa/internal/client/securestorage"
)

// LLMKeyStore keeps the generation API key in the OS keyring.
type LLMKeyStore interface {
	SaveLLMKey(key string) error
	LoadLLMKey() (string, error)
	DeleteLLMKey() error
}

// LLMSettings never carries the key: KeySaved only says one is stored.
type LLMSettings struct {
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	KeySaved   bool   `json:"keySaved"`
	Configured bool   `json:"configured"`
}

type Draft struct {
	Title  string `json:"title"`
	Text   string `json:"text"`
	Model  string `json:"model"`
	Source string `json:"source"`
	Target string `json:"target"`
}

var llmTestTimeout = 20 * time.Second

func (s *Service) llmKeys() LLMKeyStore {
	if s.Keys != nil {
		return s.Keys
	}
	return securestorage.New()
}

// LLMSettings returns the endpoint and model saved in the shared nipa config
// and whether a key is saved; Configured also counts NIPA_LLM_* variables.
func (s *Service) LLMSettings() (LLMSettings, error) {
	cfg, err := clientconfig.Load()
	if err != nil {
		return LLMSettings{}, err
	}
	resolved, err := clientconfig.ResolveLLM()
	if err != nil {
		return LLMSettings{}, err
	}
	out := LLMSettings{Configured: generate.Configured(resolved)}
	if cfg.LLM != nil {
		out.Endpoint, out.Model = cfg.LLM.Endpoint, cfg.LLM.Model
	}
	key, err := s.llmKeys().LoadLLMKey()
	out.KeySaved = err == nil && key != ""
	return out, nil
}

// SetLLM saves the endpoint and model in the shared nipa config, which the
// daemon reads on every generation.
func (s *Service) SetLLM(endpoint, model string) (LLMSettings, error) {
	endpoint, model = strings.TrimSpace(endpoint), strings.TrimSpace(model)
	if (endpoint == "") != (model == "") {
		return LLMSettings{}, errors.New("set both the endpoint and the model, or clear both")
	}
	if err := clientconfig.SetLLM(endpoint, model); err != nil {
		return LLMSettings{}, err
	}
	return s.LLMSettings()
}

// SetLLMKey stores the API key in the OS keyring; it is never returned.
func (s *Service) SetLLMKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("enter a key, or use Clear to remove the saved one")
	}
	return s.llmKeys().SaveLLMKey(key)
}

func (s *Service) ClearLLMKey() error {
	return s.llmKeys().DeleteLLMKey()
}

// TestLLM sends one small request with the given endpoint and model; an
// empty key falls back to NIPA_LLM_API_KEY, then the saved key. Errors have
// the key redacted.
func (s *Service) TestLLM(ctx context.Context, endpoint, model, key string) error {
	settings := clientconfig.LLMSettings{Endpoint: endpoint, Model: model, APIKey: strings.TrimSpace(key)}
	if settings.APIKey == "" {
		resolved, err := generate.Settings(nil)
		if err != nil {
			return err
		}
		settings.APIKey = resolved.APIKey
	}
	if settings.APIKey == "" {
		if saved, err := s.llmKeys().LoadLLMKey(); err == nil {
			settings.APIKey = saved
		}
	}
	client, err := llm.New(settings, llm.WithTimeout(llmTestTimeout))
	if err != nil {
		return errors.New("enter an http(s) endpoint and a model")
	}
	return generate.Ping(ctx, client)
}

// GenerateCommitMessage drafts a message for the staged changes.
func (s *Service) GenerateCommitMessage(ctx context.Context, root string) (Draft, error) {
	return s.generate(ctx, &daemonpb.GenerateRequest{Root: root, Kind: "commit"})
}

// GenerateMergeRequest drafts a title and description for merging source
// into target.
func (s *Service) GenerateMergeRequest(ctx context.Context, root, source, target string) (Draft, error) {
	return s.generate(ctx, &daemonpb.GenerateRequest{Root: root, Kind: "mr", Source: source, Target: target})
}

func (s *Service) generate(ctx context.Context, req *daemonpb.GenerateRequest) (Draft, error) {
	client, err := s.client(ctx, req.GetRoot())
	if err != nil {
		return Draft{}, err
	}
	resp, err := client.Generate(ctx, req)
	if err != nil {
		s.dropIfDisconnected(err)
		return Draft{}, cleanError(err)
	}
	return Draft{Title: resp.GetTitle(), Text: resp.GetText(), Model: resp.GetModel(), Source: resp.GetSource(), Target: resp.GetTarget()}, nil
}

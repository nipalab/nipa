package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/desktop/internal/workspace"
	clientconfig "github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type SettingsStore interface {
	Settings() (workspace.Settings, error)
	SaveSettings(workspace.Settings) error
}

// IgnoreRules are the clone's shared .nipaignore and its local .nipa/ignore.
type IgnoreRules struct {
	Shared string `json:"shared"`
	Local  string `json:"local"`
}

const maxUploadWorkers = 64

var restartWait = 10 * time.Second

func (s *Service) prefs() workspace.Settings {
	if s.Prefs == nil {
		return workspace.DefaultSettings()
	}
	st, err := s.Prefs.Settings()
	if err != nil {
		return workspace.DefaultSettings()
	}
	return st
}

// Settings returns the app preferences.
func (s *Service) Settings() (workspace.Settings, error) {
	if s.Prefs == nil {
		return workspace.DefaultSettings(), nil
	}
	return s.Prefs.Settings()
}

// SaveSettings validates and stores the preferences, then applies them.
func (s *Service) SaveSettings(in workspace.Settings) (workspace.Settings, error) {
	if s.Prefs == nil {
		return workspace.Settings{}, errors.New("settings cannot be saved")
	}
	next, err := normalizeSettings(in)
	if err != nil {
		return workspace.Settings{}, err
	}
	old, err := s.Prefs.Settings()
	if err != nil {
		return workspace.Settings{}, err
	}
	changeLogin := next.StartAtLogin != old.StartAtLogin
	if changeLogin && s.Autostart == nil {
		return workspace.Settings{}, errors.New("starting at login is not available")
	}
	if err := s.Prefs.SaveSettings(next); err != nil {
		return workspace.Settings{}, err
	}
	if changeLogin {
		if err := s.Autostart(next.StartAtLogin); err != nil {
			_ = s.Prefs.SaveSettings(old)
			return workspace.Settings{}, fmt.Errorf("could not change starting at login: %w", err)
		}
	}
	if next.PollSeconds != old.PollSeconds {
		s.restartActivity()
	}
	if s.SettingsHook != nil {
		s.SettingsHook(next)
	}
	return next, nil
}

func normalizeSettings(in workspace.Settings) (workspace.Settings, error) {
	out := in
	switch {
	case out.PollSeconds == 0:
		out.PollSeconds = workspace.DefaultPollSeconds
	case out.PollSeconds < workspace.MinPollSeconds:
		out.PollSeconds = workspace.MinPollSeconds
	case out.PollSeconds > workspace.MaxPollSeconds:
		out.PollSeconds = workspace.MaxPollSeconds
	}
	out.Tools = []workspace.Tool{}
	for _, t := range in.Tools {
		ext := strings.ToLower(strings.TrimSpace(t.Extension))
		command := strings.TrimSpace(t.Command)
		if ext == "" && command == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		if len(ext) < 2 || strings.ContainsAny(ext[1:], `./\ *?`) {
			return workspace.Settings{}, fmt.Errorf("%q is not a file extension", t.Extension)
		}
		if command == "" {
			return workspace.Settings{}, fmt.Errorf("choose a program for %s files", ext)
		}
		if err := checkToolCommand(ext, command); err != nil {
			return workspace.Settings{}, err
		}
		if slices.ContainsFunc(out.Tools, func(o workspace.Tool) bool { return o.Extension == ext }) {
			return workspace.Settings{}, fmt.Errorf("%s files already have a program", ext)
		}
		out.Tools = append(out.Tools, workspace.Tool{Extension: ext, Command: command})
	}
	return out, nil
}

// ChooseProgram asks for an application to open files with; empty means
// cancelled.
func (s *Service) ChooseProgram() (string, error) {
	if s.ChooseProgramFunc == nil {
		return "", errors.New("file dialogs are not available")
	}
	return s.ChooseProgramFunc()
}

// UploadWorkers returns the pinned upload concurrency; 0 means automatic.
func (s *Service) UploadWorkers() (int, error) {
	cfg, err := clientconfig.Load()
	if err != nil {
		return 0, err
	}
	return cfg.UploadWorkers, nil
}

// SetUploadWorkers pins the upload concurrency (0 = automatic) in the shared
// nipa config, used by the CLI and by clones the daemon opens next.
func (s *Service) SetUploadWorkers(workers int) error {
	if workers < 0 || workers > maxUploadWorkers {
		return fmt.Errorf("choose between 1 and %d uploads at once, or automatic", maxUploadWorkers)
	}
	return clientconfig.SetUploadWorkers(workers)
}

// RestartDaemon stops the background service and starts a fresh one, so
// changed settings apply to every clone.
func (s *Service) RestartDaemon(ctx context.Context) (DaemonInfo, error) {
	s.mu.Lock()
	sess := s.active
	s.mu.Unlock()
	if sess != nil {
		client := sess.Client()
		if _, err := client.Shutdown(ctx, &daemonpb.ShutdownRequest{}); err != nil && !isConnectionError(err) {
			return DaemonInfo{}, cleanError(err)
		}
		waitGone(ctx, client)
		s.mu.Lock()
		if s.active == sess {
			_ = s.resetLocked()
		}
		s.mu.Unlock()
	}
	return s.Connect(ctx)
}

func waitGone(ctx context.Context, client daemonpb.NipaDaemonClient) {
	ctx, cancel := context.WithTimeout(ctx, restartWait)
	defer cancel()
	for {
		if _, err := client.Ping(ctx, &daemonpb.PingRequest{}); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// IgnoreRules reads the clone's ignore files; missing files are empty.
func (s *Service) IgnoreRules(root string) (IgnoreRules, error) {
	if err := s.knownRoot(root); err != nil {
		return IgnoreRules{}, err
	}
	shared, err := readOptional(filepath.Join(root, ".nipaignore"))
	if err != nil {
		return IgnoreRules{}, err
	}
	local, err := readOptional(filepath.Join(root, ".nipa", "ignore"))
	if err != nil {
		return IgnoreRules{}, err
	}
	return IgnoreRules{Shared: shared, Local: local}, nil
}

// SaveIgnoreRules writes both ignore files. An empty rule set leaves a
// missing file missing; .nipaignore is versioned, so saving it shows up as a
// change to push.
func (s *Service) SaveIgnoreRules(root string, rules IgnoreRules) error {
	if err := s.knownRoot(root); err != nil {
		return err
	}
	if err := writeOptional(filepath.Join(root, ".nipaignore"), rules.Shared); err != nil {
		return err
	}
	if err := writeOptional(filepath.Join(root, ".nipa", "ignore"), rules.Local); err != nil {
		return err
	}
	s.emit(EventRepoChanged, RepoChanged{Root: root})
	return nil
}

func (s *Service) knownRoot(root string) error {
	roots, err := s.store.Repos()
	if err != nil {
		return err
	}
	if !slices.Contains(roots, root) {
		return errors.New("open the clone in Nipa first")
	}
	return nil
}

func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

func writeOptional(path, content string) error {
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content == "" {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func toolFor(tools []workspace.Tool, path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	for _, t := range tools {
		if t.Extension == ext {
			return t.Command
		}
	}
	return ""
}

func (s *Service) launch(command, path string, line int) error {
	args := toolArgs(runtime.GOOS, command, path, line)
	if s.LaunchFunc != nil {
		return s.LaunchFunc(args)
	}
	cmd := exec.Command(args[0], args[1:]...) // NOSONAR user-configured tool
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

const (
	fileToken = "{file}"
	lineToken = "{line}"
)

// toolArgs expands a tool command. A command naming {file} is a template
// whose words may also use {line} (1 when unknown); any other command is a
// program started with the file.
func toolArgs(goos, command, path string, line int) []string {
	if strings.Contains(command, fileToken) {
		if words, err := splitCommand(command); err == nil && len(words) > 0 {
			r := strings.NewReplacer(fileToken, path, lineToken, strconv.Itoa(max(line, 1)))
			for i, w := range words {
				words[i] = r.Replace(w)
			}
			return words
		}
	}
	if goos == "darwin" && strings.HasSuffix(strings.ToLower(strings.TrimRight(command, "/")), ".app") {
		return []string{"open", "-a", command, path}
	}
	return []string{command, path}
}

// splitCommand splits a command line on spaces outside single or double
// quotes. Backslashes are kept as they are, so Windows paths need no escaping.
func splitCommand(command string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	inWord := false
	for _, r := range command {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unbalanced quotes")
	}
	if inWord {
		words = append(words, word.String())
	}
	return words, nil
}

func checkToolCommand(ext, command string) error {
	if !strings.Contains(command, fileToken) {
		if strings.Contains(command, lineToken) {
			return fmt.Errorf("the program for %s files uses {line}; add {file} where the file goes", ext)
		}
		return nil
	}
	words, err := splitCommand(command)
	if err != nil || len(words) == 0 || strings.Contains(words[0], fileToken) {
		return fmt.Errorf("the program for %s files is not a valid command: start with the program, then its arguments, and quote paths with spaces", ext)
	}
	return nil
}

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/desktop/internal/workspace"
)

func TestService_SettingsDefaultsWithoutStore(t *testing.T) {
	h := newHarness()
	st, err := h.svc.Settings()
	require.NoError(t, err)
	require.Equal(t, workspace.DefaultSettings(), st)
	_, err = h.svc.SaveSettings(st)
	require.Error(t, err)

	h.svc.Prefs = &memPrefs{err: errors.New("broken")}
	require.Equal(t, workspace.DefaultSettings(), h.svc.prefs())
	_, err = h.svc.Settings()
	require.Error(t, err)
	_, err = h.svc.SaveSettings(st)
	require.Error(t, err)
}

func TestService_SaveSettingsAppliesChanges(t *testing.T) {
	h := newHarness()
	prefs := newPrefs()
	h.svc.Prefs = prefs
	var autostart []bool
	h.svc.Autostart = func(on bool) error { autostart = append(autostart, on); return nil }
	var hooked []workspace.Settings
	h.svc.SettingsHook = func(s workspace.Settings) { hooked = append(hooked, s) }

	in := workspace.DefaultSettings()
	in.StartAtLogin = true
	in.PollSeconds = 5
	in.Tools = []workspace.Tool{{Extension: " BLEND ", Command: " /usr/bin/blender "}, {}}
	got, err := h.svc.SaveSettings(in)
	require.NoError(t, err)
	require.Equal(t, workspace.MinPollSeconds, got.PollSeconds)
	require.Equal(t, []workspace.Tool{{Extension: ".blend", Command: "/usr/bin/blender"}}, got.Tools)
	require.Equal(t, []bool{true}, autostart)
	require.Equal(t, []workspace.Settings{got}, hooked)
	stored, err := h.svc.Settings()
	require.NoError(t, err)
	require.Equal(t, got, stored)

	_, err = h.svc.SaveSettings(got)
	require.NoError(t, err)
	require.Equal(t, []bool{true}, autostart, "unchanged login item is left alone")

	h.svc.Autostart = func(bool) error { return errors.New("denied") }
	got.StartAtLogin = false
	_, err = h.svc.SaveSettings(got)
	require.ErrorContains(t, err, "denied")
	kept, err := h.svc.Settings()
	require.NoError(t, err)
	require.True(t, kept.StartAtLogin, "failed login item change is rolled back")
	h.svc.Autostart = nil
	_, err = h.svc.SaveSettings(got)
	require.ErrorContains(t, err, "not available")

	got.StartAtLogin = true
	prefs.saveErr = errors.New("disk full")
	_, err = h.svc.SaveSettings(got)
	require.ErrorContains(t, err, "disk full")
}

func TestNormalizeSettings(t *testing.T) {
	st, err := normalizeSettings(workspace.Settings{})
	require.NoError(t, err)
	require.Equal(t, workspace.DefaultPollSeconds, st.PollSeconds)
	require.NotNil(t, st.Tools)
	st, err = normalizeSettings(workspace.Settings{PollSeconds: 99999})
	require.NoError(t, err)
	require.Equal(t, workspace.MaxPollSeconds, st.PollSeconds)
	st, err = normalizeSettings(workspace.Settings{PollSeconds: 300})
	require.NoError(t, err)
	require.Equal(t, 300, st.PollSeconds)

	for _, tools := range [][]workspace.Tool{
		{{Extension: ".", Command: "x"}},
		{{Extension: "tar.gz", Command: "x"}},
		{{Extension: "a b", Command: "x"}},
		{{Extension: ".psd"}},
		{{Extension: ".psd", Command: "a"}, {Extension: "PSD", Command: "b"}},
		{{Extension: ".cs", Command: "code --goto x:{line}"}},
		{{Extension: ".cs", Command: `"code --goto {file}`}},
		{{Extension: ".cs", Command: "{file} --goto"}},
	} {
		_, err := normalizeSettings(workspace.Settings{Tools: tools})
		require.Error(t, err, tools)
	}
	st, err = normalizeSettings(workspace.Settings{Tools: []workspace.Tool{{Extension: "cs", Command: `"C:\Program Files\code.exe" -g {file}:{line}`}}})
	require.NoError(t, err)
	require.Equal(t, ".cs", st.Tools[0].Extension)
}

func TestService_UploadWorkers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := newHarness()
	n, err := h.svc.UploadWorkers()
	require.NoError(t, err)
	require.Zero(t, n)
	require.NoError(t, h.svc.SetUploadWorkers(12))
	n, err = h.svc.UploadWorkers()
	require.NoError(t, err)
	require.Equal(t, 12, n)
	require.Error(t, h.svc.SetUploadWorkers(65))
	require.Error(t, h.svc.SetUploadWorkers(-1))

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nipa"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nipa", "config.json"), []byte("{"), 0o600))
	_, err = h.svc.UploadWorkers()
	require.Error(t, err)
}

func TestService_RestartDaemon(t *testing.T) {
	saved := restartWait
	restartWait = 2e9
	t.Cleanup(func() { restartWait = saved })

	h := newHarness()
	info, err := h.svc.RestartDaemon(ctx)
	require.NoError(t, err)
	require.Equal(t, "v1", info.Version)
	require.Zero(t, h.daemon.shutdowns, "nothing to stop without a session")

	first := h.session
	_, err = h.svc.RestartDaemon(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, h.daemon.shutdowns)
	require.Equal(t, 3, h.daemon.pings, "it waits until the old daemon stops answering")
	require.Equal(t, 1, first.closed)
	require.Equal(t, 2, h.dials)

	h.daemon.stopErr = status.Error(codes.Unavailable, "already gone")
	h.daemon.pings = 2
	_, err = h.svc.RestartDaemon(ctx)
	require.NoError(t, err)

	h.daemon.stopErr = status.Error(codes.PermissionDenied, "no")
	_, err = h.svc.RestartDaemon(ctx)
	require.EqualError(t, err, "no")
}

func TestWaitGoneTimesOut(t *testing.T) {
	saved := restartWait
	restartWait = 150e6
	t.Cleanup(func() { restartWait = saved })
	d := &fakeDaemon{pings: -1000}
	waitGone(ctx, d)
	require.Greater(t, d.pings, -1000)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	waitGone(cancelled, &fakeDaemon{pings: -1000})
}

func TestService_IgnoreRules(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".nipa"), 0o755))
	h := newHarness()
	h.store.repos = []string{root}
	var changed []any
	h.svc.Events = func(name string, data any) {
		if name == EventRepoChanged {
			changed = append(changed, data)
		}
	}

	rules, err := h.svc.IgnoreRules(root)
	require.NoError(t, err)
	require.Equal(t, IgnoreRules{}, rules)

	require.NoError(t, h.svc.SaveIgnoreRules(root, IgnoreRules{Shared: "*.tmp", Local: ""}))
	_, err = os.Stat(filepath.Join(root, ".nipa", "ignore"))
	require.True(t, os.IsNotExist(err), "an empty local file is not created")
	rules, err = h.svc.IgnoreRules(root)
	require.NoError(t, err)
	require.Equal(t, IgnoreRules{Shared: "*.tmp\n"}, rules)
	require.Equal(t, []any{RepoChanged{Root: root}}, changed)

	require.NoError(t, h.svc.SaveIgnoreRules(root, IgnoreRules{Shared: "", Local: "build/\n"}))
	rules, err = h.svc.IgnoreRules(root)
	require.NoError(t, err)
	require.Equal(t, IgnoreRules{Local: "build/\n"}, rules, "clearing an existing file empties it")

	_, err = h.svc.IgnoreRules("/elsewhere")
	require.ErrorContains(t, err, "open the clone")
	require.ErrorContains(t, h.svc.SaveIgnoreRules("/elsewhere", IgnoreRules{}), "open the clone")
	h.store.err = errors.New("disk")
	_, err = h.svc.IgnoreRules(root)
	require.Error(t, err)
	h.store.err = nil

	require.NoError(t, os.Remove(filepath.Join(root, ".nipaignore")))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".nipaignore"), 0o755))
	_, err = h.svc.IgnoreRules(root)
	require.Error(t, err)
	require.Error(t, h.svc.SaveIgnoreRules(root, IgnoreRules{Shared: "x"}))
	require.NoError(t, os.Remove(filepath.Join(root, ".nipaignore")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".nipa")))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".nipa"), nil, 0o600))
	_, err = h.svc.IgnoreRules(root)
	require.Error(t, err)
	require.Error(t, h.svc.SaveIgnoreRules(root, IgnoreRules{Local: "x"}))
}

func TestService_OpenFileWithTools(t *testing.T) {
	root := t.TempDir()
	h := newHarness()
	prefs := newPrefs()
	prefs.st.Tools = []workspace.Tool{{Extension: ".blend", Command: "blender"}}
	h.svc.Prefs = prefs
	var launched [][]string
	h.svc.LaunchFunc = func(args []string) error {
		launched = append(launched, args)
		return nil
	}
	require.NoError(t, h.svc.OpenFile(ctx, root, "art/Hero.BLEND"))
	require.Equal(t, [][]string{{"blender", filepath.Join(root, "art", "Hero.BLEND")}}, launched)

	prefs.st.Tools = []workspace.Tool{{Extension: ".cs", Command: `"/opt/My Editor/code" --goto {file}:{line}`}}
	launched = nil
	require.NoError(t, h.svc.OpenFileAt(ctx, root, "src/Player.cs", 42))
	require.NoError(t, h.svc.OpenFile(ctx, root, "src/Player.cs"))
	full := filepath.Join(root, "src", "Player.cs")
	require.Equal(t, [][]string{{"/opt/My Editor/code", "--goto", full + ":42"}, {"/opt/My Editor/code", "--goto", full + ":1"}}, launched)
	require.ErrorContains(t, h.svc.OpenFileAt(ctx, root, "../x.cs", 3), "outside")
	prefs.st.Tools = []workspace.Tool{{Extension: ".blend", Command: "blender"}}
	require.ErrorContains(t, h.svc.OpenFile(ctx, root, "notes.txt"), "not available")

	h.svc.LaunchFunc = nil
	require.Error(t, h.svc.OpenFile(ctx, root, "missing-program.blend"), "a missing program fails to start")
	prefs.st.Tools = []workspace.Tool{{Extension: ".txt", Command: "true"}}
	require.NoError(t, h.svc.OpenFile(ctx, root, "notes.txt"))
}

func TestToolArgsAndChooseProgram(t *testing.T) {
	require.Equal(t, []string{"open", "-a", "/Applications/Blender.app/", "/f.blend"}, toolArgs("darwin", "/Applications/Blender.app/", "/f.blend", 0))
	require.Equal(t, []string{"/usr/bin/blender", "/f.blend"}, toolArgs("linux", "/usr/bin/blender", "/f.blend", 3))
	require.Equal(t, []string{"/Applications/x", "/f"}, toolArgs("darwin", "/Applications/x", "/f", 0))
	require.Equal(t, []string{`C:\Program Files\Code.exe`, "-g", `C:\w\a.cs:7`}, toolArgs("windows", `"C:\Program Files\Code.exe" -g {file}:{line}`, `C:\w\a.cs`, 7))
	require.Equal(t, []string{"subl", "/a {line}.txt:1"}, toolArgs("linux", "subl\t'{file}:{line}'", "/a {line}.txt", -1))
	require.Equal(t, []string{"broken \"{file}", "/f"}, toolArgs("linux", "broken \"{file}", "/f", 2))

	h := newHarness()
	_, err := h.svc.ChooseProgram()
	require.Error(t, err)
	h.svc.ChooseProgramFunc = func() (string, error) { return "/usr/bin/krita", nil }
	got, err := h.svc.ChooseProgram()
	require.NoError(t, err)
	require.Equal(t, "/usr/bin/krita", got)
}

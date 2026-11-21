//go:build windows

package usecase

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// mklinkJunction makes link a junction to target with cmd's mklink /J, which
// needs no privilege.
func mklinkJunction(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	require.NoError(t, err, "mklink /J: %s", out)
}

// dirSymlinkOrSkip makes link a directory symbolic link to target, skipping
// the test when the account may not create symlinks.
func dirSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks need the create-symlink privilege or developer mode: %v", err)
	}
}

func windowsPayload() (materializedFile, func(serverDomain.Hash) (io.ReadCloser, error)) {
	data := []byte("payload")
	open := func(serverDomain.Hash) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	return materializedFile{Mode: 2, SizeBytes: int64(len(data)), ChunkHashes: []serverDomain.Hash{chunker.Sum(data)}, ChunkSizes: []int64{int64(len(data))}}, open
}

func TestWriteGuard_WindowsLinks(t *testing.T) {
	for kind, link := range map[string]func(t *testing.T, target, link string){
		"junction or reparse point": mklinkJunction,
		"symbolic link":             dirSymlinkOrSkip,
	} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(outside, "hero.png"), []byte("keep"), 0o644))
			link(t, outside, filepath.Join(root, "art"))
			info, err := os.Lstat(filepath.Join(root, "art"))
			require.NoError(t, err)
			require.Equal(t, kind, domain.LinkKind(info.Mode()), "mode %v", info.Mode())

			file, open := windowsPayload()
			file.Path = "art/new.bin"
			refused := func(err error, verb string) {
				t.Helper()
				requireCode(t, err, 400)
				require.ErrorContains(t, err, `refusing to `+verb+` "art/`)
				require.ErrorContains(t, err, `"art" is a `+kind)
			}
			refused(materializeFile(root, file, 0, open), "write")
			_, err = createSizedTemp(testWorkTree(t, root), root, file)
			refused(err, "write")
			refused(removeWorkingFile(root, "art/hero.png"), "remove")
			_, err = guardedRemove(root, domain.SnapshotFile{Path: "art/hero.png"})
			refused(err, "remove")
			c := newConflicts(t, &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}, root)
			refused(c.Resolve(context.Background(), "art/hero.png", Resolution{Content: strings.NewReader("theirs")}), "write")
			refused(c.Resolve(context.Background(), "art/hero.png", Resolution{Delete: true}), "remove")
			requireUntouched(t, outside, "hero.png")

			// A real folder next to it is written and removed as usual.
			file.Path = "real/new.bin"
			require.NoError(t, materializeFile(root, file, 0, open))
			require.Equal(t, "payload", string(readRepoFile(t, root, "real/new.bin")))
			require.NoError(t, removeWorkingFile(root, "real/new.bin"))
			require.NoDirExists(t, filepath.Join(root, "real"))
		})
	}
}

func TestWorkTree_RefusesAFolderSwappedForAJunction(t *testing.T) {
	file, open := windowsPayload()
	for name, run := range map[string]func(root string) (string, error){
		"materialize": func(root string) (string, error) {
			f := file
			f.Path = "art/new.bin"
			return "write", materializeFile(root, f, 0, open)
		},
		"direct": func(root string) (string, error) {
			f := file
			f.Path = "art/new.bin"
			_, err := newDirectPlan(root, []materializeJob{{file: f}})
			return "write", err
		},
		"remove": func(root string) (string, error) {
			return "remove", removeWorkingFile(root, "art/hero.png")
		},
		"resolve": func(root string) (string, error) {
			c := newConflicts(t, &stubLocalRepo{mergeState: pendingMerge(), snapshot: &domain.Snapshot{}}, root)
			return "write", c.Resolve(context.Background(), "art/hero.png", Resolution{Content: strings.NewReader("theirs")})
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeRepoFile(t, root, "art/hero.png", "keep")
			t.Cleanup(guardWrites(root))
			require.NoError(t, checkTargets(root, "write", []string{"art/hero.png", "art/new.bin"}))
			require.NoError(t, os.Rename(filepath.Join(root, "art", "hero.png"), filepath.Join(outside, "hero.png")))
			require.NoError(t, os.Remove(filepath.Join(root, "art")))
			mklinkJunction(t, outside, filepath.Join(root, "art"))

			verb, err := run(root)
			requireEscapeRefusal(t, err, verb)
			requireUntouched(t, outside, "hero.png")
		})
	}
}

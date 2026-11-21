package usecase

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func setMaxOpenTemps(t *testing.T, n int) {
	t.Helper()
	old := maxOpenTemps
	maxOpenTemps = n
	t.Cleanup(func() { maxOpenTemps = old })
}

// handlePlan builds a plan of n two-chunk files with distinct content.
func handlePlan(t *testing.T, n int) (*directPlan, string, map[serverDomain.Hash][]byte) {
	t.Helper()
	root := t.TempDir()
	data := map[serverDomain.Hash][]byte{}
	tree := &serverDomain.TreeNode{Name: "root"}
	for i := range n {
		tree.FileChildren = append(tree.FileChildren, rawManifestFile(fmt.Sprintf("f%02d.bin", i), data, fmt.Sprintf("head-%02d", i), fmt.Sprintf("tail-%02d", i)))
	}
	var jobs []materializeJob
	for _, f := range flattenTree(tree, "") {
		jobs = append(jobs, materializeJob{file: f})
	}
	plan, err := newDirectPlan(root, jobs)
	require.NoError(t, err)
	t.Cleanup(plan.cleanup)
	return plan, root, data
}

func openTemps(plan *directPlan) int {
	plan.handles.mu.Lock()
	defer plan.handles.mu.Unlock()
	return len(plan.handles.open)
}

func TestDirectPlan_KeepsABoundedNumberOfTempsOpen(t *testing.T) {
	setMaxOpenTemps(t, 3)
	plan, root, _ := handlePlan(t, 12)

	for i := range 12 {
		require.NoError(t, plan.write(chunker.Sum(fmt.Appendf(nil, "head-%02d", i)), fmt.Appendf(nil, "head-%02d", i)))
		require.LessOrEqual(t, openTemps(plan), 3, "an idle handle is closed to make room")
	}
	require.Equal(t, 3, openTemps(plan), "handles stay open between the chunks of a file")
	for i := range 12 {
		_, err := plan.receive(chunker.Sum(fmt.Appendf(nil, "tail-%02d", i)), bytes.NewReader(fmt.Appendf(nil, "tail-%02d", i)))
		require.NoError(t, err)
		require.NoError(t, plan.settle(chunker.Sum(fmt.Appendf(nil, "tail-%02d", i))))
	}
	require.Zero(t, openTemps(plan), "a file's handle is closed with its last chunk")

	require.NoError(t, plan.finish(context.Background()))
	for i := range 12 {
		require.Equal(t, fmt.Sprintf("head-%02dtail-%02d", i, i), string(readRepoFile(t, root, fmt.Sprintf("f%02d.bin", i))))
	}
	requireNoTempFiles(t, root)
}

func TestDirectPlan_ConcurrentWritesShareHandles(t *testing.T) {
	setMaxOpenTemps(t, 2)
	plan, root, data := handlePlan(t, 16)

	var wg sync.WaitGroup
	errs := make(chan error, len(data))
	for h, content := range data {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- plan.write(h, content)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Zero(t, openTemps(plan))
	require.NoError(t, plan.finish(context.Background()))
	for i := range 16 {
		require.Equal(t, fmt.Sprintf("head-%02dtail-%02d", i, i), string(readRepoFile(t, root, fmt.Sprintf("f%02d.bin", i))))
	}
}

func TestDirectPlan_CleanupClosesOpenTemps(t *testing.T) {
	plan, root, _ := handlePlan(t, 2)
	require.NoError(t, plan.write(chunker.Sum([]byte("head-00")), []byte("head-00")))
	require.Equal(t, 1, openTemps(plan))

	plan.cleanup()
	require.Zero(t, openTemps(plan))
	requireNoTempFiles(t, root)
}

func TestTempHandles_CloseWaitsForTheWriter(t *testing.T) {
	plan, _, _ := handlePlan(t, 1)
	df := plan.files[0]
	f, err := plan.handles.acquire(df)
	require.NoError(t, err)
	again, err := plan.handles.acquire(df)
	require.NoError(t, err)
	require.Same(t, f, again)

	require.NoError(t, plan.handles.close(df))
	_, err = f.WriteAt([]byte("x"), 0)
	require.NoError(t, err, "a handle in use stays open")
	plan.handles.release(df)
	require.Equal(t, 1, openTemps(plan))
	plan.handles.release(df)
	require.Zero(t, openTemps(plan), "the last writer closes a finished file")
	plan.handles.release(df)
}

func TestTempHandles_KeepsBusyHandlesOverTheBound(t *testing.T) {
	setMaxOpenTemps(t, 1)
	plan, _, _ := handlePlan(t, 3)
	for _, df := range plan.files {
		_, err := plan.handles.acquire(df)
		require.NoError(t, err)
	}
	require.Equal(t, 3, openTemps(plan), "a handle being written is never closed")
	for _, df := range plan.files {
		plan.handles.release(df)
	}
	_, err := plan.handles.acquire(plan.files[0])
	require.NoError(t, err)
	plan.handles.release(plan.files[0])
	require.Equal(t, 3, openTemps(plan), "an open handle is reused without closing others")
	plan.handles.closeAll()
	require.Zero(t, openTemps(plan))
}

func TestDirectPlan_ReportsACloseErrorOfAnEvictedHandle(t *testing.T) {
	setMaxOpenTemps(t, 1)
	plan, _, _ := handlePlan(t, 2)
	first := chunker.Sum([]byte("head-00"))
	require.NoError(t, plan.write(first, []byte("head-00")))
	plan.handles.mu.Lock()
	require.NoError(t, plan.handles.open[plan.files[0]].file.Close())
	plan.handles.mu.Unlock()

	require.NoError(t, plan.write(chunker.Sum([]byte("head-01")), []byte("head-01")), "evicts the first file's handle")
	err := plan.write(chunker.Sum([]byte("tail-00")), []byte("tail-00"))
	var failed *materializeError
	require.ErrorAs(t, err, &failed, "the file cannot complete after a failed close")
	require.Equal(t, "f00.bin", failed.path)
	require.False(t, plan.files[0].done.Load())
}

func TestWriteTargets_Errors(t *testing.T) {
	plan, _, _ := handlePlan(t, 2)
	head := chunker.Sum([]byte("head-00"))
	targets := plan.targets[head]
	df := targets[0].file

	require.ErrorContains(t, writeTargets(df, targets, []byte("short")), "content length mismatch")
	require.ErrorContains(t, writeTargets(df, []directTarget{{file: df, offset: -1, size: 7}}, []byte("head-00")), "negative offset")
	require.NoError(t, writeTargets(df, targets, []byte("head-00")))

	missing := plan.files[1]
	require.NoError(t, os.Remove(filepath.Join(plan.root, missing.tmp)))
	err := writeTargets(missing, plan.targets[chunker.Sum([]byte("head-01"))], []byte("head-01"))
	require.ErrorIs(t, err, os.ErrNotExist, "a temp file that disappeared cannot be reopened")

	var failed *materializeError
	require.ErrorAs(t, plan.write(chunker.Sum([]byte("head-01")), []byte("head-01")), &failed)
	require.Equal(t, "f01.bin", failed.path)
}

func TestRestoreTargets(t *testing.T) {
	tracked := map[string]domain.SnapshotFile{}
	for _, p := range []string{"a.txt", "a/b.txt", "a/c/d.txt", "a-b/e.txt", "ab/f.txt", "z.txt"} {
		tracked[p] = domain.SnapshotFile{Path: p}
	}
	all := []string{"a-b/e.txt", "a.txt", "a/b.txt", "a/c/d.txt", "ab/f.txt", "z.txt"}

	for name, tc := range map[string]struct {
		targets []string
		want    []string
	}{
		"exact file":                {[]string{"z.txt"}, []string{"z.txt"}},
		"directory":                 {[]string{"a"}, []string{"a/b.txt", "a/c/d.txt"}},
		"directory with a slash":    {[]string{"a/"}, []string{"a/b.txt", "a/c/d.txt"}},
		"nested directory":          {[]string{"./a/c"}, []string{"a/c/d.txt"}},
		"prefix is not a directory": {[]string{"ab"}, []string{"ab/f.txt"}},
		"overlapping targets":       {[]string{"a/c", "z.txt", "a", "a/b.txt"}, []string{"a/b.txt", "a/c/d.txt", "z.txt"}},
		"dot is everything":         {[]string{"."}, all},
		"everything plus a file":    {[]string{"a", ".", "z.txt"}, all},
		"no targets":                {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := restoreTargets(tc.targets, tracked)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	for name, tc := range map[string]struct {
		targets []string
		want    string
	}{
		"empty target":          {[]string{""}, "a path is required"},
		"metadata":              {[]string{".nipa/config"}, "cannot operate on path inside"},
		"outside":               {[]string{"../x"}, "outside the repository"},
		"unknown":               {[]string{"a/b"}, `path "a/b" is not tracked`},
		"sibling prefix":        {[]string{"a-"}, `path "a-" is not tracked`},
		"unknown after the dot": {[]string{".", "nope"}, `path "nope" is not tracked`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := restoreTargets(tc.targets, tracked)
			require.ErrorContains(t, err, tc.want)
		})
	}

	_, err := restoreTargets([]string{"."}, map[string]domain.SnapshotFile{})
	require.ErrorContains(t, err, `path "." is not tracked`, "nothing tracked, nothing to restore")
}

package service

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"google.golang.org/grpc"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type FileEntry struct {
	Name            string `json:"name"`
	Path            string `json:"path"`
	IsDir           bool   `json:"isDir"`
	Size            int64  `json:"size"`
	Binary          bool   `json:"binary"`
	Kind            string `json:"kind"`
	Status          string `json:"status"` // empty, modified, added, deleted, missing, untracked, conflict
	Staged          bool   `json:"staged"`
	ChildrenChanged bool   `json:"childrenChanged"`
	Tracked         bool   `json:"tracked"`
	Hash            string `json:"hash"`
	Mtime           int64  `json:"mtime"` // unix nanoseconds, set for entries with a status
}

type FileListing struct {
	Dir     string      `json:"dir"`
	Entries []FileEntry `json:"entries"`
}

// FileDetails is one file's metadata plus the last commit that changed it.
// LastCommit is nil when the file has no history or the server could not be
// reached; LastCommitError then says why.
type FileDetails struct {
	Entry           FileEntry `json:"entry"`
	LastCommit      *Commit   `json:"lastCommit"`
	LastCommitError string    `json:"lastCommitError,omitempty"`
}

// Files lists one directory of the clone with each entry's status.
func (s *Service) Files(ctx context.Context, root, dir string) (*FileListing, error) {
	var res *daemonpb.ListFilesResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: dir})
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &FileListing{Dir: res.GetDir(), Entries: make([]FileEntry, 0, len(res.GetEntries()))}
	for _, e := range res.GetEntries() {
		out.Entries = append(out.Entries, toFileEntry(e))
	}
	return out, nil
}

// FileInfo returns one file's metadata and its last change.
func (s *Service) FileInfo(ctx context.Context, root, p string) (*FileDetails, error) {
	p = strings.Trim(p, "/")
	dir, name := path.Split(p)
	if name == "" {
		return nil, errors.New("choose a file")
	}
	var res *daemonpb.ListFilesResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ListFiles(ctx, &daemonpb.ListFilesRequest{Root: root, Dir: strings.TrimSuffix(dir, "/"), Name: name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(res.GetEntries()) == 0 {
		return nil, errors.New(p + " does not exist")
	}
	out := &FileDetails{Entry: toFileEntry(res.GetEntries()[0])}
	page, err := s.History(ctx, root, HistoryQuery{Path: p, Limit: 1})
	switch {
	case err != nil:
		out.LastCommitError = err.Error()
	case len(page.Commits) > 0:
		out.LastCommit = &page.Commits[0]
	}
	return out, nil
}

// FileView returns the working file as a one-sided diff for the viewers: text
// as plain lines, other kinds with their metadata. Nil means the file is not
// on disk.
func (s *Service) FileView(ctx context.Context, root, p string) (*FileDiff, error) {
	client, err := s.client(ctx, root)
	if err != nil {
		return nil, err
	}
	view, err := s.untrackedDiff(ctx, client, root, p)
	if err != nil || view == nil {
		return nil, err
	}
	view.Status = ""
	view.Untracked = false
	view.Additions = 0
	for i := range view.Hunks {
		for j := range view.Hunks[i].Lines {
			view.Hunks[i].Lines[j].Kind = "context"
		}
	}
	return view, nil
}

// RestoreRevision writes the file as it was in commitID over the working
// file; the file then shows as modified.
func (s *Service) RestoreRevision(ctx context.Context, root, p, commitID string) (OpOutcome, error) {
	if commitID == "" {
		return OpOutcome{}, errors.New("choose a version")
	}
	return s.runOp(ctx, root, "restore", func(c daemonpb.NipaDaemonClient) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
		return c.RestoreRevision(ctx, &daemonpb.RestoreRevisionRequest{Root: root, Path: p, CommitId: commitID})
	})
}

// RevealFile shows a clone file or folder in the system file manager; an
// empty path reveals the clone itself.
func (s *Service) RevealFile(_ context.Context, root, p string) error {
	full := filepath.Clean(root)
	if p = strings.Trim(p, "/"); p != "" {
		full = filepath.Join(root, filepath.FromSlash(p))
		if _, ok := insideRoot(root, full); !ok {
			return errors.New("the file is outside this clone")
		}
	}
	if s.RevealFunc != nil {
		return s.RevealFunc(full)
	}
	return runReveal(revealCommands(runtime.GOOS, full))
}

// revealCommands lists what to try in order: on Linux the file manager's
// D-Bus ShowItems call selects the file, opening the folder is the fallback.
func revealCommands(goos, full string) []*exec.Cmd {
	switch goos {
	case "darwin":
		return []*exec.Cmd{exec.Command("/usr/bin/open", "-R", full)}
	case "windows":
		return []*exec.Cmd{exec.Command("explorer", "/select,"+full)} // NOSONAR system file manager from the user session
	}
	dir := full
	if info, err := os.Stat(full); err != nil || !info.IsDir() {
		dir = filepath.Dir(full)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
	return []*exec.Cmd{
		exec.Command("dbus-send", "--session", // NOSONAR system file manager from the user session
			"--print-reply", "--dest=org.freedesktop.FileManager1", "--type=method_call",
			"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:"+uri, "string:"),
		exec.Command("xdg-open", dir), // NOSONAR system file manager from the user session
	}
}

func runReveal(cmds []*exec.Cmd) error {
	var err error
	for i, cmd := range cmds {
		if i < len(cmds)-1 {
			if err = cmd.Run(); err == nil {
				return nil
			}
			continue
		}
		if err = cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	return err
}

// FileLink builds the nipa:// link that opens a file in the Files tab.
func (s *Service) FileLink(repoURL, p string) (string, error) {
	u, err := clientDomain.ParseNipaUrl(repoURL)
	if err != nil {
		return "", err
	}
	segments := []string{url.PathEscape(u.Org), url.PathEscape(u.Project), "file"}
	for _, seg := range strings.Split(strings.Trim(p, "/"), "/") {
		if seg != "" {
			segments = append(segments, url.PathEscape(seg))
		}
	}
	link := LinkScheme + "://" + u.Host + "/" + strings.Join(segments, "/")
	if u.Origin != linkServer(&url.URL{Host: u.Host}) {
		link += "?server=" + url.QueryEscape(u.Origin)
	}
	return link, nil
}

func toFileEntry(e *daemonpb.FileEntry) FileEntry {
	return FileEntry{
		Name: e.GetName(), Path: e.GetPath(), IsDir: e.GetIsDir(), Size: e.GetSizeBytes(), Binary: e.GetIsBinary(), Kind: e.GetKind(),
		Status: e.GetStatus(), Staged: e.GetStaged(), ChildrenChanged: e.GetChildrenChanged(), Tracked: e.GetTracked(), Hash: e.GetHash(), Mtime: e.GetMtimeNs(),
	}
}

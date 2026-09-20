package difftool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FilePair is one changed file handed to an external diff command.
type FilePair struct {
	Path       string
	Old        []byte
	New        []byte
	OldHash    string
	NewHash    string
	OldMode    string
	NewMode    string
	OldMissing bool
	NewMissing bool
}

// Run executes the external diff command once per pair using git's argument
// contract: <path> <old-file> <old-hex> <old-mode> <new-file> <new-hex>
// <new-mode>. Missing sides are passed as /dev/null. An exit status of 1 means
// "differences found" and is not an error; higher statuses abort.
func Run(ctx context.Context, command string, pairs []FilePair, out, errW io.Writer) error {
	args := strings.Fields(command)
	if len(args) == 0 {
		return errors.New("external diff command is empty")
	}
	if len(pairs) == 0 {
		return nil
	}
	dir, err := os.MkdirTemp("", "nipa-diff-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	for i, pair := range pairs {
		oldFile, newFile := os.DevNull, os.DevNull
		if !pair.OldMissing {
			oldFile = filepath.Join(dir, fmt.Sprintf("old-%d", i))
			if err := os.WriteFile(oldFile, pair.Old, 0o600); err != nil {
				return err
			}
		}
		if !pair.NewMissing {
			newFile = filepath.Join(dir, fmt.Sprintf("new-%d", i))
			if err := os.WriteFile(newFile, pair.New, 0o600); err != nil {
				return err
			}
		}

		argv := append([]string{}, args[1:]...)
		argv = append(argv, pair.Path, oldFile, pair.OldHash, pair.OldMode, newFile, pair.NewHash, pair.NewMode)
		cmd := exec.CommandContext(ctx, args[0], argv...)
		cmd.Stdout = out
		cmd.Stderr = errW
		if err := cmd.Run(); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				continue
			}
			return fmt.Errorf("external diff failed for %s: %w", pair.Path, err)
		}
	}
	return nil
}

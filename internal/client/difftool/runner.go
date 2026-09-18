package difftool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Runner interface {
	Run(ctx context.Context, argv []string, env []string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, argv []string, env []string) error {
	if len(argv) == 0 {
		return errors.New("difftool: empty command")
	}
	name := argv[0]
	resolved, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("diff tool %q not found: %w", name, err)
	}
	args := argv[1:]
	if runtime.GOOS == "windows" {
		if ext := strings.ToLower(filepath.Ext(resolved)); ext == ".bat" || ext == ".cmd" {
			args = append([]string{"/c", resolved}, args...)
			resolved = "cmd"
		}
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Run(ctx context.Context, r Runner, tmpl Command, tree *Tree, pairs []Pair, w io.Writer) error {
	for i, p := range pairs {
		if w != nil {
			fmt.Fprintf(w, "Viewing (%d/%d): '%s'\n", i+1, len(pairs), p.Path)
		}
		argv, env := tmpl.Expand(p, tree.OldDir, tree.NewDir)
		if err := r.Run(ctx, argv, env); err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				continue
			}
			return fmt.Errorf("run external diff tool: %w", err)
		}
	}
	return nil
}

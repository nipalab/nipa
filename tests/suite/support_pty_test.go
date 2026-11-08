package suite_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const ansiEscape = "\x1b["

// colorEnv is the CLI environment without the harness-wide NO_COLOR, so color
// decisions depend only on the flags and the output stream.
func colorEnv(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "NO_COLOR=") || strings.HasPrefix(kv, "NIPA_TOKEN_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "NIPA_TOKEN_FILE="+cli.tokenFile, "TERM=xterm")
	return append(env, extra...)
}

func runNipaPiped(cwd string, args ...string) commandResult {
	GinkgoHelper()
	cmd := exec.Command(cli.binary, args...)
	cmd.Dir = cwd
	cmd.Env = colorEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		Fail("failed to run nipa " + strings.Join(args, " ") + ": " + err.Error())
	}
	return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type ptyRun struct {
	cmd    *exec.Cmd
	master *os.File
	out    *lockedBuffer
	done   chan error
}

func startNipaPTY(cwd string, extraEnv []string, args ...string) *ptyRun {
	GinkgoHelper()
	cmd := exec.Command(cli.binary, args...)
	cmd.Dir = cwd
	cmd.Env = colorEnv(extraEnv...)
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 160})
	Expect(err).NotTo(HaveOccurred())
	run := &ptyRun{cmd: cmd, master: master, out: &lockedBuffer{}, done: make(chan error, 1)}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(run.out, master)
		close(copied)
	}()
	go func() {
		err := cmd.Wait()
		select {
		case <-copied:
		case <-time.After(2 * time.Second):
		}
		run.done <- err
	}()
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_ = master.Close()
	})
	return run
}

// wait returns the output once the command exits on its own; a pager waiting
// for keyboard input fails the spec instead of hanging it.
func (r *ptyRun) wait(timeout time.Duration) string {
	GinkgoHelper()
	select {
	case err := <-r.done:
		Expect(err).NotTo(HaveOccurred(), r.out.String())
	case <-time.After(timeout):
		Fail("nipa did not exit without input (an interactive pager is running?): " + r.out.String())
	}
	GinkgoWriter.Printf("$ nipa %s (pty)\n%q\n", strings.Join(r.cmd.Args[1:], " "), r.out.String())
	return r.out.String()
}

func runNipaPTY(cwd string, extraEnv []string, args ...string) string {
	GinkgoHelper()
	return startNipaPTY(cwd, extraEnv, args...).wait(20 * time.Second)
}

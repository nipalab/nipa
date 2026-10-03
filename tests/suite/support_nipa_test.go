package suite_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2"
)

type commandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Output combines both streams: the CLI prints some command output (status,
// errors) through cobra's stderr writer while progress goes to stdout.
func (r commandResult) Output() string {
	return r.Stdout + r.Stderr
}

func runNipa(cwd string, args ...string) commandResult {
	GinkgoHelper()
	return runNipaEnv(cwd, nil, args...)
}

// runNipaEnv runs the CLI with extra environment entries appended (later
// duplicates win, so callers can override NIPA_TOKEN_FILE per identity).
func runNipaEnv(cwd string, extraEnv []string, args ...string) commandResult {
	GinkgoHelper()

	cmd := exec.Command(cli.binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "NIPA_TOKEN_FILE="+cli.tokenFile, "NO_COLOR=1")
	cmd.Env = append(cmd.Env, extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		Fail("failed to run nipa " + strings.Join(args, " ") + ": " + err.Error())
	}

	GinkgoWriter.Printf("$ nipa %s (exit %d)\n%s%s\n", strings.Join(args, " "), exitCode, stdout.String(), stderr.String())
	return commandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}
}

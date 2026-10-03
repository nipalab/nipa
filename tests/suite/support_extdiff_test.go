package suite_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// externalDiffTool writes a POSIX shell script that records each invocation's
// argv into a log file and exits 0. It returns the script path and log path.
func externalDiffTool(dir string) (script, logPath string) {
	GinkgoHelper()

	logPath = filepath.Join(dir, "extdiff.log")
	script = filepath.Join(dir, "extdiff.sh")
	content := "#!/bin/sh\nprintf '%s\\n' \"$#\" \"$@\" >> \"$NIPA_EXTDIFF_LOG\"\n"
	Expect(os.WriteFile(script, []byte(content), 0o755)).To(Succeed())
	return script, logPath
}

func externalDiffEnv(script, logPath string) []string {
	return []string{
		"NIPA_EXTERNAL_DIFF=" + script,
		"NIPA_EXTDIFF_LOG=" + logPath,
	}
}

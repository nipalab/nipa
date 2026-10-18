package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// replayTerminal applies carriage-return overwrites the way a terminal does
// and returns the committed lines, so tests can assert what a user sees.
func replayTerminal(out string) []string {
	var lines []string
	var current []byte
	overwrite := func(text string) {
		if len(text) > len(current) {
			current = append(current, make([]byte, len(text)-len(current))...)
		}
		copy(current, text)
	}
	for _, segment := range strings.Split(out, "\r") {
		for {
			i := strings.IndexByte(segment, '\n')
			if i < 0 {
				overwrite(segment)
				break
			}
			overwrite(segment[:i])
			lines = append(lines, string(current))
			current = current[:0]
			segment = segment[i+1:]
		}
	}
	return lines
}

func TestProgressRenderer_Download(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.DownloadStart(3, 2<<20)
	p.DownloadProgress(1, 1<<20)
	p.DownloadProgress(2, 1<<20+1<<19)
	p.DownloadProgress(3, 2<<20)
	p.DownloadEnd()

	out := buf.String()
	require.Contains(t, out, "Downloading 3 objects (2.0 MiB)...\n")
	require.Contains(t, out, "3/3 objects, 2.0 MiB downloaded (100%)")
	require.Regexp(t, `Downloaded 3 objects \(2\.0 MiB\) *\n`, out)
}

func TestProgressRenderer_Upload(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.UploadStart(4, 2<<20)
	p.UploadProgress(1, 512<<10)
	p.UploadProgress(2, 1<<20)
	p.UploadProgress(4, 2<<20)
	p.UploadEnd()

	out := buf.String()
	require.Contains(t, out, "Uploading 4 objects (2.0 MiB)...\n")
	require.Contains(t, out, "1/4 objects, 512.0 KiB uploaded (25%)")
	require.Contains(t, out, "4/4 objects, 2.0 MiB uploaded (100%)")
	require.Regexp(t, `Uploaded 4 objects \(2\.0 MiB\) *\n`, out)
}

func TestProgressRenderer_PercentageIsCapped(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.DownloadStart(1, 100)
	p.DownloadProgress(1, 10_000) // receive more than the estimate

	require.Contains(t, buf.String(), "(100%)", "the percentage must never exceed 100")
}

func TestProgressRenderer_PercentageFallsBackToObjects(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.DownloadStart(4, 0) // no byte estimate available
	p.DownloadProgress(2, 1<<20)

	require.Contains(t, buf.String(), "(50%)")
}

func TestProgressRenderer_ShorterFinishClearsTheUpdateLine(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.DownloadStart(514, 0)
	p.DownloadProgress(514, 2<<30)
	p.DownloadEnd()

	lines := replayTerminal(buf.String())
	require.Len(t, lines, 2)
	require.Equal(t, "Downloading 514 objects (0 B)...", lines[0])
	require.Equal(t, "  Downloaded 514 objects (2.0 GiB)", strings.TrimRight(lines[1], " "),
		"the finish line must not leave the tail of the longer update line visible")
}

func TestProgressRenderer_ShrinkingFramesArePadded(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.UploadStart(100, 2<<20)
	p.UploadProgress(100, 2<<20) // "100/100 objects, 2.0 MiB uploaded (100%)"
	p.UploadProgress(9, 1<<20)   // shorter frame
	p.UploadEnd()

	lines := replayTerminal(buf.String())
	require.Equal(t, "  Uploaded 9 objects (1.0 MiB)", strings.TrimRight(lines[len(lines)-1], " "))
}

func TestProgressRenderer_FinishReportsActuals(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.UploadStart(100, 1<<20)
	p.UploadProgress(3, 32<<10)
	p.UploadEnd()

	out := buf.String()
	require.Contains(t, out, "Uploaded 3 objects (32.0 KiB)")
	require.NotContains(t, out, "Uploaded 100 objects")
}

func TestProgressRenderer_SmallTransfer(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.UploadStart(1, 512)
	p.UploadEnd()

	out := buf.String()
	require.Contains(t, out, "Uploading 1 objects (512 B)...")
	require.Contains(t, out, "Uploaded 1 objects (512 B)")
}

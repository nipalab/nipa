package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadProgressRenderer(t *testing.T) {
	var buf bytes.Buffer
	p := newDownloadProgress(&buf)

	p.DownloadStart(3, 2<<20)
	p.DownloadProgress(1, 1<<20)
	p.DownloadProgress(2, 1<<20+1<<19)
	p.DownloadProgress(3, 2<<20)
	p.DownloadEnd()

	out := buf.String()
	require.Contains(t, out, "Downloading 3 objects (2.0 MiB)...\n")
	require.Contains(t, out, "3/3 objects, 2.0 MiB downloaded (100%)")
	require.Contains(t, out, "Downloaded 3 objects (2.0 MiB)\n")
}

func TestDownloadProgressRenderer_PercentageIsCapped(t *testing.T) {
	var buf bytes.Buffer
	p := newDownloadProgress(&buf)

	p.DownloadStart(1, 100)
	p.DownloadProgress(1, 10_000) // receive more than the estimate

	require.Contains(t, buf.String(), "(100%)", "the percentage must never exceed 100")
}

func TestDownloadProgressRenderer_SmallTransfer(t *testing.T) {
	var buf bytes.Buffer
	p := newDownloadProgress(&buf)

	p.DownloadStart(1, 512)
	p.DownloadEnd()

	out := buf.String()
	require.Contains(t, out, "Downloading 1 objects (512 B)...")
	require.Contains(t, out, "Downloaded 1 objects (512 B)")
}

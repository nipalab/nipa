package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

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
	require.Contains(t, out, "Downloaded 3 objects (2.0 MiB)\n")
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
	require.Contains(t, out, "Uploaded 4 objects (2.0 MiB)\n")
}

func TestProgressRenderer_PercentageIsCapped(t *testing.T) {
	var buf bytes.Buffer
	p := newProgressRenderer(&buf)

	p.DownloadStart(1, 100)
	p.DownloadProgress(1, 10_000) // receive more than the estimate

	require.Contains(t, buf.String(), "(100%)", "the percentage must never exceed 100")
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

package cli

import (
	"fmt"
	"io"
)

type downloadProgress struct {
	w            io.Writer
	totalObjects int
	totalBytes   int64
}

func newDownloadProgress(w io.Writer) *downloadProgress {
	return &downloadProgress{w: w}
}

func (p *downloadProgress) DownloadStart(objects int, estimatedBytes int64) {
	p.totalObjects = objects
	p.totalBytes = estimatedBytes
	fmt.Fprintf(p.w, "Downloading %d objects (%s)...\n", objects, humanBytes(estimatedBytes))
}

func (p *downloadProgress) DownloadProgress(objectsDone int, bytesDone int64) {
	pct := int64(0)
	if p.totalBytes > 0 {
		pct = bytesDone * 100 / p.totalBytes
		if pct > 100 {
			pct = 100
		}
	}
	fmt.Fprintf(p.w, "\r  %d/%d objects, %s downloaded (%d%%)", objectsDone, p.totalObjects, humanBytes(bytesDone), pct)
}

func (p *downloadProgress) DownloadEnd() {
	fmt.Fprintf(p.w, "\r  Downloaded %d objects (%s)\n", p.totalObjects, humanBytes(p.totalBytes))
}

func humanBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(1024), 0
	for n := b / 1024; n >= 1024; n /= 1024 {
		div *= 1024
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

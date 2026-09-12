package cli

import (
	"fmt"
	"io"
)

type progressRenderer struct {
	w            io.Writer
	verb         string
	totalObjects int
	totalBytes   int64
}

func newProgressRenderer(w io.Writer) *progressRenderer {
	return &progressRenderer{w: w}
}

func (p *progressRenderer) DownloadStart(objects int, estimatedBytes int64) {
	p.begin("Downloading", objects, estimatedBytes)
}

func (p *progressRenderer) DownloadProgress(objectsDone int, bytesDone int64) {
	p.update(objectsDone, bytesDone)
}

func (p *progressRenderer) DownloadEnd() {
	p.finish("Downloaded")
}

func (p *progressRenderer) UploadStart(objects int, totalBytes int64) {
	p.begin("Uploading", objects, totalBytes)
}

func (p *progressRenderer) UploadProgress(objectsDone int, bytesDone int64) {
	p.update(objectsDone, bytesDone)
}

func (p *progressRenderer) UploadEnd() {
	p.finish("Uploaded")
}

func (p *progressRenderer) begin(verb string, objects int, bytes int64) {
	p.verb = verb
	p.totalObjects = objects
	p.totalBytes = bytes
	fmt.Fprintf(p.w, "%s %d objects (%s)...\n", verb, objects, humanBytes(bytes))
}

func (p *progressRenderer) update(objectsDone int, bytesDone int64) {
	pct := int64(0)
	if p.totalBytes > 0 {
		pct = bytesDone * 100 / p.totalBytes
		if pct > 100 {
			pct = 100
		}
	}
	fmt.Fprintf(p.w, "\r  %d/%d objects, %s %s (%d%%)", objectsDone, p.totalObjects, humanBytes(bytesDone), pastTense(p.verb), pct)
}

func (p *progressRenderer) finish(verb string) {
	fmt.Fprintf(p.w, "\r  %s %d objects (%s)\n", verb, p.totalObjects, humanBytes(p.totalBytes))
}

func pastTense(verb string) string {
	switch verb {
	case "Downloading":
		return "downloaded"
	case "Uploading":
		return "uploaded"
	default:
		return verb
	}
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

package daemon

import (
	"sync"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const opEventBuffer = 64

// progressAdapter bridges the usecase progress callbacks onto the OpEvent
// stream. Events are dropped when the buffer is full: progress is coalesced by
// nature and the stream handler drains eagerly, while the terminal event is
// sent by runOp and never travels through this adapter.
type progressAdapter struct {
	ch chan *daemonpb.OpEvent

	mu      sync.Mutex
	dlTotal [2]int64 // objects, bytes
	upTotal [2]int64
}

func newProgressAdapter() *progressAdapter {
	return &progressAdapter{ch: make(chan *daemonpb.OpEvent, opEventBuffer)}
}

func (p *progressAdapter) events() <-chan *daemonpb.OpEvent {
	return p.ch
}

func (p *progressAdapter) emit(ev *daemonpb.OpEvent) {
	select {
	case p.ch <- ev:
	default:
	}
}

func (p *progressAdapter) DownloadStart(objects int, estimatedBytes int64) {
	p.mu.Lock()
	p.dlTotal = [2]int64{int64(objects), estimatedBytes}
	p.mu.Unlock()
	p.emit(opStartedEvent("download"))
	p.emit(p.downloadProgress(0, 0))
}

func (p *progressAdapter) DownloadProgress(objectsDone int, bytesDone int64) {
	p.emit(p.downloadProgress(int64(objectsDone), bytesDone))
}

func (p *progressAdapter) DownloadEnd() {
	p.mu.Lock()
	total := p.dlTotal
	p.mu.Unlock()
	p.emit(p.downloadProgress(total[0], total[1]))
}

func (p *progressAdapter) UploadStart(objects int, totalBytes int64) {
	p.mu.Lock()
	p.upTotal = [2]int64{int64(objects), totalBytes}
	p.mu.Unlock()
	p.emit(opStartedEvent("upload"))
	p.emit(p.uploadProgress(0, 0))
}

func (p *progressAdapter) UploadProgress(objectsDone int, bytesDone int64) {
	p.emit(p.uploadProgress(int64(objectsDone), bytesDone))
}

func (p *progressAdapter) UploadEnd() {
	p.mu.Lock()
	total := p.upTotal
	p.mu.Unlock()
	p.emit(p.uploadProgress(total[0], total[1]))
}

func (p *progressAdapter) downloadProgress(objectsDone, bytesDone int64) *daemonpb.OpEvent {
	p.mu.Lock()
	total := p.dlTotal
	p.mu.Unlock()
	return opProgressEvent("download", objectsDone, total[0], bytesDone, total[1])
}

func (p *progressAdapter) uploadProgress(objectsDone, bytesDone int64) *daemonpb.OpEvent {
	p.mu.Lock()
	total := p.upTotal
	p.mu.Unlock()
	return opProgressEvent("upload", objectsDone, total[0], bytesDone, total[1])
}

package daemon

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/thumbcache"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/preview"
)

const (
	// maxThumbnailSize is the largest max_size a one-sided Preview may ask for
	// to be treated as a thumbnail: cached on disk and decoded under the
	// thumbnail concurrency limit.
	maxThumbnailSize = 256
	// thumbnailDecodeSlots bounds concurrent thumbnail reads and decodes so a
	// scrolling grid cannot pile up full-resolution decodes.
	thumbnailDecodeSlots = 2
	// thumbnailFormatVersion is part of every cache key. Bump it when a
	// decoder, the downscale or the cached record changes, so old entries
	// stop matching (they age out through eviction).
	thumbnailFormatVersion = 1
)

// thumbnails holds the daemon's thumbnail cache (opened on first use, so a
// daemon that never renders a thumbnail never creates the folder) and the
// decode slots.
type thumbnails struct {
	dir   string
	slots chan struct{}

	once  sync.Once
	cache *thumbcache.Cache
}

func newThumbnails(dir string) *thumbnails {
	return &thumbnails{dir: dir, slots: make(chan struct{}, thumbnailDecodeSlots)}
}

// store returns the cache, or nil when its folder cannot be opened; the
// thumbnail is then rendered without caching.
func (t *thumbnails) store() *thumbcache.Cache {
	t.once.Do(func() {
		dir := t.dir
		if dir == "" {
			var err error
			if dir, err = thumbcache.DefaultDir(); err != nil {
				return
			}
		}
		if c, err := thumbcache.New(dir, thumbcache.DefaultMaxBytes); err == nil {
			t.cache = c
		}
	})
	return t.cache
}

// acquire takes a decode slot, giving up when ctx ends first.
func (t *thumbnails) acquire(ctx context.Context) (func(), error) {
	select {
	case t.slots <- struct{}{}:
		return func() { <-t.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// thumbnailRequest reports whether req is a thumbnail: exactly one side and a
// small max_size. Two-sided compares and full-size previews are not.
func thumbnailRequest(req *daemonpb.PreviewRequest, maxSize int) (string, *daemonpb.BlobRef, bool) {
	if maxSize > maxThumbnailSize {
		return "", nil, false
	}
	switch {
	case req.GetOld() != nil && req.GetNew() == nil:
		return previewSideOld, req.GetOld(), true
	case req.GetNew() != nil && req.GetOld() == nil:
		return previewSideNew, req.GetNew(), true
	}
	return "", nil, false
}

// thumbnailKey builds the cache key of one rendering. Stored versions are
// keyed by content hash (plus the extension, which picks the decoder);
// working files, whose hash is unknown without reading them, by absolute
// path, size and modification time. ok is false when neither is known.
func thumbnailKey(st usecase.BlobStat, sel preview.Select, maxSize int) (string, bool) {
	tail := fmt.Sprintf("%d|%d|%d", maxSize, sel.Level, sel.Face)
	switch {
	case st.Working():
		return fmt.Sprintf("v%d|w|%s|%d|%d|%s", thumbnailFormatVersion, st.AbsPath, st.Entry.SizeBytes, st.ModTimeNS, tail), true
	case st.Entry.Hash != (serverDomain.Hash{}):
		ext := strings.ToLower(filepath.Ext(st.Entry.Path))
		return fmt.Sprintf("v%d|h|%s|%s|%d|%s", thumbnailFormatVersion, st.Entry.Hash, ext, st.Entry.SizeBytes, tail), true
	}
	return "", false
}

// previewThumbnail serves a one-sided small preview. It resolves the side's
// identity without reading its content (StatBlob: a plain stat for working
// files, the tree entry for stored versions) and answers a cache hit from
// disk, so a hit never reads or decodes the file and never waits for a
// decode slot. A miss takes a decode slot, reads and decodes as Preview
// does (textures from the smallest mip that covers maxSize), and stores the
// rendering when the file did not change meanwhile.
func (s *Server) previewThumbnail(ctx context.Context, stream daemonpb.NipaDaemon_PreviewServer, rp *repo, side string, ref *daemonpb.BlobRef, sel preview.Select, maxSize int) error {
	cache := s.thumbs.store()
	var key string
	var st usecase.BlobStat
	keyed := false
	if cache != nil {
		var err error
		if st, err = rp.diff.StatBlob(ctx, rp.root, blobRequest(ref)); err == nil {
			key, keyed = thumbnailKey(st, sel, maxSize)
		}
		if keyed {
			if r, ok := loadThumbnail(cache, key); ok {
				return sendRenderedSide(stream, side, r)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	release, err := s.thumbs.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	if keyed {
		// Another request may have rendered it while this one waited.
		if r, ok := loadThumbnail(cache, key); ok {
			return sendRenderedSide(stream, side, r)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	decoded, err := decodePreviewSide(ctx, rp.diff, rp.root, side, ref, sel, maxSize)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return stream.Send(previewFailureEvent(err))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r, err := renderPreviewSide(decoded, maxSize)
	if err != nil {
		return err
	}
	if keyed && s.thumbnailStillCurrent(ctx, rp, ref, st, decoded, sel, maxSize, key) {
		// A failed write only costs a decode next time.
		_ = storeThumbnail(cache, key, r)
	}
	return sendRenderedSide(stream, side, r)
}

// thumbnailStillCurrent guards against caching a rendering under a key that
// no longer describes what was read: the read must match the stat'ed size
// and hash, and a working file must stat the same after the read.
func (s *Server) thumbnailStillCurrent(ctx context.Context, rp *repo, ref *daemonpb.BlobRef, before usecase.BlobStat, decoded decodedSide, sel preview.Select, maxSize int, key string) bool {
	if decoded.entry.SizeBytes != before.Entry.SizeBytes {
		return false
	}
	if !before.Working() {
		return decoded.entry.Hash == before.Entry.Hash
	}
	after, err := rp.diff.StatBlob(ctx, rp.root, blobRequest(ref))
	if err != nil {
		return false
	}
	again, ok := thumbnailKey(after, sel, maxSize)
	return ok && again == key
}

// A cached rendering is the marshalled ImagePreview (without its side),
// length-prefixed, followed by the PNG bytes.
func storeThumbnail(cache *thumbcache.Cache, key string, r renderedSide) error {
	info, err := proto.Marshal(r.info)
	if err != nil {
		return err
	}
	value := binary.AppendUvarint(nil, uint64(len(info)))
	value = append(value, info...)
	return cache.Put(key, append(value, r.png...))
}

func loadThumbnail(cache *thumbcache.Cache, key string) (renderedSide, bool) {
	value, ok := cache.Get(key)
	if !ok {
		return renderedSide{}, false
	}
	n, read := binary.Uvarint(value)
	if read <= 0 || n > uint64(len(value)-read) {
		return renderedSide{}, false
	}
	info := &daemonpb.ImagePreview{}
	if err := proto.Unmarshal(value[read:read+int(n)], info); err != nil {
		return renderedSide{}, false
	}
	png := value[read+int(n):]
	if len(png) == 0 {
		return renderedSide{}, false
	}
	return renderedSide{info: info, png: png}, true
}

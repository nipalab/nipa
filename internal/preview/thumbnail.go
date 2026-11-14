package preview

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// ErrNoThumbnail means the file carries no embedded preview image.
var ErrNoThumbnail = errors.New("preview: no embedded thumbnail")

const (
	maxThumbnailSide    = 2048
	maxBlendScanBytes   = 64 << 20
	maxBlendBlocks      = 4096
	maxUnrealThumbBytes = 16 << 20
)

var thumbnailExtensions = map[string]bool{".blend": true, ".uasset": true, ".umap": true}

// HasEmbeddedThumbnail reports whether a path is a format whose thumbnail
// DecodeThumbnail can extract.
func HasEmbeddedThumbnail(path string) bool {
	return thumbnailExtensions[strings.ToLower(filepath.Ext(path))]
}

// DecodeThumbnail extracts the preview image a Blender file or an Unreal
// package stores for file browsers. The Image's Format names the source.
func DecodeThumbnail(name string, data []byte) (*Image, error) {
	var img image.Image
	var format string
	var err error
	switch strings.ToLower(filepath.Ext(name)) {
	case ".blend":
		img, err = blendThumbnail(data)
		format = "Blender thumbnail"
	case ".uasset", ".umap":
		img, format, err = unrealThumbnail(data)
	default:
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, err
	}
	out := fromImage(img, format)
	out.Levels, out.Faces = 1, 1
	return out, nil
}

func fromImage(src image.Image, format string) *Image {
	pixels := toNRGBA(src)
	return &Image{Pixels: pixels, Format: format, BitDepth: 8, HasAlpha: hasAlpha(pixels)}
}

func blendReader(data []byte) (io.Reader, func(), error) {
	switch {
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}):
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, nil, err
		}
		return zr, func() { _ = zr.Close() }, nil
	case bytes.HasPrefix(data, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderMaxMemory(maxBlendScanBytes), zstd.WithDecoderConcurrency(1))
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	}
	return bytes.NewReader(data), func() {}, nil
}

type blendHeader struct {
	order   binary.ByteOrder
	pointer int
	large   bool
}

func readBlendHeader(r *bufio.Reader) (blendHeader, error) {
	bad := errors.New("preview: not a Blender file")
	head, err := r.Peek(17)
	if len(head) < 12 || (err != nil && len(head) < 12) || !bytes.HasPrefix(head, []byte("BLENDER")) {
		return blendHeader{}, bad
	}
	h := blendHeader{}
	var endian byte
	var skip int
	if len(head) >= 17 && head[7] >= '0' && head[7] <= '9' {
		size, err := strconv.Atoi(string(head[7:9]))
		if err != nil || size != 17 || head[9] != '-' {
			return h, bad
		}
		version, err := strconv.Atoi(string(head[10:12]))
		if err != nil || version != 1 {
			return h, ErrUnsupported
		}
		h.pointer, h.large, endian, skip = 8, true, head[12], 17
	} else {
		switch head[7] {
		case '_':
			h.pointer = 4
		case '-':
			h.pointer = 8
		default:
			return h, bad
		}
		endian, skip = head[8], 12
	}
	switch endian {
	case 'v':
		h.order = binary.LittleEndian
	case 'V':
		h.order = binary.BigEndian
	default:
		return h, bad
	}
	_, err = r.Discard(skip)
	return h, err
}

func blendThumbnail(data []byte) (image.Image, error) {
	src, done, err := blendReader(data)
	if err != nil {
		return nil, errors.New("preview: not a Blender file")
	}
	defer done()
	r := bufio.NewReader(io.LimitReader(src, maxBlendScanBytes))
	h, err := readBlendHeader(r)
	if err != nil {
		return nil, err
	}
	headLen := 16 + h.pointer
	if h.large {
		headLen = 32
	}
	buf := make([]byte, headLen)
	for i := 0; i < maxBlendBlocks; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, ErrNoThumbnail
		}
		code := string(buf[:4])
		var length int64
		if h.large {
			length = int64(h.order.Uint64(buf[16:24]))
		} else {
			length = int64(int32(h.order.Uint32(buf[4:8])))
		}
		if length < 0 || length > maxBlendScanBytes {
			return nil, errors.New("preview: malformed Blender block")
		}
		switch code {
		case "TEST":
			return readBlendTest(r, h.order, length)
		case "ENDB":
			return nil, ErrNoThumbnail
		}
		if _, err := r.Discard(int(length)); err != nil {
			return nil, ErrNoThumbnail
		}
	}
	return nil, ErrNoThumbnail
}

func readBlendTest(r io.Reader, order binary.ByteOrder, length int64) (image.Image, error) {
	var dims [8]byte
	if length < 8 {
		return nil, ErrNoThumbnail
	}
	if _, err := io.ReadFull(r, dims[:]); err != nil {
		return nil, ErrNoThumbnail
	}
	w, h := int(int32(order.Uint32(dims[0:4]))), int(int32(order.Uint32(dims[4:8])))
	if w <= 0 || h <= 0 || w > maxThumbnailSide || h > maxThumbnailSide || int64(w)*int64(h)*4 > length-8 {
		return nil, errors.New("preview: malformed Blender thumbnail")
	}
	px := make([]byte, w*h*4)
	if _, err := io.ReadFull(r, px); err != nil {
		return nil, errors.New("preview: truncated Blender thumbnail")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	stride := w * 4
	for y := 0; y < h; y++ {
		copy(img.Pix[y*stride:(y+1)*stride], px[(h-1-y)*stride:(h-y)*stride])
	}
	return img, nil
}

var (
	pngSignature = []byte("\x89PNG\r\n\x1a\n")
	jpegSOI      = []byte{0xFF, 0xD8, 0xFF}
)

// unrealThumbnail finds the package thumbnail: Unreal stores each one as
// int32 width, int32 height (negative for JPEG) and int32 size followed by
// the compressed image.
func unrealThumbnail(data []byte) (image.Image, string, error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data[:4]) != 0x9E2A83C1 {
		return nil, "", errors.New("preview: not an Unreal package")
	}
	for _, sig := range [][]byte{pngSignature, jpegSOI} {
		for from := 12; from < len(data); {
			i := bytes.Index(data[from:], sig)
			if i < 0 {
				break
			}
			at := from + i
			from = at + 1
			if img, format, ok := unrealCandidate(data, at, sig[0] == 0x89); ok {
				return img, format, nil
			}
		}
	}
	return nil, "", ErrNoThumbnail
}

func unrealCandidate(data []byte, at int, isPNG bool) (image.Image, string, bool) {
	if at < 12 {
		return nil, "", false
	}
	w := int(int32(binary.LittleEndian.Uint32(data[at-12:])))
	h := int(int32(binary.LittleEndian.Uint32(data[at-8:])))
	size := int(int32(binary.LittleEndian.Uint32(data[at-4:])))
	if isPNG == (h < 0) {
		return nil, "", false
	}
	h = max(h, -h)
	if w <= 0 || h <= 0 || w > maxThumbnailSide || h > maxThumbnailSide || size <= 0 || size > maxUnrealThumbBytes || at+size > len(data) {
		return nil, "", false
	}
	blob := bytes.NewReader(data[at : at+size])
	var cfg image.Config
	var err error
	if isPNG {
		cfg, err = png.DecodeConfig(blob)
	} else {
		cfg, err = jpeg.DecodeConfig(blob)
	}
	if err != nil || cfg.Width != w || cfg.Height != h {
		return nil, "", false
	}
	_, _ = blob.Seek(0, io.SeekStart)
	if isPNG {
		img, err := png.Decode(blob)
		return img, "Unreal thumbnail (PNG)", err == nil
	}
	img, err := jpeg.Decode(blob)
	return img, "Unreal thumbnail (JPEG)", err == nil
}

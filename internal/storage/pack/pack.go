// Package pack is the chunk pack object format: many chunks of one project in
// one store object, so a bundle upload costs one store write and a clone
// reads neighbouring chunks with one range request. Packs are written by the
// server only and never overwritten.
//
//	"NPK1"                                  4 bytes magic
//	chunk data, concatenated                offsets are relative to the object start
//	index: count x (hash 32 | offset u64 | length u32)
//	index offset u64 | "NPK1"               12-byte trailer
//
// Integers are big-endian. The data region is what range reads touch; the
// index and trailer make a pack self-describing, so chunk locations can be
// rebuilt or checked from the bucket alone.
package pack

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/nipalab/nipa/internal/domain"
)

// hashSize is the size of a chunk hash (BLAKE3-256).
const hashSize = len(domain.Hash{})

const (
	// Magic opens and closes every pack.
	Magic = "NPK1"
	// HeaderSize is the size of the leading magic; the first chunk starts
	// here.
	HeaderSize = len(Magic)
	// IndexEntrySize is the size of one index entry: hash, offset, length.
	IndexEntrySize = hashSize + 8 + 4
	// TrailerSize is the size of the trailer: index offset and magic.
	TrailerSize = 8 + len(Magic)
	// MaxEntries bounds the index a reader accepts, far above any bundle.
	MaxEntries = 1 << 20
)

// ErrInvalid is returned for a pack whose framing, index or trailer is
// malformed.
var ErrInvalid = errors.New("invalid chunk pack")

// Entry is one chunk to pack.
type Entry struct {
	Hash domain.Hash
	Data []byte
}

// IndexEntry is where one chunk sits in a pack.
type IndexEntry struct {
	Hash   domain.Hash
	Offset int64
	Length int64
}

// Pack is an encoded pack held as segments (the header, the chunk data the
// caller owns and the index with trailer), so building one copies no chunk
// bytes.
type Pack struct {
	segments [][]byte
	size     int64
	index    []IndexEntry
}

// Build lays the entries out in order. The chunk data is referenced, not
// copied; the caller must not change it while the pack is in use. Every
// entry must be non-empty and at most math.MaxUint32 bytes long.
func Build(entries []Entry) (*Pack, error) {
	if len(entries) == 0 || len(entries) > MaxEntries {
		return nil, fmt.Errorf("%w: %d entries", ErrInvalid, len(entries))
	}
	segments := make([][]byte, 0, len(entries)+2)
	segments = append(segments, []byte(Magic))
	index := make([]IndexEntry, 0, len(entries))
	offset := int64(HeaderSize)
	for _, entry := range entries {
		length := int64(len(entry.Data))
		if length == 0 || length > math.MaxUint32 {
			return nil, fmt.Errorf("%w: entry %s of %d bytes", ErrInvalid, entry.Hash, length)
		}
		segments = append(segments, entry.Data)
		index = append(index, IndexEntry{Hash: entry.Hash, Offset: offset, Length: length})
		offset += length
	}
	tail := make([]byte, 0, len(index)*IndexEntrySize+TrailerSize)
	for _, entry := range index {
		tail = append(tail, entry.Hash[:]...)
		tail = binary.BigEndian.AppendUint64(tail, uint64(entry.Offset))
		tail = binary.BigEndian.AppendUint32(tail, uint32(entry.Length))
	}
	tail = binary.BigEndian.AppendUint64(tail, uint64(offset))
	tail = append(tail, Magic...)
	segments = append(segments, tail)
	return &Pack{segments: segments, size: offset + int64(len(tail)), index: index}, nil
}

// Size is the encoded size in bytes.
func (p *Pack) Size() int64 {
	return p.size
}

// Index returns where each entry sits, in the order given to Build.
func (p *Pack) Index() []IndexEntry {
	return p.index
}

// Reader returns a fresh reader over the encoded pack. It also seeks, so an
// uploader can replay the body after a failed attempt.
func (p *Pack) Reader() io.ReadSeeker {
	return &segmentReader{segments: p.segments, size: p.size}
}

// Bytes returns the encoded pack as one slice (a copy).
func (p *Pack) Bytes() []byte {
	out := make([]byte, 0, p.size)
	for _, segment := range p.segments {
		out = append(out, segment...)
	}
	return out
}

// segmentReader reads and seeks over concatenated segments.
type segmentReader struct {
	segments [][]byte
	size     int64
	pos      int64
}

func (r *segmentReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	n := 0
	start := int64(0)
	for _, segment := range r.segments {
		end := start + int64(len(segment))
		if r.pos < end && n < len(p) {
			copied := copy(p[n:], segment[r.pos-start:])
			n += copied
			r.pos += int64(copied)
		}
		start = end
		if n == len(p) {
			break
		}
	}
	return n, nil
}

func (r *segmentReader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.size + offset
	default:
		return 0, errors.New("pack: invalid whence")
	}
	if next < 0 {
		return 0, errors.New("pack: negative position")
	}
	r.pos = next
	return next, nil
}

// ReadIndex reads and checks the index of a pack of size bytes: the magic at
// both ends, an index offset inside the object, a whole number of entries
// and every entry inside the data region with a positive length. It does not
// hash the chunk data.
func ReadIndex(r io.ReaderAt, size int64) ([]IndexEntry, error) {
	if size < int64(HeaderSize+TrailerSize) {
		return nil, fmt.Errorf("%w: %d bytes", ErrInvalid, size)
	}
	head := make([]byte, HeaderSize)
	if _, err := r.ReadAt(head, 0); err != nil {
		return nil, fmt.Errorf("pack: read header: %w", err)
	}
	trailer := make([]byte, TrailerSize)
	if _, err := r.ReadAt(trailer, size-int64(TrailerSize)); err != nil {
		return nil, fmt.Errorf("pack: read trailer: %w", err)
	}
	if string(head) != Magic || string(trailer[8:]) != Magic {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalid)
	}
	indexEnd := size - int64(TrailerSize)
	rawOffset := binary.BigEndian.Uint64(trailer)
	if rawOffset < uint64(HeaderSize) || rawOffset > uint64(indexEnd) {
		return nil, fmt.Errorf("%w: index offset %d", ErrInvalid, rawOffset)
	}
	indexOffset := int64(rawOffset)
	indexLen := indexEnd - indexOffset
	if indexLen%int64(IndexEntrySize) != 0 || indexLen/int64(IndexEntrySize) > MaxEntries {
		return nil, fmt.Errorf("%w: index of %d bytes", ErrInvalid, indexLen)
	}
	raw := make([]byte, indexLen)
	if _, err := r.ReadAt(raw, indexOffset); err != nil {
		return nil, fmt.Errorf("pack: read index: %w", err)
	}
	entries := make([]IndexEntry, 0, indexLen/int64(IndexEntrySize))
	for pos := 0; pos < len(raw); pos += IndexEntrySize {
		var entry IndexEntry
		copy(entry.Hash[:], raw[pos:pos+hashSize])
		offset := binary.BigEndian.Uint64(raw[pos+hashSize:])
		length := int64(binary.BigEndian.Uint32(raw[pos+hashSize+8:]))
		if offset < uint64(HeaderSize) || offset > uint64(indexOffset) || length == 0 || int64(offset)+length > indexOffset {
			return nil, fmt.Errorf("%w: entry %s at %d+%d", ErrInvalid, entry.Hash, offset, length)
		}
		entry.Offset, entry.Length = int64(offset), length
		entries = append(entries, entry)
	}
	return entries, nil
}

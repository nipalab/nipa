// Package chunkbundle is the body format of a bundle upload: many chunks in
// one HTTP request (PUT /api/chunk-bundles/{org}/{project}). A body is a
// sequence of entries, each
//
//	hash (32 bytes, BLAKE3 of the content) | length (uint32, big-endian) | content
//
// with nothing before, between or after them. The client encodes a bundle
// from memory (Encoder); the server decodes it as it arrives (Decoder),
// hashing every entry and enforcing the limits the signed bundle URL carries,
// so a body that breaks any rule is rejected before anything is stored.
package chunkbundle

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

// HeaderSize is the size of one entry header (hash and length).
const HeaderSize = len(domain.Hash{}) + 4

// Entry is one chunk of a bundle.
type Entry struct {
	Hash domain.Hash
	Data []byte
}

// EncodedSize is the body size of a bundle holding chunks of the given
// content sizes.
func EncodedSize(sizes ...int) int64 {
	var n int64
	for _, size := range sizes {
		n += int64(HeaderSize) + int64(size)
	}
	return n
}

// Encoder streams a bundle body over entries held in memory without copying
// their content. It is an io.Reader; Size is the exact body length, to send as
// Content-Length.
type Encoder struct {
	entries []Entry
	size    int64
	index   int
	header  [HeaderSize]byte
	// offset is how far the current entry (header then data) was read.
	offset int
}

// NewEncoder returns a reader over the bundle body of entries. Each entry's
// Hash must be the BLAKE3 hash of its Data and every Data must be non-empty
// and fit in a uint32; the server rejects the bundle otherwise.
func NewEncoder(entries []Entry) *Encoder {
	e := &Encoder{entries: entries}
	for _, entry := range entries {
		e.size += int64(HeaderSize) + int64(len(entry.Data))
	}
	e.fillHeader()
	return e
}

// Size is the exact length of the body.
func (e *Encoder) Size() int64 {
	return e.size
}

func (e *Encoder) fillHeader() {
	if e.index >= len(e.entries) {
		return
	}
	entry := e.entries[e.index]
	copy(e.header[:], entry.Hash[:])
	binary.BigEndian.PutUint32(e.header[len(entry.Hash):], uint32(len(entry.Data)))
}

// Read implements io.Reader.
func (e *Encoder) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) && e.index < len(e.entries) {
		entry := e.entries[e.index]
		if e.offset < HeaderSize {
			c := copy(p[n:], e.header[e.offset:])
			n += c
			e.offset += c
			continue
		}
		dataOffset := e.offset - HeaderSize
		c := copy(p[n:], entry.Data[dataOffset:])
		n += c
		e.offset += c
		if e.offset-HeaderSize == len(entry.Data) {
			e.index++
			e.offset = 0
			e.fillHeader()
		}
	}
	if n == 0 && e.index >= len(e.entries) {
		return 0, io.EOF
	}
	return n, nil
}

// Limits bound what a decoder accepts.
type Limits struct {
	// Size is the exact body length (the request's Content-Length).
	Size int64
	// MaxCount is the most entries the body may hold.
	MaxCount int
	// MaxEntry is the largest entry content; zero means
	// chunker.MaxChunkSize().
	MaxEntry int64
}

// ErrInvalid is wrapped by every decode failure caused by the body itself.
var ErrInvalid = errors.New("invalid chunk bundle")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Decoder reads a bundle body entry by entry. Entry contents are read into
// one arena of Limits.Size bytes allocated up front, so the decoded bundle
// takes exactly the memory the caller reserved for the body, and every entry
// is hashed as it arrives.
type Decoder struct {
	r       io.Reader
	limits  Limits
	arena   []byte
	read    int64
	count   int
	scratch [HeaderSize]byte
	done    bool
}

// NewDecoder starts decoding r under limits. It fails for a size that cannot
// hold a bundle.
func NewDecoder(r io.Reader, limits Limits) (*Decoder, error) {
	if limits.MaxEntry <= 0 {
		limits.MaxEntry = chunker.MaxChunkSize()
	}
	if limits.Size < int64(HeaderSize)+1 {
		return nil, invalid("body of %d bytes holds no entry", limits.Size)
	}
	if limits.MaxCount <= 0 {
		return nil, invalid("no entry allowed")
	}
	return &Decoder{r: r, limits: limits}, nil
}

// Next returns the next entry, or io.EOF once the body ended exactly at
// Limits.Size. Any rule broken (truncation, trailing bytes, a length of zero
// or over MaxEntry, more than MaxCount entries, a hash mismatch) is an error
// wrapping ErrInvalid; a failure of the reader itself is returned as it is.
func (d *Decoder) Next() (Entry, error) {
	if d.done {
		return Entry{}, io.EOF
	}
	if d.read == d.limits.Size {
		return Entry{}, d.finish()
	}
	if d.count >= d.limits.MaxCount {
		return Entry{}, invalid("more than %d entries", d.limits.MaxCount)
	}
	if d.limits.Size-d.read < int64(HeaderSize) {
		return Entry{}, invalid("truncated entry header")
	}
	if err := d.readFull(d.scratch[:]); err != nil {
		return Entry{}, err
	}
	var entry Entry
	copy(entry.Hash[:], d.scratch[:len(entry.Hash)])
	length := int64(binary.BigEndian.Uint32(d.scratch[len(entry.Hash):]))
	if length == 0 || length > d.limits.MaxEntry {
		return Entry{}, invalid("entry %d: length %d out of range (1..%d)", d.count, length, d.limits.MaxEntry)
	}
	if length > d.limits.Size-d.read {
		return Entry{}, invalid("entry %d: length %d past the end of the body", d.count, length)
	}
	if d.arena == nil {
		d.arena = make([]byte, 0, d.limits.Size-int64(HeaderSize))
	}
	start := len(d.arena)
	d.arena = d.arena[:start+int(length)]
	entry.Data = d.arena[start:]
	if err := d.readFull(entry.Data); err != nil {
		return Entry{}, err
	}
	if got := chunker.Sum(entry.Data); got != entry.Hash {
		return Entry{}, invalid("entry %d: content does not hash to %s", d.count, entry.Hash)
	}
	d.count++
	return entry, nil
}

// finish checks that nothing follows the last entry.
func (d *Decoder) finish() error {
	var extra [1]byte
	n, err := io.ReadFull(d.r, extra[:])
	if n > 0 {
		return invalid("body longer than %d bytes", d.limits.Size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	d.done = true
	return io.EOF
}

func (d *Decoder) readFull(p []byte) error {
	n, err := io.ReadFull(d.r, p)
	d.read += int64(n)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return invalid("body shorter than %d bytes", d.limits.Size)
	}
	return err
}

// DecodeAll decodes a whole body and returns its entries, the duplicates of
// an earlier hash dropped (the first wins). Nothing is returned unless every
// entry is valid.
func DecodeAll(r io.Reader, limits Limits) ([]Entry, error) {
	d, err := NewDecoder(r, limits)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	seen := make(map[domain.Hash]struct{})
	for {
		entry, err := d.Next()
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		if _, dup := seen[entry.Hash]; dup {
			continue
		}
		seen[entry.Hash] = struct{}{}
		entries = append(entries, entry)
	}
}

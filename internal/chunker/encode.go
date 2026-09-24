package chunker

import (
	"bytes"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/nipalab/nipa/internal/domain"
)

const (
	EncodingRaw  = "raw"
	EncodingZstd = "zstd"

	TextSingleBlobRawMax = 8 << 20
	TextSingleBlobMax    = 4 << 20
)

// TextLargeConfig chunks text that is too large for a single compressed blob.
var TextLargeConfig = Config{Min: 64 << 10, Avg: 256 << 10, Max: 1 << 20}

// EncodedChunk is one stored object: Hash and SizeBytes refer to the bytes as
// stored (compressed when the file encoding is zstd); RawSize is the plaintext
// size the chunk contributes to the file.
type EncodedChunk struct {
	Hash      domain.Hash
	Data      []byte
	SizeBytes int64
	RawSize   int64
}

var (
	zstdEncoder = mustZstdEncoder()
	zstdDecoder = mustZstdDecoder()
)

func mustZstdEncoder() *zstd.Encoder {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		panic(err)
	}
	return encoder
}

func mustZstdDecoder() *zstd.Decoder {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
	return decoder
}

// Encode converts content into storage chunks. An empty encoding selects the
// default for the content: zstd for text, raw for binary. Callers hashing
// content that is already tracked must pass the tracked encoding so the hashes
// stay stable for files stored before text compression existed.
func Encode(r io.ReadSeeker, path string, isBinary bool, encoding string, fn func(EncodedChunk) error) (string, error) {
	if encoding == "" {
		encoding = defaultEncoding(isBinary)
	}
	if encoding != EncodingZstd || isBinary {
		return EncodingRaw, scanChunks(r, ConfigForFile(path, isBinary), fn)
	}
	return EncodingZstd, encodeText(r, fn)
}

// EncodeBytes is the in-memory equivalent of Encode.
func EncodeBytes(data []byte, path string, isBinary bool, encoding string) (string, []EncodedChunk, error) {
	var chunks []EncodedChunk
	encoding, err := Encode(bytes.NewReader(data), path, isBinary, encoding, func(c EncodedChunk) error {
		chunks = append(chunks, c)
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return encoding, chunks, nil
}

// NormalizeEncoding maps an empty encoding to raw and reports whether the
// value is a known encoding.
func NormalizeEncoding(encoding string) (string, bool) {
	switch encoding {
	case "":
		return EncodingRaw, true
	case EncodingRaw, EncodingZstd:
		return encoding, true
	default:
		return "", false
	}
}

// Decode returns the plaintext bytes of a stored chunk.
func Decode(encoding string, data []byte) ([]byte, error) {
	if encoding != EncodingZstd {
		return data, nil
	}
	decoded, err := zstdDecoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("decode zstd chunk: %w", err)
	}
	return decoded, nil
}

func defaultEncoding(isBinary bool) string {
	if isBinary {
		return EncodingRaw
	}
	return EncodingZstd
}

func scanChunks(r io.Reader, cfg Config, fn func(EncodedChunk) error) error {
	return Scan(r, func(c Chunk) error {
		return fn(EncodedChunk{Hash: c.Hash, Data: c.Data, SizeBytes: int64(len(c.Data)), RawSize: int64(len(c.Data))})
	}, cfg)
}

func encodeText(r io.ReadSeeker, fn func(EncodedChunk) error) error {
	raw, complete, err := readUpTo(r, TextSingleBlobRawMax)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if complete {
		compressed := zstdEncoder.EncodeAll(raw, nil)
		if len(compressed) <= TextSingleBlobMax {
			return fn(EncodedChunk{Hash: Sum(compressed), Data: compressed, SizeBytes: int64(len(compressed)), RawSize: int64(len(raw))})
		}
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return Scan(r, func(c Chunk) error {
		compressed := zstdEncoder.EncodeAll(c.Data, nil)
		return fn(EncodedChunk{Hash: Sum(compressed), Data: compressed, SizeBytes: int64(len(compressed)), RawSize: int64(len(c.Data))})
	}, TextLargeConfig)
}

// readUpTo reads up to limit bytes and reports whether the content ended.
func readUpTo(r io.Reader, limit int64) ([]byte, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(raw)) > limit {
		return raw[:limit], false, nil
	}
	return raw, true, nil
}

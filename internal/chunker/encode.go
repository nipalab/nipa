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

	// MaxDecodedChunkSize caps the plaintext one stored zstd chunk may decode
	// to. encodeText emits at most TextSingleBlobRawMax bytes of plaintext in a
	// single blob and TextLargeConfig.Max (1 MiB) per chunk otherwise, so no
	// legitimately encoded chunk exceeds TextSingleBlobRawMax; the extra 1 MiB
	// is headroom, not a format allowance. Anything larger is a decompression
	// bomb and fails before its output is allocated.
	MaxDecodedChunkSize = TextSingleBlobRawMax + 1<<20
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
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(MaxDecodedChunkSize))
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

// Decode returns the plaintext bytes of one stored chunk. A zstd chunk that
// decodes to more than MaxDecodedChunkSize is rejected, so callers decode
// chunk by chunk rather than a whole file at once.
func Decode(encoding string, data []byte) ([]byte, error) {
	if encoding != EncodingZstd {
		return data, nil
	}
	return DecodeAppend(nil, encoding, data)
}

// DecodeAppend appends the plaintext bytes of one stored chunk to dst. The
// MaxDecodedChunkSize cap applies to the bytes this call appends, not to dst.
func DecodeAppend(dst []byte, encoding string, data []byte) ([]byte, error) {
	if encoding != EncodingZstd {
		return append(dst, data...), nil
	}
	// Decode into the spare capacity after dst: the decoder counts dst's
	// length against MaxDecodedChunkSize, so it must start from an empty slice.
	tail := dst[len(dst):]
	decoded, err := zstdDecoder.DecodeAll(data, tail)
	if err != nil {
		return nil, fmt.Errorf("decode zstd chunk: %w", err)
	}
	if len(dst) == 0 {
		return decoded, nil
	}
	if len(decoded) == 0 {
		return dst, nil
	}
	if cap(tail) > 0 && &tail[:1][0] == &decoded[0] {
		return dst[:len(dst)+len(decoded)], nil
	}
	return append(dst, decoded...), nil
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

package provenance

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var errCBOR = errors.New("invalid CBOR")

type cborReader struct {
	data     []byte
	pos      int
	maxDepth int
	items    int
}

const maxCBORItems = 1 << 16

// decodeCBOR decodes one definite-length CBOR item into nil, bool, uint64,
// int64, float64, string, []byte, []any or map[string]any. Map keys that are
// not text are dropped; tags are unwrapped.
func decodeCBOR(data []byte, maxDepth int) (any, error) {
	r := &cborReader{data: data, maxDepth: maxDepth}
	return r.item(0)
}

func (r *cborReader) head() (major byte, arg uint64, err error) {
	if r.pos >= len(r.data) {
		return 0, 0, fmt.Errorf("%w: truncated", errCBOR)
	}
	b := r.data[r.pos]
	r.pos++
	major, info := b>>5, b&0x1f
	var n int
	switch {
	case info < 24:
		return major, uint64(info), nil
	case info == 24:
		n = 1
	case info == 25:
		n = 2
	case info == 26:
		n = 4
	case info == 27:
		n = 8
	default:
		return 0, 0, fmt.Errorf("%w: unsupported additional info %d", errCBOR, info)
	}
	if len(r.data)-r.pos < n {
		return 0, 0, fmt.Errorf("%w: truncated", errCBOR)
	}
	var buf [8]byte
	copy(buf[8-n:], r.data[r.pos:r.pos+n])
	r.pos += n
	return major, binary.BigEndian.Uint64(buf[:]), nil
}

func (r *cborReader) bytes(n uint64) ([]byte, error) {
	if n > uint64(len(r.data)-r.pos) {
		return nil, fmt.Errorf("%w: truncated", errCBOR)
	}
	out := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *cborReader) count(n uint64) (int, error) {
	if n > uint64(len(r.data)-r.pos) {
		return 0, fmt.Errorf("%w: length exceeds input", errCBOR)
	}
	return int(n), nil
}

func (r *cborReader) item(depth int) (any, error) {
	if depth > r.maxDepth {
		return nil, fmt.Errorf("%w: nesting deeper than %d", errCBOR, r.maxDepth)
	}
	if r.items++; r.items > maxCBORItems {
		return nil, fmt.Errorf("%w: too many items", errCBOR)
	}
	start := r.pos
	major, arg, err := r.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		return arg, nil
	case 1:
		if arg > math.MaxInt64 {
			return nil, fmt.Errorf("%w: negative integer overflow", errCBOR)
		}
		return -1 - int64(arg), nil
	case 2:
		b, err := r.bytes(arg)
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), b...), nil
	case 3:
		b, err := r.bytes(arg)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case 4:
		n, err := r.count(arg)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, min(n, 64))
		for range n {
			v, err := r.item(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 5:
		n, err := r.count(arg)
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, min(n, 64))
		for range n {
			k, err := r.item(depth + 1)
			if err != nil {
				return nil, err
			}
			v, err := r.item(depth + 1)
			if err != nil {
				return nil, err
			}
			if ks, ok := k.(string); ok {
				out[ks] = v
			}
		}
		return out, nil
	case 6:
		return r.item(depth + 1)
	default:
		return r.simple(r.data[start]&0x1f, arg)
	}
}

func (r *cborReader) simple(info byte, arg uint64) (any, error) {
	switch info {
	case 20:
		return false, nil
	case 21:
		return true, nil
	case 22, 23:
		return nil, nil
	case 25:
		return float64(halfToFloat(uint16(arg))), nil
	case 26:
		return float64(math.Float32frombits(uint32(arg))), nil
	case 27:
		return math.Float64frombits(arg), nil
	}
	return nil, fmt.Errorf("%w: unsupported simple value", errCBOR)
}

func halfToFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h & 0x3ff)
	switch exp {
	case 0:
		v := float32(frac) / 1024 * float32(math.Pow(2, -14))
		if sign != 0 {
			return -v
		}
		return v
	case 0x1f:
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}

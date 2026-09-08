package chunker

import (
	"bytes"
	"errors"
	"io"

	"github.com/zeebo/blake3"

	"github.com/nipalab/nipa/internal/domain"
)

const binaryProbeSize = 8000

var gear = newGearTable()

type Config struct {
	Min int64
	Avg int64
	Max int64
}

var DefaultConfig = Config{
	Min: 16 << 10,
	Avg: 64 << 10,
	Max: 1 << 18,
}

func (c Config) sanitized() (Config, error) {
	if c.Avg == 0 {
		c = DefaultConfig
	}
	if c.Min == 0 {
		c.Min = c.Avg / 4
	}
	if c.Max == 0 {
		c.Max = c.Avg * 4
	}
	switch {
	case c.Min <= 0 || c.Avg <= 0 || c.Max <= 0:
		return Config{}, errors.New("chunker: chunk sizes must be positive")
	case c.Min >= c.Avg || c.Avg >= c.Max:
		return Config{}, errors.New("chunker: requires min < avg < max")
	case c.Avg&(c.Avg-1) != 0:
		return Config{}, errors.New("chunker: avg must be a power of two")
	case c.Min < 16:
		return Config{}, errors.New("chunker: min size too small")
	}
	return c, nil
}

type Chunk struct {
	Hash domain.Hash
	Data []byte
}

func Sum(data []byte) domain.Hash {
	var out domain.Hash
	h := blake3.New()
	_, _ = h.Write(data)
	_, _ = h.Digest().Read(out[:])
	return out
}

func FileHash(chunks []domain.Hash) domain.Hash {
	h := blake3.New()
	for _, c := range chunks {
		_, _ = h.Write(c[:])
	}
	var out domain.Hash
	_, _ = h.Digest().Read(out[:])
	return out
}

func IsBinary(data []byte) bool {
	if len(data) > binaryProbeSize {
		data = data[:binaryProbeSize]
	}
	return bytes.IndexByte(data, 0) >= 0
}

func ChunkAll(data []byte) ([]Chunk, error) {
	return ChunkAllWith(DefaultConfig, data)
}

func ChunkAllWith(cfg Config, data []byte) ([]Chunk, error) {
	s, err := NewSplitter(bytes.NewReader(data), cfg)
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, 8)
	for {
		c, err := s.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
}

type Splitter struct {
	r   io.Reader
	cfg Config
	buf []byte
	err error
}

func NewSplitter(r io.Reader, cfgs ...Config) (*Splitter, error) {
	cfg := DefaultConfig
	if len(cfgs) > 0 {
		cfg = cfgs[0]
	}
	s, err := cfg.sanitized()
	if err != nil {
		return nil, err
	}
	return &Splitter{
		r:   r,
		cfg: s,
		buf: make([]byte, 0, s.Max),
	}, nil
}

func (s *Splitter) Next() (*Chunk, error) {
	if len(s.buf) == 0 && s.err != nil {
		return nil, s.err
	}
	_ = s.fill()
	if len(s.buf) == 0 {
		return nil, s.err
	}
	cut := s.cut()
	hash := Sum(s.buf[:cut])
	data := make([]byte, cut)
	copy(data, s.buf[:cut])
	tail := s.buf[cut:]
	copy(s.buf, tail)
	s.buf = s.buf[:len(tail)]
	return &Chunk{Hash: hash, Data: data}, nil
}

func (s *Splitter) fill() error {
	if len(s.buf) == cap(s.buf) {
		return nil
	}
	for len(s.buf) < cap(s.buf) {
		n, err := s.r.Read(s.buf[len(s.buf):cap(s.buf)])
		if n > 0 {
			s.buf = s.buf[:len(s.buf)+n]
		}
		if err != nil {
			s.err = err
			return nil
		}
		if n == 0 {
			s.err = io.EOF
			return nil
		}
	}
	return nil
}

func (s *Splitter) cut() int {
	data := s.buf
	n := len(data)
	min := int(s.cfg.Min)
	if n <= min {
		return n
	}
	avg := int(s.cfg.Avg)
	hard := int(s.cfg.Max)
	if avg > n {
		avg = n
	}
	if hard > n {
		hard = n
	}
	maskS := uint64(s.cfg.Avg - 1)
	maskL := (uint64(s.cfg.Avg) >> 1) - 1
	fp := uint64(0)
	for i := 0; i < min; i++ {
		fp = (fp << 1) + gear[data[i]]
	}
	for i := min; i < avg; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskS == 0 {
			return i + 1
		}
	}
	for i := avg; i < hard; i++ {
		fp = (fp << 1) + gear[data[i]]
		if fp&maskL == 0 {
			return i + 1
		}
	}
	return hard
}

type gearTable [256]uint64

func newGearTable() gearTable {
	var t gearTable
	seed := uint64(0x4d595df4d0f33173)
	for i := range t {
		t[i] = splitMix64(&seed)
	}
	return t
}

func splitMix64(seed *uint64) uint64 {
	*seed += 0x9e3779b97f4a7c15
	z := *seed
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

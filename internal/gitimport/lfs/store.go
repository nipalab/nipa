package lfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

type Object interface {
	io.ReadSeeker
	io.Closer
}

type LocalStore struct {
	Dir string
}

func NewLocalStore(gitDir string) *LocalStore {
	return &LocalStore{Dir: filepath.Join(gitDir, "lfs", "objects")}
}

func (s *LocalStore) Path(oid string) string {
	return filepath.Join(s.Dir, oid[0:2], oid[2:4], oid)
}

func (s *LocalStore) Has(p Pointer) bool {
	if !ValidOID(p.OID) {
		return false
	}
	info, err := os.Stat(s.Path(p.OID))
	return err == nil && info.Mode().IsRegular() && info.Size() == p.Size
}

func (s *LocalStore) Open(p Pointer) (Object, error) {
	if !s.Has(p) {
		return nil, os.ErrNotExist
	}
	return os.Open(s.Path(p.OID))
}

var ErrCorrupt = errors.New("lfs object does not match its pointer")

type Verifier struct {
	r   io.ReadSeeker
	p   Pointer
	h   hash.Hash
	n   int64
	eof bool
}

func NewVerifier(r io.ReadSeeker, p Pointer) *Verifier {
	return &Verifier{r: r, p: p, h: sha256.New()}
}

func (v *Verifier) Read(b []byte) (int, error) {
	n, err := v.r.Read(b)
	v.h.Write(b[:n])
	v.n += int64(n)
	if errors.Is(err, io.EOF) {
		v.eof = true
	}
	return n, err
}

func (v *Verifier) Seek(offset int64, whence int) (int64, error) {
	pos, err := v.r.Seek(offset, whence)
	if err != nil {
		return pos, err
	}
	if pos != 0 {
		return pos, errors.New("lfs object reader only rewinds to the start")
	}
	v.h.Reset()
	v.n, v.eof = 0, false
	return pos, nil
}

func (v *Verifier) Verify() error {
	if !v.eof {
		var buf [1]byte
		n, err := v.Read(buf[:])
		if n > 0 || (err != nil && !errors.Is(err, io.EOF)) {
			return fmt.Errorf("%w: object %s is larger than %d bytes", ErrCorrupt, v.p.OID, v.p.Size)
		}
	}
	return check(v.p, v.n, v.h)
}

func check(p Pointer, n int64, h hash.Hash) error {
	if n != p.Size {
		return fmt.Errorf("%w: object %s has %d bytes, the pointer says %d", ErrCorrupt, p.OID, n, p.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != p.OID {
		return fmt.Errorf("%w: object %s hashes to %s", ErrCorrupt, p.OID, got)
	}
	return nil
}

type Mode string

const (
	ModeAuto  Mode = "auto"
	ModeLocal Mode = "local"
	ModeOff   Mode = "off"
)

func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeLocal, ModeOff:
		return Mode(s), nil
	}
	return "", fmt.Errorf("invalid lfs mode %q: want auto, local or off", s)
}

type Fetcher interface {
	Fetch(ctx context.Context, p Pointer) (Object, error)
}

type MissingError struct {
	OID    string
	Mode   Mode
	Remote bool
}

func (e *MissingError) Error() string {
	switch {
	case e.Mode == ModeLocal:
		return fmt.Sprintf("lfs object %s is not in the local lfs store", e.OID)
	case !e.Remote:
		return fmt.Sprintf("lfs object %s is not in the local lfs store and no lfs server is configured", e.OID)
	}
	return fmt.Sprintf("lfs object %s is missing", e.OID)
}

type Resolver struct {
	Mode   Mode
	Local  []*LocalStore
	Remote Fetcher
}

func (r *Resolver) Has(p Pointer) bool {
	for _, s := range r.Local {
		if s.Has(p) {
			return true
		}
	}
	return false
}

func (r *Resolver) CanFetch() bool {
	return r.Mode == ModeAuto && r.Remote != nil
}

func (r *Resolver) Open(ctx context.Context, p Pointer) (Object, error) {
	for _, s := range r.Local {
		if obj, err := s.Open(p); err == nil {
			return obj, nil
		}
	}
	if r.CanFetch() {
		return r.Remote.Fetch(ctx, p)
	}
	return nil, &MissingError{OID: p.OID, Mode: r.Mode, Remote: r.Remote != nil}
}

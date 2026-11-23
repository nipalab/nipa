// Package chunkproof holds what the server uses to trust chunk bytes at
// confirm: the streaming verifier that hashes stored bytes read back from a
// store, and the signed receipts a bundle upload returns (receipt.go). It
// also owns the chunk transfer protocol version both sides send.
//
// Dedup is per project: a project only ever references chunks uploaded into
// its own scope, so there is no possession proof or challenge.
package chunkproof

import (
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

// Protocol is the chunk transfer protocol version (chunk_protocol on the
// GetChunkUploadUrls, GetChunkDownloadUrls and ConfirmChunkUploads requests
// and responses). Version 2 is per-project scope without possession proofs;
// version 3 uploads through the server in bundles (one signed bundle URL per
// presign, receipts v2 with a location) instead of one PUT per chunk. Client
// and server must speak the same version, an older peer is refused with an
// upgrade hint.
const Protocol uint32 = 3

// Verifier checks streamed chunk bytes in one pass: it computes the chunk
// hash (equal to chunker.Sum) and counts the bytes, so the server never holds
// a whole chunk to verify it.
type Verifier struct {
	plain *chunker.Hasher
	size  int64
}

// NewVerifier starts a verifier.
func NewVerifier() *Verifier {
	return &Verifier{plain: chunker.NewHasher()}
}

// Write feeds chunk bytes to the hash. It never fails.
func (v *Verifier) Write(p []byte) (int, error) {
	_, _ = v.plain.Write(p)
	v.size += int64(len(p))
	return len(p), nil
}

// Size is the number of bytes written.
func (v *Verifier) Size() int64 {
	return v.size
}

// Sum is the chunk hash of the bytes written, equal to chunker.Sum of them.
func (v *Verifier) Sum() domain.Hash {
	return v.plain.Sum()
}

package domain

import (
	"context"
	"io"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// ChunkScope scopes chunk transfer authorization: the project, plus for
// downloads the commits and path prefixes whose visible chunks may be
// requested.
type ChunkScope struct {
	Org       string
	Project   string
	CommitIDs []string
	Paths     []string
}

// ChunkStream lets a download hand selected chunks to the caller as a stream
// instead of a buffered byte slice, so large raw chunks never sit in memory.
// Wants picks the chunks to stream. Write must consume body, verify what it
// read and place it; it runs concurrently for different chunks and is called
// again for the same chunk when the transfer failed part way, so it must be
// safe to repeat.
type ChunkStream struct {
	Wants func(h serverDomain.Hash) bool
	Write func(h serverDomain.Hash, body io.Reader) error
}

type verifyObserverKey struct{}

// WithVerifyObserver returns a context whose chunk uploads call fn with the
// number of objects the server is about to verify. Verification reads every
// object back from the chunk store, so it can dominate small pushes.
func WithVerifyObserver(ctx context.Context, fn func(objects int)) context.Context {
	return context.WithValue(ctx, verifyObserverKey{}, fn)
}

// NotifyVerify reports a verification round to the observer in ctx, if any.
func NotifyVerify(ctx context.Context, objects int) {
	if fn, ok := ctx.Value(verifyObserverKey{}).(func(objects int)); ok && fn != nil {
		fn(objects)
	}
}

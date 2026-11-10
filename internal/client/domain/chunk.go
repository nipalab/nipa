package domain

import "context"

// ChunkScope scopes chunk transfer authorization: the project, plus for
// downloads the commits and path prefixes whose visible chunks may be
// requested.
type ChunkScope struct {
	Org       string
	Project   string
	CommitIDs []string
	Paths     []string
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

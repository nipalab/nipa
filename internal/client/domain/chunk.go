package domain

// ChunkScope scopes chunk transfer authorization: the project, plus for
// downloads the commits and path prefixes whose visible chunks may be
// requested.
type ChunkScope struct {
	Org       string
	Project   string
	CommitIDs []string
	Paths     []string
}

package domain

// DiskUsage is what a clone occupies locally: the tracked working files
// (snapshot sizes, never a walk) and the loose object cache.
type DiskUsage struct {
	WorkingFiles int64
	WorkingBytes int64
	CacheObjects int64
	CacheBytes   int64
	Pins         []string
}

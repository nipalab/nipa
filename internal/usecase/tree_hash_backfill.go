package usecase

import (
	"container/heap"
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/treehash"
)

// TreeHashNode is a tree_nodes row as the tree hash backfill sees it. Hash is
// the stored hash; HasContent reports whether the row owns files or child
// rows (a content node) rather than being a reference to an unchanged
// directory, which manifests resolve through the oldest content node with the
// same hash.
type TreeHashNode struct {
	ID         int64
	ParentID   *int64
	Name       string
	Hash       domain.Hash
	HasContent bool
}

// TreeHashRepository stores what the tree hash backfill reads and writes. All
// methods called from inside WithTreeHashBackfillLock run in its transaction.
type TreeHashRepository interface {
	// WithTreeHashBackfillLock runs fn in one transaction holding the
	// backfill marker row locked, passing the stored watermark (the highest
	// tree node id already covered) and storing the one fn returns.
	WithTreeHashBackfillLock(ctx context.Context, fn func(ctx context.Context, watermark int64) (int64, error)) error
	MaxTreeNodeID(ctx context.Context) (int64, error)
	// TreeHashSeeds lists the ids in (afterID, upToID] of tree nodes that own
	// a file whose stored mode is not plain read-write (0 or 0o644).
	TreeHashSeeds(ctx context.Context, afterID, upToID int64) ([]int64, error)
	TreeHashNode(ctx context.Context, id int64) (*TreeHashNode, error)
	// TreeHashFiles lists the file rows owned by the node itself (no hash
	// resolution).
	TreeHashFiles(ctx context.Context, treeID int64) ([]treehash.FileEntry, error)
	// TreeHashChildren lists the child rows owned by the node itself.
	TreeHashChildren(ctx context.Context, treeID int64) ([]TreeHashNode, error)
	// TreeHashNodesByHash lists every node stored with hash, by id.
	TreeHashNodesByHash(ctx context.Context, hash domain.Hash) ([]TreeHashNode, error)
	SetTreeNodeHash(ctx context.Context, id int64, hash domain.Hash) error
	// SaveLegacyTreeHash records the hash a root tree had before the backfill
	// changed it; an existing record is kept.
	SaveLegacyTreeHash(ctx context.Context, treeID int64, hash domain.Hash) error
	// LegacyTreeHash returns the recorded pre-backfill hash of a root tree,
	// or nil when it has none.
	LegacyTreeHash(ctx context.Context, treeID int64) (*domain.Hash, error)
}

// TreeHashBackfillResult reports one backfill pass.
type TreeHashBackfillResult struct {
	FromID     int64
	UpToID     int64
	Seeds      int
	Recomputed int
	Updated    int
}

// BackfillTreeHashes recomputes the stored hashes of tree nodes whose hash
// changed when file modes became part of the tree hash (treehash.TreeHash), and
// of everything that depends on them. A read-write file hashes as before, so
// only nodes owning an executable or read-only file can change; from those the
// change propagates to parents and to the reference rows that resolved to a
// changed content node. Reads see the original hashes (every write is held
// back until the pass is done), so a reference keeps the content it resolved
// to before: it takes the new hash of the oldest content node sharing its old
// hash. Only rows whose hash changes are written, plus the old hash of every
// changed root for clients that still push against it.
//
// The pass covers the nodes added since the stored watermark, so it is a full
// scan once and cheap afterwards, and rerunning it changes nothing.
func BackfillTreeHashes(ctx context.Context, repo TreeHashRepository) (TreeHashBackfillResult, error) {
	var result TreeHashBackfillResult
	err := repo.WithTreeHashBackfillLock(ctx, func(ctx context.Context, watermark int64) (int64, error) {
		result = TreeHashBackfillResult{FromID: watermark, UpToID: watermark}
		upTo, err := repo.MaxTreeNodeID(ctx)
		if err != nil {
			return 0, err
		}
		if upTo <= watermark {
			return watermark, nil
		}
		result.UpToID = upTo
		seeds, err := repo.TreeHashSeeds(ctx, watermark, upTo)
		if err != nil {
			return 0, err
		}
		result.Seeds = len(seeds)
		b := newTreeHashBackfill(repo)
		if err := b.run(ctx, seeds); err != nil {
			return 0, err
		}
		result.Recomputed = b.recomputed
		updated, err := b.write(ctx)
		if err != nil {
			return 0, err
		}
		result.Updated = updated
		return upTo, nil
	})
	return result, err
}

type treeHashBackfill struct {
	repo TreeHashRepository
	// nodes caches loaded rows with their original hash.
	nodes map[int64]*TreeHashNode
	// next holds the new hash of every node the pass changed.
	next map[int64]domain.Hash
	// byHash caches TreeHashNodesByHash by original hash.
	byHash     map[domain.Hash][]TreeHashNode
	queue      idHeap
	queued     map[int64]bool
	recomputed int
}

func newTreeHashBackfill(repo TreeHashRepository) *treeHashBackfill {
	return &treeHashBackfill{
		repo:   repo,
		nodes:  map[int64]*TreeHashNode{},
		next:   map[int64]domain.Hash{},
		byHash: map[domain.Hash][]TreeHashNode{},
		queued: map[int64]bool{},
	}
}

func (b *treeHashBackfill) push(id int64) {
	if b.queued[id] {
		return
	}
	b.queued[id] = true
	heap.Push(&b.queue, id)
}

func (b *treeHashBackfill) hashOf(id int64, original domain.Hash) domain.Hash {
	if h, ok := b.next[id]; ok {
		return h
	}
	return original
}

func (b *treeHashBackfill) node(ctx context.Context, id int64) (*TreeHashNode, error) {
	if n, ok := b.nodes[id]; ok {
		return n, nil
	}
	n, err := b.repo.TreeHashNode(ctx, id)
	if err != nil {
		return nil, err
	}
	b.nodes[id] = n
	return n, nil
}

// run recomputes queued content nodes until no hash changes any more. Nodes
// are taken highest id first, which within a push is children before parents;
// a node whose input changes after it was computed is simply queued again.
func (b *treeHashBackfill) run(ctx context.Context, seeds []int64) error {
	for _, id := range seeds {
		b.push(id)
	}
	for b.queue.Len() > 0 {
		id := heap.Pop(&b.queue).(int64)
		delete(b.queued, id)
		if err := b.recompute(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (b *treeHashBackfill) recompute(ctx context.Context, id int64) error {
	node, err := b.node(ctx, id)
	if err != nil {
		return err
	}
	files, err := b.repo.TreeHashFiles(ctx, id)
	if err != nil {
		return err
	}
	children, err := b.repo.TreeHashChildren(ctx, id)
	if err != nil {
		return err
	}
	trees := make([]treehash.TreeEntry, 0, len(children))
	for _, child := range children {
		if _, ok := b.nodes[child.ID]; !ok {
			c := child
			b.nodes[child.ID] = &c
		}
		trees = append(trees, treehash.TreeEntry{Name: child.Name, Hash: b.hashOf(child.ID, child.Hash)})
	}
	b.recomputed++
	hash := treehash.TreeHash(files, trees)
	if hash == b.hashOf(id, node.Hash) {
		return nil
	}
	b.next[id] = hash
	if node.ParentID != nil {
		b.push(*node.ParentID)
	}
	return b.moveReferences(ctx, node, hash)
}

// moveReferences gives the reference rows that resolved to node (the oldest
// content node with its original hash) the node's new hash, and queues their
// parents.
func (b *treeHashBackfill) moveReferences(ctx context.Context, node *TreeHashNode, hash domain.Hash) error {
	group, ok := b.byHash[node.Hash]
	if !ok {
		var err error
		if group, err = b.repo.TreeHashNodesByHash(ctx, node.Hash); err != nil {
			return err
		}
		b.byHash[node.Hash] = group
	}
	resolved := false
	for _, n := range group {
		if n.HasContent {
			resolved = n.ID == node.ID
			break
		}
	}
	if !resolved {
		return nil
	}
	for _, ref := range group {
		if ref.HasContent {
			continue
		}
		if _, ok := b.nodes[ref.ID]; !ok {
			r := ref
			b.nodes[ref.ID] = &r
		}
		if b.hashOf(ref.ID, ref.Hash) == hash {
			continue
		}
		b.next[ref.ID] = hash
		if ref.ParentID != nil {
			b.push(*ref.ParentID)
		}
	}
	return nil
}

// write stores every changed hash and the old hash of every changed root.
func (b *treeHashBackfill) write(ctx context.Context) (int, error) {
	updated := 0
	for id, hash := range b.next {
		node := b.nodes[id]
		if node == nil || node.Hash == hash {
			continue
		}
		if err := b.repo.SetTreeNodeHash(ctx, id, hash); err != nil {
			return 0, err
		}
		if node.ParentID == nil {
			if err := b.repo.SaveLegacyTreeHash(ctx, id, node.Hash); err != nil {
				return 0, err
			}
		}
		updated++
	}
	return updated, nil
}

// idHeap is a max-heap of tree node ids.
type idHeap []int64

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] > h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *idHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

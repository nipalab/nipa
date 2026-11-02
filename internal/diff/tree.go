package diff

import serverDomain "github.com/nipalab/nipa/internal/domain"

// TreeDiff compares two recursive tree manifests: it flattens both trees,
// compares them, detects renames and attaches text contents through loadChunk.
// Binary files are left without content; the patch renders them as a one-line
// "Binary files ... differ". This is the shared entry point for server-side
// merge-request diffs and for callers whose chunks are already local.
func TreeDiff(base, head *serverDomain.TreeNode, loadChunk func(serverDomain.Hash) ([]byte, error)) []FileDiff {
	return TreeDiffEntries(FromTree(base), FromTree(head), loadChunk)
}

// TreeDiffEntries is TreeDiff over already flattened sides. Callers may omit
// files that are identical on both sides; the result is the same as with the
// full maps.
func TreeDiffEntries(baseMap, headMap map[string]Entry, loadChunk func(serverDomain.Hash) ([]byte, error)) []FileDiff {
	changes := Compare(baseMap, headMap)
	loader := treeLoader(loadChunk)
	changes = DetectRenames(changes, loader)
	return AttachContents(changes, func(e Entry, _ bool) ([]byte, bool) {
		return loader(e)
	})
}

func treeLoader(loadChunk func(serverDomain.Hash) ([]byte, error)) func(Entry) ([]byte, bool) {
	if loadChunk == nil {
		return func(Entry) ([]byte, bool) { return nil, false }
	}
	return func(e Entry) ([]byte, bool) {
		if e.IsBinary {
			return nil, true
		}
		content, err := LoadContent(loadChunk, e)
		if err != nil {
			return nil, false
		}
		return content, true
	}
}

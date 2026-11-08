package unitymeta

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChangesFromStatus(t *testing.T) {
	changes, unity := ChangesFromStatus(StatusPaths{
		Staged:    []string{"Assets/a.png", "Assets/b.png"},
		Added:     []string{"Assets/a.png"},
		Deleted:   []string{"Assets/d.png"},
		Modified:  []string{"x/Assets/m.png"},
		Missing:   []string{"Assets/g.png"},
		Untracked: []string{"Assets/u.png"},
	})
	assert.True(t, unity)
	assert.Equal(t, []Change{
		{Path: "Assets/a.png", Status: "added", Staged: true},
		{Path: "Assets/b.png", Status: "modified", Staged: true},
		{Path: "Assets/d.png", Status: "deleted", Staged: true},
		{Path: "x/Assets/m.png", Status: "modified"},
		{Path: "Assets/g.png", Status: "deleted"},
		{Path: "Assets/u.png", Status: "untracked"},
	}, changes)

	changes, unity = ChangesFromStatus(StatusPaths{Modified: []string{"src/main.go"}})
	assert.False(t, unity)
	assert.Len(t, changes, 1)

	changes, unity = ChangesFromStatus(StatusPaths{})
	assert.False(t, unity)
	assert.Empty(t, changes)
}

package unitymeta

import "strings"

// StatusPaths is a working-copy status as plain path lists. Added lists the
// staged paths the snapshot does not track.
type StatusPaths struct {
	Staged, Added, Deleted, Modified, Missing, Untracked []string
}

// ChangesFromStatus turns a status into lint changes and reports whether any
// path sits inside a Unity Assets folder.
func ChangesFromStatus(st StatusPaths) ([]Change, bool) {
	added := map[string]bool{}
	for _, p := range st.Added {
		added[p] = true
	}
	var changes []Change
	unity := false
	add := func(paths []string, staged bool, statusOf func(string) string) {
		for _, p := range paths {
			changes = append(changes, Change{Path: p, Status: statusOf(p), Staged: staged})
			unity = unity || strings.HasPrefix(p, "Assets/") || strings.Contains(p, "/Assets/")
		}
	}
	constant := func(v string) func(string) string { return func(string) string { return v } }
	add(st.Staged, true, func(p string) string {
		if added[p] {
			return "added"
		}
		return "modified"
	})
	add(st.Deleted, true, constant("deleted"))
	add(st.Modified, false, constant("modified"))
	add(st.Missing, false, constant("deleted"))
	add(st.Untracked, false, constant("untracked"))
	return changes, unity
}

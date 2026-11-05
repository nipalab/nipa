package cli

import "os"

// Windows consoles have no resize signal; the pager keeps its size until the
// next key press redraws it.
func notifyResize(chan<- os.Signal) {}

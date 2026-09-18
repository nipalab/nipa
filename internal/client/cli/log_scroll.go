package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type logKey int

const (
	keyNone logKey = iota
	keyUp
	keyDown
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
	keyQuit
)

type logViewport struct {
	lines  []string
	top    int
	height int
}

func newLogViewport(lines []string, height int) *logViewport {
	v := &logViewport{lines: lines, height: height}
	v.clamp()
	return v
}

func (v *logViewport) setHeight(h int) {
	v.height = h
	v.clamp()
}

func (v *logViewport) total() int {
	return len(v.lines)
}

func (v *logViewport) lastTop() int {
	n := v.total() - v.height
	if n < 0 {
		return 0
	}
	return n
}

func (v *logViewport) clamp() {
	if v.top < 0 {
		v.top = 0
	}
	if lt := v.lastTop(); v.top > lt {
		v.top = lt
	}
}

func (v *logViewport) handleKey(k logKey) bool {
	switch k {
	case keyDown:
		if v.top < v.lastTop() {
			v.top++
		}
	case keyUp:
		if v.top > 0 {
			v.top--
		}
	case keyPageDown:
		v.top += v.height
	case keyPageUp:
		v.top -= v.height
	case keyHome:
		v.top = 0
	case keyEnd:
		v.top = v.lastTop()
	case keyQuit:
		return true
	}
	v.clamp()
	return false
}

func (v *logViewport) visibleLines() []string {
	end := v.top + v.height
	if end > v.total() {
		end = v.total()
	}
	if v.top >= end {
		return nil
	}
	return v.lines[v.top:end]
}

func readKeyFrom(in io.Reader) (logKey, error) {
	buf := make([]byte, 8)
	n, err := in.Read(buf)
	if err != nil {
		return keyNone, err
	}
	if n == 0 {
		return keyNone, io.ErrUnexpectedEOF
	}
	switch b := buf[0]; b {
	case 'q', 0x03: // q, Ctrl-C
		return keyQuit, nil
	case 'k', 0x10: // k, Ctrl-P
		return keyUp, nil
	case 'j', 0x0e: // j, Ctrl-N
		return keyDown, nil
	case ' ', 'f', 0x06: // space, f, Ctrl-F
		return keyPageDown, nil
	case 'b', 0x02: // b, Ctrl-B
		return keyPageUp, nil
	case 'g':
		return keyHome, nil
	case 'G':
		return keyEnd, nil
	case 0x1b: // ESC sequences
		return parseEscapeSequence(buf[1:n]), nil
	}
	return keyNone, nil
}

func parseEscapeSequence(rest []byte) logKey {
	if len(rest) >= 2 && rest[0] == '[' {
		switch rest[1] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		case 'H':
			return keyHome
		case 'F':
			return keyEnd
		case '5':
			return keyPageUp
		case '6':
			return keyPageDown
		case '1', '7':
			return keyHome
		case '4', '8':
			return keyEnd
		}
	}
	if len(rest) >= 2 && rest[0] == 'O' {
		switch rest[1] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		case 'H':
			return keyHome
		case 'F':
			return keyEnd
		}
	}
	return keyQuit
}

func shortHash(h serverDomain.Hash) string {
	return h.String()[:12]
}

func authorLine(e *serverDomain.CommitLogEntry) string {
	name := e.AuthorName
	if name == "" {
		name = "unknown"
	}
	if e.AuthorEmail != "" {
		return fmt.Sprintf("%s <%s>", name, e.AuthorEmail)
	}
	return name
}

func commitSubject(e *serverDomain.CommitLogEntry) string {
	if i := strings.IndexByte(e.Message, '\n'); i >= 0 {
		return e.Message[:i]
	}
	return e.Message
}

func formatCommitFull(e *serverDomain.CommitLogEntry) []string {
	lines := []string{
		fmt.Sprintf("commit %s", shortHash(e.Hash)),
		fmt.Sprintf("Author: %s", authorLine(e)),
		fmt.Sprintf("Date:   %s", e.CreatedAt.Format("Mon Jan 2 15:04:05 2006 -0700")),
		"",
	}
	if e.Message != "" {
		for _, m := range strings.Split(e.Message, "\n") {
			lines = append(lines, "    "+m)
		}
	}
	lines = append(lines, "")
	return lines
}

func formatCommitOneline(e *serverDomain.CommitLogEntry) string {
	return fmt.Sprintf("%s %s", shortHash(e.Hash), commitSubject(e))
}

func logLines(entries []*serverDomain.CommitLogEntry, oneline bool) []string {
	var lines []string
	for _, e := range entries {
		if e == nil {
			continue
		}
		if oneline {
			lines = append(lines, formatCommitOneline(e))
		} else {
			lines = append(lines, formatCommitFull(e)...)
		}
	}
	return lines
}

func printLogPlain(out io.Writer, entries []*serverDomain.CommitLogEntry, oneline bool) error {
	for _, line := range logLines(entries, oneline) {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func heightRows(total int) int {
	if total <= 1 {
		return 1
	}
	return total - 1
}

func runLogPager(out io.Writer, in io.Reader, entries []*serverDomain.CommitLogEntry, oneline bool, getSize func() (int, int), sigs <-chan os.Signal) error {
	return runViewportPager(out, in, logLines(entries, oneline), logFooter, getSize, sigs)
}

func runViewportPager(out io.Writer, in io.Reader, lines []string, footer func(*logViewport) string, getSize func() (int, int), sigs <-chan os.Signal) error {
	_, height := getSize()
	vp := newLogViewport(lines, heightRows(height))
	drawLog(out, vp, footer(vp))

	keys := make(chan logKey)
	go func() {
		defer close(keys)
		for {
			k, err := readKeyFrom(in)
			if err != nil {
				return
			}
			keys <- k
		}
	}()

	for {
		select {
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			if vp.handleKey(k) {
				io.WriteString(out, "\x1b[2J\x1b[H")
				return nil
			}
			drawLog(out, vp, footer(vp))
		case <-sigs:
			_, height := getSize()
			vp.setHeight(heightRows(height))
			drawLog(out, vp, footer(vp))
		}
	}
}

func drawLog(out io.Writer, vp *logViewport, footer string) {
	io.WriteString(out, "\x1b[H\x1b[2J")
	lines := vp.visibleLines()
	for i := 0; i < vp.height; i++ {
		if i < len(lines) {
			io.WriteString(out, lines[i])
		}
		io.WriteString(out, "\x1b[K\r\n")
	}
	io.WriteString(out, "\x1b[K"+footer+"\r\n")
}

func logFooter(vp *logViewport) string {
	if vp.total() == 0 {
		return "nipa log: no commits"
	}
	last := vp.top + len(vp.visibleLines())
	return fmt.Sprintf("nipa log: lines %d-%d of %d (q to quit)", vp.top+1, last, vp.total())
}

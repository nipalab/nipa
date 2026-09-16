package cli

import (
	"strings"
	"testing"
	"time"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func mustHashStr(hex string) serverDomain.Hash {
	h, _ := serverDomain.ParseHashHex(hex)
	return h
}

func testEntries() []*serverDomain.CommitLogEntry {
	return []*serverDomain.CommitLogEntry{
		{
			Commit: serverDomain.Commit{
				ID:        9001,
				Hash:      mustHashStr(strings.Repeat("a", 64)),
				CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				Message:   "first commit",
			},
			AuthorName:  "Alice",
			AuthorEmail: "alice@example.com",
		},
		{
			Commit: serverDomain.Commit{
				ID:        9002,
				Hash:      mustHashStr(strings.Repeat("b", 64)),
				CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				Message:   "second commit\nwith body",
			},
			AuthorName:  "Bob",
			AuthorEmail: "bob@example.com",
		},
		{
			Commit: serverDomain.Commit{
				ID:        9003,
				Hash:      mustHashStr(strings.Repeat("c", 64)),
				CreatedAt: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
				Message:   "third",
			},
			AuthorName:  "Charlie",
			AuthorEmail: "",
		},
	}
}

func TestLogLinesOneline(t *testing.T) {
	lines := logLines(testEntries(), true)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "aaaaaaaaaaaa ") {
		t.Errorf("expected short hash prefix, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "second commit") {
		t.Errorf("expected subject for second, got %q", lines[1])
	}
}

func TestLogLinesFull(t *testing.T) {
	lines := logLines(testEntries(), false)
	if len(lines) < 10 {
		t.Fatalf("expected many lines for full mode, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "commit ") {
		t.Errorf("first line should start with commit, got %q", lines[0])
	}
}

func TestFormatCommitFull(t *testing.T) {
	lines := formatCommitFull(testEntries()[1])
	if lines[0] != "commit bbbbbbbbbbbb" {
		t.Errorf("expected commit bbb, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Bob <bob@example.com>") {
		t.Errorf("expected author line with email, got %q", lines[1])
	}
	if !strings.Contains(lines[4], "    second commit") {
		t.Errorf("expected indented message, got %q", lines[4])
	}
	if !strings.Contains(lines[5], "    with body") {
		t.Errorf("expected indented body, got %q", lines[5])
	}
}

func TestFormatCommitFullNoEmail(t *testing.T) {
	lines := formatCommitFull(testEntries()[2])
	if !strings.Contains(lines[1], "Charlie") {
		t.Errorf("expected Charlie, got %q", lines[1])
	}
	if strings.Contains(lines[1], "<") {
		t.Errorf("should not have email angle brackets, got %q", lines[1])
	}
}

func TestFormatCommitOneline(t *testing.T) {
	line := formatCommitOneline(testEntries()[0])
	if !strings.HasPrefix(line, "aaaaaaaaaaaa ") {
		t.Errorf("expected short hash, got %q", line)
	}
	if !strings.Contains(line, "first commit") {
		t.Errorf("expected message, got %q", line)
	}
}

func TestNewLogViewport(t *testing.T) {
	vp := newLogViewport([]string{"a", "b", "c", "d", "e"}, 3)
	if vp.top != 0 {
		t.Errorf("top should be 0, got %d", vp.top)
	}
	if vp.height != 3 {
		t.Errorf("height should be 3, got %d", vp.height)
	}
}

func TestViewportClampBottom(t *testing.T) {
	vp := newLogViewport([]string{"a", "b", "c"}, 2)
	vp.handleKey(keyPageDown) // top += 2 → clamped to lastTop=1
	if vp.top != 1 {
		t.Errorf("top should be 1 after page down, got %d", vp.top)
	}
}

func TestViewportKeyNavigation(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e", "f", "g"}
	vp := newLogViewport(lines, 3)

	v := vp.visibleLines()
	if len(v) != 3 || v[0] != "a" || v[2] != "c" {
		t.Fatalf("unexpected initial visible: %v", v)
	}

	vp.handleKey(keyDown)
	if vp.top != 1 {
		t.Errorf("expected top=1 after down, got %d", vp.top)
	}
	v = vp.visibleLines()
	if v[0] != "b" || v[2] != "d" {
		t.Errorf("expected b/d visible after down, got %v", v)
	}

	vp.handleKey(keyPageDown)
	if vp.top != 4 {
		t.Errorf("expected top=4 after page down, got %d", vp.top)
	}
	v = vp.visibleLines()
	if v[0] != "e" || len(v) != 3 {
		t.Errorf("expected e/f/g visible, got %v", v)
	}

	vp.handleKey(keyDown)
	if vp.top != 4 {
		t.Errorf("top should stay 4, got %d", vp.top)
	}

	vp.handleKey(keyHome)
	if vp.top != 0 {
		t.Errorf("top should be 0 after home, got %d", vp.top)
	}

	vp.handleKey(keyEnd)
	if vp.top != 4 {
		t.Errorf("top should be 4 after end, got %d", vp.top)
	}

	vp.handleKey(keyUp)
	if vp.top != 3 {
		t.Errorf("top should be 3 after up, got %d", vp.top)
	}

	vp.handleKey(keyPageUp)
	if vp.top != 0 {
		t.Errorf("top should be 0 after page up, got %d", vp.top)
	}

	vp.handleKey(keyUp)
	if vp.top != 0 {
		t.Errorf("top should stay 0, got %d", vp.top)
	}
}

func TestViewportQuit(t *testing.T) {
	vp := newLogViewport([]string{"a", "b"}, 2)
	if !vp.handleKey(keyQuit) {
		t.Error("expected quit=true")
	}
	if vp.handleKey(keyDown) {
		t.Error("non-quit key should return false")
	}
}

func TestViewportSetHeight(t *testing.T) {
	vp := newLogViewport([]string{"a", "b", "c", "d"}, 10)
	vp.top = 2
	vp.setHeight(2)
	if vp.height != 2 {
		t.Errorf("height should be 2, got %d", vp.height)
	}
	if vp.top != 2 {
		t.Errorf("top should still be 2, got %d", vp.top)
	}
}

func TestViewportEmpty(t *testing.T) {
	vp := newLogViewport(nil, 10)
	if len(vp.visibleLines()) != 0 {
		t.Error("expected empty visible lines")
	}
	if !vp.handleKey(keyQuit) {
		t.Error("should quit")
	}
}

func TestReadKeyFrom(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want logKey
	}{
		{"q", "q", keyQuit},
		{"ctrl-c", "\x03", keyQuit},
		{"j", "j", keyDown},
		{"ctrl-n", "\x0e", keyDown},
		{"k", "k", keyUp},
		{"ctrl-p", "\x10", keyUp},
		{"space", " ", keyPageDown},
		{"f", "f", keyPageDown},
		{"ctrl-f", "\x06", keyPageDown},
		{"b", "b", keyPageUp},
		{"ctrl-b", "\x02", keyPageUp},
		{"g", "g", keyHome},
		{"G", "G", keyEnd},
		{"arrow-up", "\x1b[A", keyUp},
		{"arrow-down", "\x1b[B", keyDown},
		{"arrow-home", "\x1b[H", keyHome},
		{"arrow-end", "\x1b[F", keyEnd},
		{"bare-esc", "\x1b", keyQuit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readKeyFrom(strings.NewReader(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPrintLogPlain(t *testing.T) {
	var sb strings.Builder
	if err := printLogPlain(&sb, testEntries()[:1], true); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "aaaaaaaaaaaa") {
		t.Errorf("expected short hash in output, got %q", out)
	}
	if !strings.Contains(out, "first commit") {
		t.Errorf("expected commit message, got %q", out)
	}
}

func TestLogFooter(t *testing.T) {
	vp := newLogViewport([]string{"a", "b"}, 10)
	footer := logFooter(vp)
	if !strings.Contains(footer, "2") {
		t.Errorf("expected total 2 in footer, got %q", footer)
	}
	if !strings.Contains(footer, "q to quit") {
		t.Errorf("expected quit hint in footer, got %q", footer)
	}
}

func TestLogFooterEmpty(t *testing.T) {
	vp := newLogViewport(nil, 10)
	if !strings.Contains(logFooter(vp), "no commits") {
		t.Errorf("expected no commits footer, got %q", logFooter(vp))
	}
}

func TestHeightRows(t *testing.T) {
	if h := heightRows(25); h != 24 {
		t.Errorf("heightRows(25) = %d, want 24", h)
	}
	if h := heightRows(1); h != 1 {
		t.Errorf("heightRows(1) = %d, want 1", h)
	}
	if h := heightRows(0); h != 1 {
		t.Errorf("heightRows(0) = %d, want 1", h)
	}
}

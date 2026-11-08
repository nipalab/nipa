package aiprompt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/assetbudget"
	"github.com/nipalab/nipa/internal/assetdiff"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/preview"
)

func sampleChangeset() Changeset {
	return Changeset{
		Diff: []FileText{
			{Path: "a.txt", Status: "M", Lines: []string{"diff --nipa a/a.txt b/a.txt", "--- a/a.txt", "+++ b/a.txt", "@@ -1 +1,2 @@", " one", "+two"}},
			{Path: "tex.png", Status: "A", Binary: true, Lines: []string{"diff --nipa a/tex.png b/tex.png", "Binary files /dev/null and b/tex.png differ"}},
			{Path: "new.txt", OldPath: "old.txt", Status: "R"},
		},
		Summaries: []assetdiff.Summary{
			{Path: "tex.png", Status: "added", Kind: preview.Kind("image"), Headline: "4x2 RGBA"},
			{Path: "new.txt", OldPath: "old.txt", Status: "renamed", Error: "boom"},
		},
		OmittedSummaries: 2,
		Violations:       []assetbudget.Violation{{Path: "tex.png", Rule: "textures", Limit: "power_of_two", Message: "4x2 is fine"}},
	}
}

func TestCommitMessage(t *testing.T) {
	p := CommitMessage(CommitInput{Branch: "main", Changeset: sampleChangeset()})
	require.Contains(t, p.System, "72 characters")
	require.Contains(t, p.System, "Output only the commit message")
	require.Contains(t, p.User, `staged on branch "main"`)
	require.Contains(t, p.User, "M a.txt\n")
	require.Contains(t, p.User, "A tex.png [binary]\n")
	require.Contains(t, p.User, "R new.txt (from old.txt)\n")
	require.Contains(t, p.User, "- added tex.png [image]: 4x2 RGBA\n")
	require.Contains(t, p.User, "- renamed new.txt (from old.txt) (not decoded: boom)\n")
	require.Contains(t, p.User, "[... 2 more files omitted]")
	require.Contains(t, p.User, "- tex.png: power_of_two: 4x2 is fine (rule textures)\n")
	require.Contains(t, p.User, "+two\n")
	require.Contains(t, p.User, "diff --nipa a/old.txt b/new.txt\n")
	require.Equal(t, p, CommitMessage(CommitInput{Branch: "main", Changeset: sampleChangeset()}), "output must be deterministic")

	empty := CommitMessage(CommitInput{})
	require.Contains(t, empty.User, "for the staged changes.")
	require.Contains(t, empty.User, "(none)")
	require.NotContains(t, empty.User, "<diff>")
	require.NotContains(t, empty.User, "<asset_summaries>")
	require.NotContains(t, empty.User, "<budget_violations>")
}

func TestMRDescription(t *testing.T) {
	commits := []Commit{{Subject: "\nadd things\nbody", Author: "alice"}, {Subject: "fix"}}
	p := MRDescription(MRInput{Source: "feature", Target: "main", Commits: commits, Changeset: sampleChangeset()})
	require.Contains(t, p.System, "first line is the title")
	require.Contains(t, p.User, `from "feature" into "main"`)
	require.Contains(t, p.User, "- add things (alice)\n- fix\n")

	many := make([]Commit, maxCommits+3)
	for i := range many {
		many[i] = Commit{Subject: fmt.Sprintf("c%d", i)}
	}
	p = MRDescription(MRInput{Commits: many})
	require.Contains(t, p.User, "[... 3 more commits omitted]")
}

func TestReviewMR(t *testing.T) {
	line, old := int64(7), int64(3)
	threads := []Thread{
		{Comments: []Comment{{Author: "bob", Body: "overall ok?\nsecond"}}},
		{Path: "a.txt", NewLine: &line, Resolved: true, Outdated: true, Comments: []Comment{{Author: "carol", Body: "nit"}}},
		{Path: "b.txt", OldLine: &old},
		{Path: "c.txt"},
	}
	p := ReviewMR(ReviewInput{
		Number: 4, Title: "Add\ttex", Description: "desc", Author: "alice", Source: "f", Target: "main",
		Status: "open", Draft: true, BlockedBy: "draft", Threads: threads, Changeset: sampleChangeset(),
	})
	require.Contains(t, p.System, "Do not approve")
	require.Contains(t, p.System, "No findings.")
	for _, want := range []string{
		"Review merge request #4.", "title: Add tex\n", "status: open\n", "draft: true\n", "blocked_by: draft\n", "author: alice\n",
		"<description>\ndesc\n</description>", "- [unresolved] conversation\n  bob: overall ok?\n    second\n",
		"- [resolved, outdated] a.txt:7\n", "- [unresolved] b.txt (old line 3)\n", "- [unresolved] c.txt\n",
	} {
		require.Contains(t, p.User, want)
	}

	many := make([]Thread, maxThreads+2)
	p = ReviewMR(ReviewInput{Threads: many})
	require.Contains(t, p.User, "[... 2 more threads omitted]")
	require.NotContains(t, p.User, "<description>")
	require.NotContains(t, p.User, "<threads>\n</threads>")

	big := []Thread{}
	for i := 0; i < 40; i++ {
		big = append(big, Thread{Comments: []Comment{{Body: strings.Repeat("x", 2000)}}})
	}
	p = ReviewMR(ReviewInput{Threads: big})
	require.Contains(t, p.User, "more threads omitted]")

	long := Thread{}
	for i := 0; i < 100; i++ {
		long.Comments = append(long.Comments, Comment{Author: "bob", Body: strings.Repeat("y", 2000)})
	}
	p = ReviewMR(ReviewInput{Threads: []Thread{long, {Comments: []Comment{{Body: "after"}}}}})
	section := p.User[strings.Index(p.User, "<threads>"):strings.Index(p.User, "</threads>")]
	require.LessOrEqual(t, len(section), maxThreadBytes+100)
	require.Contains(t, section, "more comments omitted]")
	require.Contains(t, section, "[... 1 more threads omitted]")
	require.NotContains(t, section, "after")
}

func TestRenderDiff_Budget(t *testing.T) {
	var files []FileText
	for i := 0; i < 5; i++ {
		lines := []string{fmt.Sprintf("diff --nipa a/f%d b/f%d", i, i)}
		for j := 0; j < 100; j++ {
			lines = append(lines, "+"+strings.Repeat("y", 50))
		}
		files = append(files, FileText{Path: fmt.Sprintf("f%d", i), Lines: lines})
	}
	out := renderDiff(files, 6000)
	require.LessOrEqual(t, len(out), 6000+200)
	require.Contains(t, out, "more lines of f1 omitted]")
	require.Contains(t, out, "[... 3 more files omitted]")

	full := renderDiff(files[:1], 0)
	require.NotContains(t, full, "omitted")

	long := renderDiff([]FileText{{Path: "x", Lines: []string{"h", strings.Repeat("é", 600)}}}, 0)
	require.Contains(t, long, "…")
	require.Less(t, len(long), 600)
}

func TestSafeLines(t *testing.T) {
	out := renderDiff([]FileText{{Path: "bin", Lines: []string{"diff --nipa a/bin b/bin", "+\x00\x01"}}}, 0)
	require.Equal(t, "diff --nipa a/bin b/bin\n"+nonTextMarker+"\n", out)
	out = renderDiff([]FileText{{Path: "bad", Lines: []string{"h", "+\xff\xfe"}}}, 0)
	require.Contains(t, out, nonTextMarker)
}

func TestFromFileDiffs(t *testing.T) {
	files := []diff.FileDiff{
		{Change: diff.Change{Path: "a.txt", Status: diff.Modified, Old: diff.Entry{Path: "a.txt", Mode: 2, Hash: [32]byte{1}}, New: diff.Entry{Path: "a.txt", Mode: 2, Hash: [32]byte{2}}}, Old: []byte("one\n"), New: []byte("one\ntwo\n")},
		{Change: diff.Change{Path: "b.png", Status: diff.Added, New: diff.Entry{Path: "b.png", Mode: 2, IsBinary: true}}, New: []byte("\x89PNG\x00")},
		{Change: diff.Change{Path: "n.txt", Status: diff.Renamed, Old: diff.Entry{Path: "o.txt", Mode: 2, Hash: [32]byte{3}}, New: diff.Entry{Path: "n.txt", Mode: 2, Hash: [32]byte{3}}}},
	}
	out := FromFileDiffs(files)
	require.Len(t, out, 3)
	require.Equal(t, "M", out[0].Status)
	require.Contains(t, out[0].Lines, "+two")
	require.True(t, out[1].Binary)
	for _, l := range out[1].Lines {
		require.NotContains(t, l, "PNG")
	}
	require.Equal(t, "o.txt", out[2].OldPath)
}

func TestFromMRDiff(t *testing.T) {
	out := FromMRDiff([]*domain.MergeRequestDiffFile{
		{Path: "a.txt", Status: "modified", Patch: []string{"@@ -1 +1 @@", "-a", "+b"}},
		nil,
		{Path: "b.png", Status: "added", Binary: true, Patch: []string{"should not appear"}},
		{Path: "n.txt", OldPath: "o.txt", Status: "renamed"},
	})
	require.Len(t, out, 3)
	require.Equal(t, []string{"diff --nipa a/a.txt b/a.txt", "@@ -1 +1 @@", "-a", "+b"}, out[0].Lines)
	require.Equal(t, []string{"diff --nipa a/b.png b/b.png", "Binary file changed"}, out[1].Lines)
	require.Equal(t, "diff --nipa a/o.txt b/n.txt", out[2].Lines[0])

	p := CommitMessage(CommitInput{Changeset: Changeset{Diff: out}})
	require.Contains(t, p.User, "M a.txt\n")
	require.Contains(t, p.User, "A b.png [binary]\n")
	require.Contains(t, p.User, "R n.txt (from o.txt)\n")
}

func TestStatusLetterAndHelpers(t *testing.T) {
	for in, want := range map[string]string{"added": "A", "removed": "D", "deleted": "D", "R": "R", "modified": "M", "": "?", "X": "X"} {
		require.Equal(t, want, statusLetter(in), in)
	}
	require.Equal(t, "a b?", clean("a\nb\xff"))
	require.Equal(t, "abc", truncate("abc", 3))
	require.Equal(t, "", Subject("  \n "))
}

func TestChangedFilesCap(t *testing.T) {
	files := make([]FileText, maxChangedFiles+1)
	for i := range files {
		files[i] = FileText{Path: fmt.Sprintf("f%04d", i), Status: "A"}
	}
	summaries := make([]assetdiff.Summary, maxSummaries+1)
	violations := make([]assetbudget.Violation, maxViolations+1)
	p := CommitMessage(CommitInput{Changeset: Changeset{Diff: files, Summaries: summaries, Violations: violations, DiffBytes: 1000}})
	require.Contains(t, p.User, "[... 1 more files omitted]\n</changed_files>")
	require.Contains(t, p.User, "[... 1 more files omitted]\n</asset_summaries>")
	require.Contains(t, p.User, "[... 1 more violations omitted]")
}

func TestCleanOutput(t *testing.T) {
	require.Equal(t, "fix: x\n\nbody", CleanOutput("```text\nfix: x\n\nbody\n```\n"))
	require.Equal(t, "fix: y", CleanOutput("<think>hmm</think>\n\nfix: y"))
	require.Equal(t, "<think>open", CleanOutput("<think>open"))
	require.Equal(t, "plain", CleanOutput("  plain \n"))
}

func TestSplitTitle(t *testing.T) {
	title, body := SplitTitle("# Title: Add textures\n\nSummary\n- one")
	require.Equal(t, "Add textures", title)
	require.Equal(t, "Summary\n- one", body)
	title, body = SplitTitle("**Only title**")
	require.Equal(t, "Only title", title)
	require.Empty(t, body)
}

func TestAddTrailer(t *testing.T) {
	require.Equal(t, "msg\n\nNipa-Generated: m", AddTrailer("msg\n", GeneratedTrailer, "m"))
	require.Equal(t, "msg\n\nNipa-Generated: x", AddTrailer("msg\n\nNipa-Generated: x", GeneratedTrailer, "m"))
	require.Equal(t, "msg", AddTrailer("msg", GeneratedTrailer, " "))
	require.Equal(t, "Nipa-Generated: m", AddTrailer("", GeneratedTrailer, "m"))
}

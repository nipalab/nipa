package diff

import (
	"fmt"
	"strings"
	"testing"
)

func benchEntryMaps(total, changed int) (map[string]Entry, map[string]Entry) {
	oldMap := make(map[string]Entry, total)
	newMap := make(map[string]Entry, total)
	for i := 0; i < total; i++ {
		path := fmt.Sprintf("dir%03d/asset_%06d.bin", i%100, i)
		entry := Entry{Path: path, Mode: 0o644, SizeBytes: 1 << 20, IsBinary: true}
		entry.Hash[0] = byte(i)
		oldMap[path] = entry
		if i < changed {
			entry.SizeBytes += 4096
			entry.Hash[1] = 1
		}
		newMap[path] = entry
	}
	return oldMap, newMap
}

func BenchmarkCompare(b *testing.B) {
	cases := []struct {
		name    string
		changed int
	}{
		{name: "identical", changed: 0},
		{name: "small-delta", changed: 10},
		{name: "half-changed", changed: 5000},
	}
	for _, tc := range cases {
		oldMap, newMap := benchEntryMaps(10000, tc.changed)
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Compare(oldMap, newMap)
			}
		})
	}
}

func benchLines(n, changed int) ([]string, []string) {
	makeLines := func(tag int) []string {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %06d tag=%d payload=%s", i, tag, strings.Repeat("x", 64))
		}
		return lines
	}
	a := makeLines(0)
	bLines := makeLines(0)
	for i := 0; i < changed; i++ {
		bLines[(i*97)%n] = fmt.Sprintf("changed line %d %s", i, strings.Repeat("y", 64))
	}
	return a, bLines
}

func BenchmarkLines(b *testing.B) {
	cases := []struct {
		name    string
		total   int
		changed int
	}{
		{name: "2000-lines-1-change", total: 2000, changed: 1},
		{name: "5000-lines-50-changes", total: 5000, changed: 50},
	}
	for _, tc := range cases {
		a, bLines := benchLines(tc.total, tc.changed)
		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = Lines(a, bLines)
			}
		})
	}
}

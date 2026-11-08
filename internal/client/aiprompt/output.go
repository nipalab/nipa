package aiprompt

import (
	"strings"
)

// CleanOutput trims a model answer: a leading reasoning block
// (<think>...</think>), a code fence wrapping the whole answer and
// surrounding blank space are removed.
func CleanOutput(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<think>") {
		if end := strings.Index(s, "</think>"); end >= 0 {
			s = strings.TrimSpace(s[end+len("</think>"):])
		}
	}
	lines := strings.Split(s, "\n")
	if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		s = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	}
	return s
}

// SplitTitle splits a generated merge request into its first line (the
// title, without a Markdown heading or "Title:" prefix) and the rest.
func SplitTitle(s string) (string, string) {
	s = strings.TrimSpace(s)
	first, rest, _ := strings.Cut(s, "\n")
	title := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(first), "#"))
	if v, ok := cutPrefixFold(title, "title:"); ok {
		title = strings.TrimSpace(v)
	}
	title = strings.Trim(title, "*")
	return strings.TrimSpace(title), strings.TrimSpace(rest)
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// AddTrailer appends "key: value" as a trailer paragraph unless a trailer
// with that key is already present.
func AddTrailer(text, key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return text
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			return text
		}
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return key + ": " + value
	}
	return text + "\n\n" + key + ": " + value
}

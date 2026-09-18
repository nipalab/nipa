// Package difftool launches external compare applications for nipa diffs.
//
// A tool command is a template with placeholders:
//
//	$LOCAL, $REMOTE           absolute paths of the old/new file versions
//	$LOCAL_DIR, $REMOTE_DIR    roots of the old/new trees holding all changed files
//	$PATH                     repository-relative path of the file
//	$STATUS                   A, M or D
//
// The ${NAME} brace forms are equivalent. Unknown $X sequences pass through
// literally; $$ yields a literal $. The command is split shell-style
// (single/double quotes, backslash escapes) and executed directly without
// a shell, with stdio inherited so full-screen tools work as expected.
//
// Examples:
//
//	nipa diff --tool 'code --diff $LOCAL $REMOTE'
//	nipa diff --tool 'meld $LOCAL_DIR $REMOTE_DIR'
package difftool

import (
	"fmt"
	"strings"
)

type Command struct {
	argv []string
}

func Parse(cmd string) (Command, error) {
	var argv []string
	var cur strings.Builder
	hasToken := false
	flush := func() {
		if hasToken {
			argv = append(argv, cur.String())
			cur.Reset()
			hasToken = false
		}
	}

	runes := []rune(cmd)
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			flush()
			i++
		case c == '\'':
			j := i + 1
			for j < len(runes) && runes[j] != '\'' {
				j++
			}
			if j >= len(runes) {
				return Command{}, fmt.Errorf("unterminated single quote in diff tool command")
			}
			cur.WriteString(string(runes[i+1 : j]))
			hasToken = true
			i = j + 1
		case c == '"':
			j := i + 1
			closed := false
			for j < len(runes) {
				if runes[j] == '\\' && j+1 < len(runes) && isEscapable(runes[j+1]) {
					cur.WriteRune(runes[j+1])
					j += 2
					continue
				}
				if runes[j] == '"' {
					closed = true
					break
				}
				cur.WriteRune(runes[j])
				j++
			}
			if !closed {
				return Command{}, fmt.Errorf("unterminated double quote in diff tool command")
			}
			hasToken = true
			i = j + 1
		case c == '\\' && i+1 < len(runes) && isEscapable(runes[i+1]):
			cur.WriteRune(runes[i+1])
			hasToken = true
			i += 2
		default:
			cur.WriteRune(c)
			hasToken = true
			i++
		}
	}
	flush()
	if len(argv) == 0 {
		return Command{}, fmt.Errorf("empty diff tool command")
	}
	return Command{argv: argv}, nil
}

func isEscapable(c rune) bool {
	switch c {
	case '"', '\'', '\\', '$', ' ', '\t', '\n':
		return true
	}
	return false
}

// Pair is one changed file with materialized old/new versions.
type Pair struct {
	Path   string // repository-relative path
	Status string // A, M or D
	Local  string // absolute old-side path
	Remote string // absolute new-side path
}

func (c Command) Expand(p Pair, localDir, remoteDir string) (argv []string, env []string) {
	values := map[string]string{
		"LOCAL":      p.Local,
		"REMOTE":     p.Remote,
		"LOCAL_DIR":  localDir,
		"REMOTE_DIR": remoteDir,
		"PATH":       p.Path,
		"STATUS":     p.Status,
	}
	argv = make([]string, 0, len(c.argv))
	for _, tok := range c.argv {
		argv = append(argv, expandToken(tok, values))
	}
	env = []string{
		"NIPA_PATH=" + p.Path,
		"NIPA_STATUS=" + p.Status,
		"NIPA_LOCAL=" + p.Local,
		"NIPA_REMOTE=" + p.Remote,
		"NIPA_LOCAL_DIR=" + localDir,
		"NIPA_REMOTE_DIR=" + remoteDir,
	}
	return argv, env
}

func expandToken(tok string, values map[string]string) string {
	var sb strings.Builder
	i := 0
	for i < len(tok) {
		if tok[i] != '$' {
			sb.WriteByte(tok[i])
			i++
			continue
		}
		rest := tok[i+1:]
		if strings.HasPrefix(rest, "$") {
			sb.WriteByte('$')
			i += 2
			continue
		}
		name := ""
		consumed := 0
		if strings.HasPrefix(rest, "{") {
			end := strings.Index(rest, "}")
			if end > 1 {
				name = rest[1:end]
				consumed = end + 1
			}
		} else {
			j := 0
			for j < len(rest) && isNameChar(rest[j]) {
				j++
			}
			name = rest[:j]
			consumed = j
		}
		if v, ok := values[name]; ok && consumed > 0 {
			sb.WriteString(v)
			i += 1 + consumed
			continue
		}
		sb.WriteByte('$')
		i++
	}
	return sb.String()
}

func isNameChar(c byte) bool {
	return c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

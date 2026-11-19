package gitimport

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nipalab/nipa/internal/snow"
)

type authorKey struct {
	name  string
	email string
}

type AuthorMap struct {
	byNameEmail map[authorKey]string
	byEmail     map[string]string
}

type AuthorMapError struct {
	Line    int
	Message string
}

func (e *AuthorMapError) Error() string {
	return fmt.Sprintf("author map line %d: %s", e.Line, e.Message)
}

func LoadAuthorMap(path string) (*AuthorMap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open author map: %w", err)
	}
	defer func() { _ = f.Close() }()
	return ParseAuthorMap(f)
}

func ParseAuthorMap(r io.Reader) (*AuthorMap, error) {
	m := &AuthorMap{byNameEmail: map[authorKey]string{}, byEmail: map[string]string{}}
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, email, user, err := parseAuthorLine(text)
		if err != nil {
			return nil, &AuthorMapError{Line: line, Message: err.Error()}
		}
		if name == "" {
			if _, dup := m.byEmail[email]; dup {
				return nil, &AuthorMapError{Line: line, Message: fmt.Sprintf("<%s> is mapped twice", email)}
			}
			m.byEmail[email] = user
			continue
		}
		key := authorKey{name: name, email: email}
		if _, dup := m.byNameEmail[key]; dup {
			return nil, &AuthorMapError{Line: line, Message: fmt.Sprintf("%s <%s> is mapped twice", name, email)}
		}
		m.byNameEmail[key] = user
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read author map: %w", err)
	}
	return m, nil
}

func parseAuthorLine(text string) (string, string, string, error) {
	left, right, ok := strings.Cut(text, "=")
	if !ok {
		return "", "", "", fmt.Errorf("want `Name <email> = user-id`")
	}
	user := strings.TrimSpace(right)
	if user == "" {
		return "", "", "", fmt.Errorf("missing user id after =")
	}
	if _, err := snow.ParseBase36(user); err != nil {
		return "", "", "", fmt.Errorf("invalid user id %q", user)
	}
	left = strings.TrimSpace(left)
	open := strings.Index(left, "<")
	if open < 0 || !strings.HasSuffix(left, ">") {
		return "", "", "", fmt.Errorf("missing <email>")
	}
	email := strings.ToLower(strings.TrimSpace(left[open+1 : len(left)-1]))
	if email == "" || strings.ContainsAny(email, "<>") {
		return "", "", "", fmt.Errorf("invalid email in %q", left)
	}
	return strings.TrimSpace(left[:open]), email, user, nil
}

func (m *AuthorMap) Len() int {
	if m == nil {
		return 0
	}
	return len(m.byNameEmail) + len(m.byEmail)
}

func (m *AuthorMap) Lookup(name, email string) (string, bool) {
	if m == nil {
		return "", false
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if user, ok := m.byNameEmail[authorKey{name: strings.TrimSpace(name), email: email}]; ok {
		return user, true
	}
	user, ok := m.byEmail[email]
	return user, ok
}

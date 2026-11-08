// Package godotres reads and writes Godot text resources (.tscn, .tres) and
// the section-based config files (project.godot, .import) losslessly: Write
// reproduces the parsed input byte for byte, and edits only rewrite what
// changed.
package godotres

import (
	"errors"
	"fmt"
	"strings"
)

var ErrSyntax = errors.New("godotres: syntax error")

type Attr struct {
	Key, Raw string
}

// Prop is one key = value line; Raw is the value as written, possibly over
// several lines.
type Prop struct {
	Key, Raw string
	lead     string
	sep      string
	post     string
	parsed   bool
}

type Section struct {
	Kind  string
	Attrs []Attr
	Props []Prop
	lead  string
	head  string
	kind0 string
	attr0 []Attr
}

// File is a parsed resource. Props holds the lines before the first section
// (project.godot keeps config_version there) and Header the gd_scene or
// gd_resource section when the file starts with one.
type File struct {
	Props    []Prop
	Header   *Section
	Sections []*Section
	tail     string
}

func (s *Section) Attr(key string) (string, bool) {
	for _, a := range s.Attrs {
		if a.Key == key {
			return a.Raw, true
		}
	}
	return "", false
}

// Value returns an attribute with string quoting removed.
func (s *Section) Value(key string) string {
	raw, _ := s.Attr(key)
	return Unquote(raw)
}

func (s *Section) Prop(key string) (string, bool) {
	for _, p := range s.Props {
		if p.Key == key {
			return p.Raw, true
		}
	}
	return "", false
}

var identityKeys = map[string][]string{
	"node":         {"name", "parent"},
	"ext_resource": {"id"},
	"sub_resource": {"id"},
	"connection":   {"signal", "from", "to", "method"},
	"editable":     {"path"},
	"resource":     {},
	"gd_scene":     {},
	"gd_resource":  {},
}

// Identity keys a section across versions of a file: nodes by name and
// parent, resources by id, connections by their endpoints, and any other
// section by its kind and attributes.
func (s *Section) Identity() string {
	keys, known := identityKeys[s.Kind]
	var b strings.Builder
	b.WriteString(s.Kind)
	if !known {
		for _, a := range s.Attrs {
			b.WriteString(" " + a.Key + "=" + a.Raw)
		}
		return b.String()
	}
	for _, k := range keys {
		if v, ok := s.Attr(k); ok {
			b.WriteString(" " + k + "=" + v)
		}
	}
	return b.String()
}

// Unquote decodes a Godot string literal, including StringName (&"") and
// NodePath (^"") forms; anything else is returned unchanged.
func Unquote(raw string) string {
	s := raw
	if strings.HasPrefix(s, "&") || strings.HasPrefix(s, "^") {
		s = s[1:]
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return raw
	}
	s = s[1 : len(s)-1]
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; c {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'u', 'U':
			n := 4
			if c == 'U' {
				n = 6
			}
			if r, ok := hexRune(s[i+1:], n); ok {
				b.WriteRune(r)
				i += n
			} else {
				b.WriteByte(c)
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func hexRune(s string, n int) (rune, bool) {
	if len(s) < n {
		return 0, false
	}
	var r rune
	for _, c := range []byte(s[:n]) {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

type parser struct {
	data []byte
	pos  int
}

func (p *parser) errorf(at int, format string, args ...any) error {
	line := 1 + strings.Count(string(p.data[:at]), "\n")
	return fmt.Errorf("%w: line %d: %s", ErrSyntax, line, fmt.Sprintf(format, args...))
}

// Parse splits data into its header, sections and properties.
func Parse(data []byte) (*File, error) {
	p := &parser{data: data}
	f := &File{}
	var cur *Section
	lead := 0
	for p.pos < len(data) {
		start := p.pos
		p.skipBlank()
		if p.pos == len(data) {
			break
		}
		switch data[p.pos] {
		case '\n', ';', '#':
			p.toLineEnd()
			p.eol()
		case '[':
			s, err := p.section()
			if err != nil {
				return nil, err
			}
			s.lead = string(data[lead:start])
			s.head = string(data[start:p.pos])
			lead = p.pos
			if cur == nil && f.Header == nil && (s.Kind == "gd_scene" || s.Kind == "gd_resource") {
				f.Header = s
			} else {
				f.Sections = append(f.Sections, s)
			}
			cur = s
		default:
			k := p.pos
			pr, err := p.prop()
			if err != nil {
				return nil, err
			}
			pr.lead = string(data[lead:k])
			lead = p.pos
			if cur == nil {
				f.Props = append(f.Props, pr)
			} else {
				cur.Props = append(cur.Props, pr)
			}
		}
	}
	f.tail = string(data[lead:])
	return f, nil
}

func (p *parser) skipBlank() {
	for p.pos < len(p.data) && (p.data[p.pos] == ' ' || p.data[p.pos] == '\t' || p.data[p.pos] == '\r') {
		p.pos++
	}
}

func (p *parser) toLineEnd() {
	for p.pos < len(p.data) && p.data[p.pos] != '\n' {
		p.pos++
	}
}

func (p *parser) eol() {
	if p.pos < len(p.data) {
		p.pos++
	}
}

// rest consumes trailing blanks and an optional comment up to the end of the
// line, failing on anything else.
func (p *parser) rest() error {
	p.skipBlank()
	if p.pos < len(p.data) && p.data[p.pos] != '\n' && p.data[p.pos] != ';' {
		return p.errorf(p.pos, "unexpected %q", p.data[p.pos])
	}
	p.toLineEnd()
	p.eol()
	return nil
}

func isNameByte(c byte) bool {
	return c != ' ' && c != '\t' && c != '\r' && c != '\n' && c != '=' && c != '[' && c != ']' && c != '"'
}

func (p *parser) section() (*Section, error) {
	open := p.pos
	p.pos++
	k := p.pos
	for p.pos < len(p.data) && isNameByte(p.data[p.pos]) {
		p.pos++
	}
	if p.pos == k {
		return nil, p.errorf(open, "section without a name")
	}
	s := &Section{Kind: string(p.data[k:p.pos])}
	for {
		p.skipBlank()
		if p.pos == len(p.data) || p.data[p.pos] == '\n' {
			return nil, p.errorf(open, "unterminated section header")
		}
		if p.data[p.pos] == ']' {
			p.pos++
			break
		}
		k := p.pos
		for p.pos < len(p.data) && isNameByte(p.data[p.pos]) {
			p.pos++
		}
		if p.pos == k || p.pos == len(p.data) || p.data[p.pos] != '=' {
			return nil, p.errorf(k, "malformed section attribute")
		}
		key := string(p.data[k:p.pos])
		p.pos++
		raw, err := p.value(true)
		if err != nil {
			return nil, err
		}
		s.Attrs = append(s.Attrs, Attr{Key: key, Raw: raw})
	}
	if err := p.rest(); err != nil {
		return nil, err
	}
	s.kind0, s.attr0 = s.Kind, append([]Attr(nil), s.Attrs...)
	return s, nil
}

func (p *parser) prop() (Prop, error) {
	k := p.pos
	if p.data[p.pos] == '"' {
		if err := p.skipString(); err != nil {
			return Prop{}, err
		}
	}
	for p.pos < len(p.data) && p.data[p.pos] != '=' && p.data[p.pos] != '\n' {
		p.pos++
	}
	if p.pos == len(p.data) || p.data[p.pos] != '=' {
		return Prop{}, p.errorf(k, "expected key = value")
	}
	key := strings.TrimRight(string(p.data[k:p.pos]), " \t\r")
	if key == "" {
		return Prop{}, p.errorf(k, "empty key")
	}
	sepStart := k + len(key)
	p.pos++
	p.skipBlank()
	valStart := p.pos
	raw, err := p.value(false)
	if err != nil {
		return Prop{}, err
	}
	if raw == "" {
		return Prop{}, p.errorf(k, "missing value for %q", key)
	}
	postStart := valStart + len(raw)
	if err := p.rest(); err != nil {
		return Prop{}, err
	}
	return Prop{Key: key, Raw: raw, sep: string(p.data[sepStart:valStart]), post: string(p.data[postStart:p.pos]), parsed: true}, nil
}

func (p *parser) skipString() error {
	open := p.pos
	for p.pos++; p.pos < len(p.data); p.pos++ {
		switch p.data[p.pos] {
		case '\\':
			p.pos++
		case '"':
			p.pos++
			return nil
		}
	}
	return p.errorf(open, "unterminated string")
}

// value scans one variant: up to the end of the line, or in a section
// header up to a blank or the closing bracket, outside strings and brackets.
func (p *parser) value(inHeader bool) (string, error) {
	start := p.pos
	depth := 0
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if depth == 0 && (c == '\n' || c == ';' || inHeader && (c == ' ' || c == '\t' || c == ']')) {
			break
		}
		switch c {
		case ';':
			p.toLineEnd()
			continue
		case '"':
			if err := p.skipString(); err != nil {
				return "", err
			}
			continue
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return "", p.errorf(p.pos, "unbalanced %q", c)
			}
			depth--
		}
		p.pos++
	}
	if depth > 0 {
		return "", p.errorf(start, "unterminated value")
	}
	return strings.TrimRight(string(p.data[start:p.pos]), " \t\r"), nil
}

func (s *Section) headerText() string {
	if s.head != "" && s.Kind == s.kind0 && attrsEqual(s.Attrs, s.attr0) {
		return s.head
	}
	var b strings.Builder
	b.WriteString("[" + s.Kind)
	for _, a := range s.Attrs {
		b.WriteString(" " + a.Key + "=" + a.Raw)
	}
	b.WriteString("]\n")
	return b.String()
}

func attrsEqual(a, b []Attr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeProps(b *strings.Builder, props []Prop) {
	for _, p := range props {
		sep, post := p.sep, p.post
		if !p.parsed {
			sep, post = " = ", "\n"
		}
		b.WriteString(p.lead + p.Key + sep + p.Raw + post)
	}
}

func writeSection(b *strings.Builder, s *Section) {
	lead := s.lead
	if s.head == "" && b.Len() > 0 {
		lead = "\n"
	}
	b.WriteString(lead + s.headerText())
	writeProps(b, s.Props)
}

// Write serializes f. A parsed file comes back byte for byte; new sections
// and properties use Godot's own layout.
func Write(f *File) []byte {
	var b strings.Builder
	writeProps(&b, f.Props)
	if f.Header != nil {
		writeSection(&b, f.Header)
	}
	for _, s := range f.Sections {
		writeSection(&b, s)
	}
	b.WriteString(f.tail)
	return []byte(b.String())
}

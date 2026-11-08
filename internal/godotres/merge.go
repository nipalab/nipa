package godotres

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Conflict is one change Merge3 cannot settle: the same key changed
// differently on both sides, or a section one side removed and the other
// changed. Section is the section identity ("" for the lines before the first
// section), Key the attribute or property name ("" for the whole section).
type Conflict struct {
	Section string
	Key     string
	Message string
}

var bom = []byte{0xef, 0xbb, 0xbf}

const loadSteps = "load_steps"

// Merge3 merges two edits of a Godot text resource section by section and
// key by key. Sections keep ours' order; sections only theirs added follow
// their predecessor in theirs. Resource ids both sides added for different
// resources are renumbered in theirs together with their references, and
// load_steps is recomputed. When conflicts is not empty the merged bytes are
// only a best effort (ours wins) and must not be written as a resolution.
func Merge3(base, ours, theirs []byte) ([]byte, []Conflict, error) {
	hadBOM := bytes.HasPrefix(ours, bom)
	files := make([]*File, 3)
	for i, data := range [][]byte{base, ours, theirs} {
		f, err := Parse(bytes.TrimPrefix(data, bom))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", [...]string{"base", "ours", "theirs"}[i], err)
		}
		files[i] = f
	}
	b, o, t := files[0], files[1], files[2]
	m := &merger{}
	oursResources := resourceCount(o)
	renumber(b, o, t)
	sides := [2]side{sideOf(o), sideOf(t)}
	orders := [2]order{orderOf(o), orderOf(t)}

	o.Props = mergeProps(m, "", b.Props, o.Props, t.Props)
	m.header(b, o, t)
	o.Sections = mergeKeyed(m, sectionLabel, b.Sections, o.Sections, t.Sections, (*Section).Identity, sectionEqual, m.section)
	fixLoadSteps(o, oursResources)
	m.validate(o, sides)
	m.validateOrder(o, orders)

	out := Write(o)
	if hadBOM {
		out = append(append([]byte(nil), bom...), out...)
	}
	return out, m.conflicts, nil
}

type merger struct {
	conflicts []Conflict
}

func (m *merger) conflict(section, key, format string, args ...any) {
	m.conflicts = append(m.conflicts, Conflict{Section: section, Key: key, Message: fmt.Sprintf(format, args...)})
}

// mergeKeyed merges three ordered lists keyed by key: ours' order first, and
// each theirs-only item right after the nearest item before it in theirs
// that the result keeps. both merges an item present in ours and theirs
// (base is nil when both sides added it).
func mergeKeyed[T any](m *merger, label func(k string) (string, string), base, ours, theirs []T, key func(T) string, equal func(a, b T) bool, both func(k string, base *T, o, t T) T) []T {
	baseKeys, oursKeys, theirsKeys := occurrenceKeys(base, key), occurrenceKeys(ours, key), occurrenceKeys(theirs, key)
	baseBy, oursBy, theirsBy := byKey(base, baseKeys), byKey(ours, oursKeys), byKey(theirs, theirsKeys)

	var kept []T
	var keptKeys []string
	inKept := map[string]bool{}
	for i, o := range ours {
		k := oursKeys[i]
		b, inBase := baseBy[k]
		t, inTheirs := theirsBy[k]
		switch {
		case inBase && inTheirs:
			o = both(k, &b, o, t)
		case inBase:
			if equal(o, b) {
				continue
			}
			s, key := label(k)
			m.conflict(s, key, "changed in ours, removed in theirs")
		case inTheirs:
			o = both(k, nil, o, t)
		}
		kept = append(kept, o)
		keptKeys = append(keptKeys, k)
		inKept[k] = true
	}
	for i, b := range base {
		k := baseKeys[i]
		if _, inOurs := oursBy[k]; inOurs {
			continue
		}
		if t, inTheirs := theirsBy[k]; inTheirs && !equal(t, b) {
			s, key := label(k)
			m.conflict(s, key, "removed in ours, changed in theirs")
		}
	}

	after := map[string][]T{}
	anchor := ""
	added := false
	for i, t := range theirs {
		k := theirsKeys[i]
		if inKept[k] {
			anchor = k
			continue
		}
		if _, inBase := baseBy[k]; inBase {
			continue
		}
		after[anchor] = append(after[anchor], t)
		added = true
	}
	if !added {
		return kept
	}
	out := append([]T(nil), after[""]...)
	for i, item := range kept {
		out = append(out, item)
		out = append(out, after[keptKeys[i]]...)
	}
	return out
}

// occurrenceKeys suffixes repeated keys with their occurrence so duplicates
// pair up in order across the three versions.
func occurrenceKeys[T any](items []T, key func(T) string) []string {
	seen := map[string]int{}
	out := make([]string, len(items))
	for i, item := range items {
		k := key(item)
		seen[k]++
		if n := seen[k]; n > 1 {
			k += "#" + strconv.Itoa(n)
		}
		out[i] = "\x00" + k
	}
	return out
}

func byKey[T any](items []T, keys []string) map[string]T {
	out := make(map[string]T, len(items))
	for i, item := range items {
		out[keys[i]] = item
	}
	return out
}

func trimKey(k string) string {
	return strings.TrimPrefix(k, "\x00")
}

func propKey(p Prop) string { return p.Key }

func propEqual(a, b Prop) bool { return a.Key == b.Key && a.Raw == b.Raw }

func attrKey(a Attr) string { return a.Key }

func attrEqual(a, b Attr) bool { return a == b }

func keyLabel(section string) func(string) (string, string) {
	return func(k string) (string, string) { return section, trimKey(k) }
}

func sectionLabel(k string) (string, string) { return trimKey(k), "" }

func mergeProps(m *merger, section string, base, ours, theirs []Prop) []Prop {
	return mergeKeyed(m, keyLabel(section), base, ours, theirs, propKey, propEqual, func(k string, b *Prop, o, t Prop) Prop {
		var raw *string
		if b != nil {
			raw = &b.Raw
		}
		o.Raw = pick3(m, section, k, raw, o.Raw, t.Raw)
		return o
	})
}

func mergeAttrs(m *merger, section string, base, ours, theirs []Attr) []Attr {
	return mergeKeyed(m, keyLabel(section), base, ours, theirs, attrKey, attrEqual, func(k string, b *Attr, o, t Attr) Attr {
		var raw *string
		if b != nil {
			raw = &b.Raw
		}
		o.Raw = pick3(m, section, k, raw, o.Raw, t.Raw)
		return o
	})
}

// pick3 settles one value both sides hold; b is nil when both added it.
func pick3(m *merger, section, k string, b *string, o, t string) string {
	switch {
	case o == t:
		return o
	case b != nil && *b == t:
		return o
	case b != nil && *b == o:
		return t
	}
	key := trimKey(k)
	if b == nil {
		m.conflict(section, key, "added differently on both sides: ours %s, theirs %s", short(o), short(t))
	} else {
		m.conflict(section, key, "changed differently on both sides: ours %s, theirs %s", short(o), short(t))
	}
	return o
}

func short(raw string) string {
	raw = strings.ReplaceAll(raw, "\n", " ")
	if len(raw) > 60 {
		return raw[:57] + "..."
	}
	return raw
}

func (m *merger) section(_ string, b **Section, o, t *Section) *Section {
	id := o.Identity()
	var base *Section
	if b != nil {
		base = *b
	}
	if sectionEqual(t, base) || sectionEqual(o, t) {
		return o
	}
	if base != nil && sectionEqual(o, base) {
		return t
	}
	var baseAttrs []Attr
	var baseProps []Prop
	if base != nil {
		baseAttrs, baseProps = base.Attrs, base.Props
	}
	o.Attrs = mergeAttrs(m, id, baseAttrs, o.Attrs, t.Attrs)
	o.Props = mergeProps(m, id, baseProps, o.Props, t.Props)
	return o
}

func (m *merger) header(b, o, t *File) {
	if b.Header != nil && o.Header != nil && t.Header != nil {
		if v, ok := o.Header.Attr(loadSteps); ok {
			setAttr(b.Header, loadSteps, v)
			setAttr(t.Header, loadSteps, v)
		} else if v, ok := b.Header.Attr(loadSteps); ok {
			setAttr(t.Header, loadSteps, v)
		}
		o.Header = m.section("", &b.Header, o.Header, t.Header)
		return
	}
	switch {
	case sectionEqual(t.Header, b.Header), sectionEqual(o.Header, t.Header):
	case sectionEqual(o.Header, b.Header):
		if o.Header == nil && len(o.Sections) > 0 && o.Sections[0].lead == "" {
			o.Sections[0].lead = "\n"
		}
		o.Header = t.Header
	default:
		m.conflict("header", "", "the file header changed differently on both sides")
	}
}

func setAttr(s *Section, key, raw string) {
	for i := range s.Attrs {
		if s.Attrs[i].Key == key {
			s.Attrs[i].Raw = raw
		}
	}
}

func sectionEqual(a, b *Section) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || !attrsEqual(a.Attrs, b.Attrs) || len(a.Props) != len(b.Props) {
		return false
	}
	for i := range a.Props {
		if !propEqual(a.Props[i], b.Props[i]) {
			return false
		}
	}
	return true
}

func isResource(s *Section) bool {
	return s.Kind == "ext_resource" || s.Kind == "sub_resource"
}

func resourceCount(f *File) int {
	n := 0
	for _, s := range f.Sections {
		if isResource(s) {
			n++
		}
	}
	return n
}

// fixLoadSteps shifts load_steps by the resources the merge added or removed
// relative to ours, so an untouched count stays byte-identical.
func fixLoadSteps(f *File, oursResources int) {
	if f.Header == nil {
		return
	}
	raw, ok := f.Header.Attr(loadSteps)
	if !ok {
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return
	}
	if delta := resourceCount(f) - oursResources; delta != 0 {
		setAttr(f.Header, loadSteps, strconv.Itoa(n+delta))
	}
}

var refKinds = map[string]string{"ExtResource(": "ext_resource", "SubResource(": "sub_resource"}

// rewriteRefs calls fn for every ExtResource(...)/SubResource(...) argument in
// raw and substitutes what it returns.
func rewriteRefs(raw string, fn func(kind, id string) string) string {
	if !strings.Contains(raw, "Resource(") {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		matched := false
		for prefix, kind := range refKinds {
			if !strings.HasPrefix(raw[i:], prefix) {
				continue
			}
			end := strings.IndexByte(raw[i+len(prefix):], ')')
			if end < 0 {
				break
			}
			arg := raw[i+len(prefix) : i+len(prefix)+end]
			b.WriteString(prefix)
			if id := strings.TrimSpace(arg); id != "" {
				b.WriteString(strings.Replace(arg, id, fn(kind, id), 1))
			} else {
				b.WriteString(arg)
			}
			b.WriteByte(')')
			i += len(prefix) + end + 1
			matched = true
			break
		}
		if !matched {
			b.WriteByte(raw[i])
			i++
		}
	}
	return b.String()
}

func eachRaw(s *Section, fn func(key, raw string) string) {
	for i := range s.Attrs {
		s.Attrs[i].Raw = fn(s.Attrs[i].Key, s.Attrs[i].Raw)
	}
	for i := range s.Props {
		s.Props[i].Raw = fn(s.Props[i].Key, s.Props[i].Raw)
	}
}

func definedIDs(f *File) map[string]map[string]bool {
	out := map[string]map[string]bool{"ext_resource": {}, "sub_resource": {}}
	for _, s := range f.Sections {
		if id, ok := s.Attr("id"); ok && isResource(s) {
			out[s.Kind][id] = true
		}
	}
	return out
}

func resourcesByID(f *File) map[string]map[string]*Section {
	out := map[string]map[string]*Section{"ext_resource": {}, "sub_resource": {}}
	for _, s := range f.Sections {
		if id, ok := s.Attr("id"); ok && isResource(s) {
			out[s.Kind][id] = s
		}
	}
	return out
}

// sameResource compares two resource sections ignoring their ids.
func sameResource(a, b *Section) bool {
	strip := func(s *Section) *Section {
		c := *s
		c.Attrs = nil
		for _, at := range s.Attrs {
			if at.Key != "id" {
				c.Attrs = append(c.Attrs, at)
			}
		}
		return &c
	}
	return sectionEqual(strip(a), strip(b))
}

// renumber gives theirs' new resources a fresh id when ours added a different
// resource under the same id, and reuses ours' id when both added the same
// external file, rewriting theirs' references to match.
func renumber(b, o, t *File) {
	baseIDs, oursIDs, theirsIDs := resourcesByID(b), resourcesByID(o), resourcesByID(t)
	used := map[string]map[string]bool{}
	for kind := range baseIDs {
		used[kind] = map[string]bool{}
		for _, ids := range []map[string]*Section{baseIDs[kind], oursIDs[kind], theirsIDs[kind]} {
			for id := range ids {
				used[kind][id] = true
			}
		}
	}
	renames := map[string]map[string]string{"ext_resource": {}, "sub_resource": {}}
	for _, ts := range t.Sections {
		id, ok := ts.Attr("id")
		if !ok || !isResource(ts) {
			continue
		}
		os, inOurs := oursIDs[ts.Kind][id]
		if _, inBase := baseIDs[ts.Kind][id]; inBase || !inOurs || sameResource(os, ts) {
			continue
		}
		fresh := freshID(id, used[ts.Kind])
		used[ts.Kind][fresh] = true
		renames[ts.Kind][id] = fresh
	}
	for _, ts := range t.Sections {
		id, ok := ts.Attr("id")
		if !ok || ts.Kind != "ext_resource" {
			continue
		}
		if _, inBase := baseIDs[ts.Kind][id]; inBase {
			continue
		}
		if os, inOurs := oursIDs[ts.Kind][id]; inOurs && sameResource(os, ts) {
			continue
		}
		for _, os := range o.Sections {
			oid, ok := os.Attr("id")
			if !ok || os.Kind != ts.Kind {
				continue
			}
			_, oidInBase := baseIDs[ts.Kind][oid]
			_, oidInTheirs := theirsIDs[ts.Kind][oid]
			_, oidMoved := renames[ts.Kind][oid]
			if !oidInBase && (!oidInTheirs || oidMoved) && sameResource(os, ts) {
				renames[ts.Kind][id] = oid
				break
			}
		}
	}
	if len(renames["ext_resource"])+len(renames["sub_resource"]) == 0 {
		return
	}
	for _, s := range t.Sections {
		if isResource(s) {
			if id, ok := s.Attr("id"); ok {
				if to, ok := renames[s.Kind][id]; ok {
					setAttr(s, "id", to)
				}
			}
		}
		eachRaw(s, func(_, raw string) string {
			return rewriteRefs(raw, func(kind, id string) string {
				if to, ok := renames[kind][id]; ok {
					return to
				}
				return id
			})
		})
	}
}

func freshID(id string, used map[string]bool) string {
	if n, err := strconv.Atoi(id); err == nil {
		for max := range used {
			if v, err := strconv.Atoi(max); err == nil && v > n {
				n = v
			}
		}
		return strconv.Itoa(n + 1)
	}
	quoted := len(id) >= 2 && id[0] == '"' && id[len(id)-1] == '"'
	stem := strings.Trim(id, `"`)
	for i := 2; ; i++ {
		c := stem + "_" + strconv.Itoa(i)
		if quoted {
			c = `"` + c + `"`
		}
		if !used[c] {
			return c
		}
	}
}

func nodePath(s *Section) (string, bool) {
	parent, ok := s.Attr("parent")
	if !ok {
		return ".", false
	}
	p := Unquote(parent)
	if p == "." {
		return s.Value("name"), true
	}
	return p + "/" + s.Value("name"), true
}

func nodePaths(f *File) map[string]bool {
	out := map[string]bool{".": true}
	for _, s := range f.Sections {
		if s.Kind == "node" {
			p, _ := nodePath(s)
			out[p] = true
		}
	}
	return out
}

// resolves reports whether a node path is declared, or sits under a declared
// node (children of instanced scenes are not declared in the file).
func resolves(path string, nodes map[string]bool) bool {
	for p := path; p != ""; {
		if nodes[p] {
			return true
		}
		i := strings.LastIndexByte(p, '/')
		if i < 0 {
			return false
		}
		p = p[:i]
	}
	return false
}

// side is what one input held before the merge, so validate only flags
// breakage the merge introduced, not what a side already had.
type side struct {
	refs    map[string]map[string]bool
	defined map[string]map[string]bool
	nodes   map[string]bool
	roots   int
}

func sideOf(f *File) side {
	sd := side{refs: map[string]map[string]bool{}, defined: definedIDs(f), nodes: nodePaths(f)}
	keys := sectionKeys(f)
	for i, s := range f.Sections {
		refs := map[string]bool{}
		eachRaw(s, func(_, raw string) string {
			rewriteRefs(raw, func(kind, ref string) string {
				refs[kind+" "+ref] = true
				return ref
			})
			return raw
		})
		sd.refs[keys[i]] = refs
		if _, hasParent := nodePath(s); s.Kind == "node" && !hasParent {
			sd.roots++
		}
	}
	return sd
}

// validate flags references the merge broke: a resource, parent node or
// connection endpoint that resolves in every input holding the section but
// not in the result, and an extra root node.
func (m *merger) validate(f *File, sides [2]side) {
	have := definedIDs(f)
	nodes := nodePaths(f)
	broken := func(inSide func(side) bool, resolved func(side) bool) bool {
		for _, sd := range sides {
			if inSide(sd) && !resolved(sd) {
				return false
			}
		}
		return true
	}
	held := func(sk string) func(side) bool {
		return func(sd side) bool { _, ok := sd.refs[sk]; return ok }
	}
	lost := func(sk, path string) bool {
		return !resolves(path, nodes) && broken(held(sk), func(sd side) bool { return resolves(path, sd.nodes) })
	}
	roots := 0
	keys := sectionKeys(f)
	for i, s := range f.Sections {
		id, sk := s.Identity(), keys[i]
		eachRaw(s, func(key, raw string) string {
			rewriteRefs(raw, func(kind, ref string) string {
				k := kind + " " + ref
				if !have[kind][ref] && broken(func(sd side) bool { return sd.refs[sk][k] }, func(sd side) bool { return sd.defined[kind][ref] }) {
					m.conflict(id, key, "refers to %s %s, which the merge removed", kind, ref)
				}
				return ref
			})
			return raw
		})
		switch s.Kind {
		case "node":
			if _, hasParent := nodePath(s); !hasParent {
				roots++
				if roots == max(2, sides[0].roots+1, sides[1].roots+1) {
					m.conflict(id, "", "the merge leaves two root nodes")
				}
			} else if parent := Unquote(attrValue(s.Attr("parent"))); lost(sk, parent) {
				m.conflict(id, "parent", "parent node %s was removed", parent)
			}
		case "connection":
			for _, key := range []string{"from", "to"} {
				if v, ok := s.Attr(key); ok && lost(sk, Unquote(v)) {
					m.conflict(id, key, "node %s was removed", Unquote(v))
				}
			}
		case "editable":
			if v, ok := s.Attr("path"); ok && lost(sk, Unquote(v)) {
				m.conflict(id, "path", "node %s was removed", Unquote(v))
			}
		}
	}
}

func attrValue(v string, _ bool) string { return v }

// order records where sections sit, so a merge that moves a child before its
// parent, or a use before its sub_resource, can be told from a source file
// that was already written that way.
type order struct {
	section map[string]int
	node    map[string]int
	res     map[string]int
}

func orderOf(f *File) order {
	o := order{section: map[string]int{}, node: map[string]int{}, res: map[string]int{}}
	keys := sectionKeys(f)
	first := func(m map[string]int, k string, i int) {
		if _, ok := m[k]; !ok {
			m[k] = i
		}
	}
	for i, s := range f.Sections {
		o.section[keys[i]] = i
		switch {
		case s.Kind == "node":
			p, _ := nodePath(s)
			first(o.node, p, i)
		case isResource(s):
			if id, ok := s.Attr("id"); ok {
				first(o.res, s.Kind+" "+id, i)
			}
		}
	}
	return o
}

// after reports whether dep sits after the section in an order that holds
// both, i.e. the source already had them that way.
func (o order) after(identity string, dep int, ok bool) bool {
	i, has := o.section[identity]
	return has && ok && dep > i
}

func (m *merger) validateOrder(f *File, sides [2]order) {
	merged := orderOf(f)
	keptOrder := func(identity string, lookup func(order) (int, bool)) bool {
		for _, side := range sides {
			if dep, ok := lookup(side); side.after(identity, dep, ok) {
				return true
			}
		}
		return false
	}
	keys := sectionKeys(f)
	for i, s := range f.Sections {
		id, skey := s.Identity(), keys[i]
		if s.Kind == "node" {
			if parent, ok := s.Attr("parent"); ok {
				p := Unquote(parent)
				if at, declared := merged.node[p]; declared && at > i && !keptOrder(skey, func(o order) (int, bool) { v, ok := o.node[p]; return v, ok }) {
					m.conflict(id, "parent", "the merge puts parent node %s after this node", p)
				}
			}
		}
		eachRaw(s, func(key, raw string) string {
			rewriteRefs(raw, func(kind, ref string) string {
				k := kind + " " + ref
				if at, declared := merged.res[k]; declared && at > i && !keptOrder(skey, func(o order) (int, bool) { v, ok := o.res[k]; return v, ok }) {
					m.conflict(id, key, "the merge puts %s %s after its first use", kind, ref)
				}
				return ref
			})
			return raw
		})
	}
}

func sectionKeys(f *File) []string {
	return occurrenceKeys(f.Sections, (*Section).Identity)
}

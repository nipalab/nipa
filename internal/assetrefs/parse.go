package assetrefs

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"github.com/nipalab/nipa/internal/godotres"
)

var guidRef = regexp.MustCompile(`guid:[ \t]*([0-9a-fA-F]{32})`)

// ParseRefs returns the raw references of one source file, sorted and
// deduplicated: guid:<x> for Unity, the res:// path (else the uid://) for
// Godot. Non-source, binary and unparsable files have none.
func ParseRefs(p string, data []byte) []string {
	refs, _ := parseRaw(p, data)
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.raw)
	}
	return out
}

func parseRaw(p string, data []byte) ([]rawRef, string) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	switch {
	case isUnitySource(p):
		return unityRefs(data), ""
	case isGodotSource(p):
		return godotRefs(data)
	}
	return nil, ""
}

func unityRefs(data []byte) []rawRef {
	if !bytes.HasPrefix(data, []byte("%YAML")) {
		return nil
	}
	seen := map[string]bool{}
	for _, m := range guidRef.FindAllSubmatch(data, -1) {
		guid := strings.ToLower(string(m[1]))
		if !strings.HasPrefix(guid, "0000000000000000") {
			seen[guid] = true
		}
	}
	out := make([]rawRef, 0, len(seen))
	for guid := range seen {
		out = append(out, rawRef{raw: guidPrefix + guid})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].raw < out[j].raw })
	return out
}

func godotRefs(data []byte) ([]rawRef, string) {
	if bytes.HasPrefix(data, []byte("RSRC")) || bytes.HasPrefix(data, []byte("RSCC")) {
		return nil, ""
	}
	f, err := godotres.Parse(data)
	if err != nil {
		return nil, ""
	}
	self := ""
	if f.Header != nil {
		self = validUID(f.Header.Value("uid"))
	}
	seen := map[string]bool{}
	var out []rawRef
	for _, s := range f.Sections {
		if s.Kind != "ext_resource" {
			continue
		}
		r := rawRef{uid: validUID(s.Value("uid"))}
		if p := s.Value("path"); strings.HasPrefix(p, resScheme) && len(p) > len(resScheme) {
			r.path, r.raw = p, p
		} else if r.uid != "" {
			r.raw = r.uid
		}
		if r.raw == "" || seen[r.raw] {
			continue
		}
		seen[r.raw] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].raw < out[j].raw })
	return out, self
}

func validUID(s string) string {
	if strings.HasPrefix(s, uidScheme) && len(s) > len(uidScheme) && !strings.ContainsAny(s, " \t\r\n\"") {
		return s
	}
	return ""
}

package preview

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/godotres"
)

var godotNodeAttrs = map[string]bool{"name": true, "parent": true, "type": true, "instance": true, "node_paths": true}

var godotConnectionAttrs = map[string]bool{"signal": true, "from": true, "to": true, "method": true}

type godotParser struct {
	ext   map[string]string
	sub   map[string]string
	refs  map[string]bool
	ids   map[string]bool
	props int
	scene *Scene
}

// ParseGodotResource reads a Godot text scene or resource (.tscn, .tres) or
// a section-based config file (project.godot, .import). Nodes are keyed by
// their path below the root, since resource ids are renumbered on save.
func ParseGodotResource(data []byte) (*Scene, error) {
	if bytes.HasPrefix(data, []byte("RSRC")) || bytes.HasPrefix(data, []byte("RSCC")) {
		return nil, fmt.Errorf("%w: binary Godot resource, save it as .tscn or .tres", ErrUnsupported)
	}
	f, err := godotres.Parse(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errScene, strings.TrimPrefix(err.Error(), "godotres: "))
	}
	p := &godotParser{ext: map[string]string{}, sub: map[string]string{}, refs: map[string]bool{}, ids: map[string]bool{}, scene: &Scene{Engine: "godot", Objects: []SceneObject{}}}
	for _, s := range f.Sections {
		switch s.Kind {
		case "ext_resource":
			target := s.Value("path")
			if target == "" {
				target = s.Value("uid")
			}
			p.ext[s.Value("id")] = target
			if target != "" {
				p.refs[target] = true
			}
		case "sub_resource":
			p.sub[s.Value("id")] = "sub:" + s.Value("type") + "#" + s.Value("id")
		}
	}
	if len(f.Props) > 0 {
		if err := p.add("global", "(global)", "section", "", nil, f.Props); err != nil {
			return nil, err
		}
	}
	root := ""
	for _, s := range f.Sections {
		var err error
		switch s.Kind {
		case "ext_resource":
		case "sub_resource":
			ref := p.sub[s.Value("id")]
			err = p.add(ref, ref, s.Value("type"), "", nil, s.Props)
		case "node":
			err = p.node(s, &root)
		case "connection":
			err = p.connection(s, root)
		case "editable":
			path := s.Value("path")
			err = p.add("editable:"+path, "editable "+path, "editable", "", nil, nil)
		case "resource":
			typ := "Resource"
			if f.Header != nil && f.Header.Value("type") != "" {
				typ = f.Header.Value("type")
			}
			err = p.add("resource", typ, typ, "", nil, s.Props)
		default:
			id := s.Identity()
			err = p.add("section:"+id, "["+clip(id)+"]", "section", "", nil, s.Props)
		}
		if err != nil {
			return nil, err
		}
	}
	p.scene.Refs = sortedSet(p.refs)
	return p.scene, nil
}

func (p *godotParser) node(s *godotres.Section, root *string) error {
	name := s.Value("name")
	parent, hasParent := s.Attr("parent")
	attrs := map[string]string{}
	id, path, parentID := ".", clip(name), ""
	if !hasParent {
		*root = path
		attrs["name"] = name
	} else {
		parentID = godotres.Unquote(parent)
		id = name
		if parentID != "." {
			id = parentID + "/" + name
		}
		path = joinPath(*root, "/", clip(id))
		if *root == "" {
			path = clip(id)
		}
	}
	typ := s.Value("type")
	if inst, ok := s.Attr("instance"); ok {
		typ = p.render(inst)
	}
	if typ == "" {
		typ = "inherited"
	}
	for _, a := range s.Attrs {
		if !godotNodeAttrs[a.Key] {
			attrs[a.Key] = a.Raw
		}
	}
	return p.add(id, path, typ, parentID, attrs, s.Props)
}

func (p *godotParser) connection(s *godotres.Section, root string) error {
	from, to := s.Value("from"), s.Value("to")
	if root != "" {
		from, to = nodeLabel(from, root), nodeLabel(to, root)
	}
	label := fmt.Sprintf("%s %s → %s.%s", s.Value("signal"), from, to, s.Value("method"))
	attrs := map[string]string{}
	for _, a := range s.Attrs {
		if !godotConnectionAttrs[a.Key] {
			attrs[a.Key] = a.Raw
		}
	}
	return p.add("connection:"+s.Identity(), clip(label), "connection", "", attrs, nil)
}

func (p *godotParser) add(id, path, typ, parent string, attrs map[string]string, props []godotres.Prop) error {
	if len(p.scene.Objects) >= MaxSceneObjects {
		return fmt.Errorf("%w: more than %d objects", errSceneTooLarge, MaxSceneObjects)
	}
	for n, base := 2, id; p.ids[id]; n++ {
		id = base + "~" + strconv.Itoa(n)
	}
	p.ids[id] = true
	obj := SceneObject{ID: id, Path: path, Type: clip(typ), Parent: parent, Props: make(map[string]string, len(attrs)+len(props))}
	for k, v := range attrs {
		if err := p.set(obj.Props, k, p.render(v)); err != nil {
			return err
		}
	}
	for _, pr := range props {
		if err := p.set(obj.Props, pr.Key, p.render(pr.Raw)); err != nil {
			return err
		}
	}
	p.scene.Objects = append(p.scene.Objects, obj)
	return nil
}

func (p *godotParser) set(props map[string]string, key, value string) error {
	if _, ok := props[key]; !ok {
		p.props++
		if p.props > MaxSceneProps {
			return fmt.Errorf("%w: more than %d properties", errSceneTooLarge, MaxSceneProps)
		}
	}
	props[key] = value
	return nil
}

// render replaces ExtResource and SubResource references in a raw value
// with the resource path or sub-resource key, leaving strings untouched.
func (p *godotParser) render(raw string) string {
	if !strings.Contains(raw, "Resource(") {
		return clip(raw)
	}
	var b strings.Builder
	for i := 0; i < len(raw); {
		c := raw[i]
		if c == '"' {
			j := i + 1
			for j < len(raw) && raw[j] != '"' {
				if raw[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(raw))
			b.WriteString(raw[i:j])
			i = j
			continue
		}
		if repl, n := p.reference(raw[i:]); n > 0 && (i == 0 || !isIdentByte(raw[i-1])) {
			b.WriteString(repl)
			i += n
			continue
		}
		b.WriteByte(c)
		i++
	}
	return clip(b.String())
}

func (p *godotParser) reference(s string) (string, int) {
	var table map[string]string
	var prefix string
	switch {
	case strings.HasPrefix(s, "ExtResource("):
		table, prefix = p.ext, "ExtResource("
	case strings.HasPrefix(s, "SubResource("):
		table, prefix = p.sub, "SubResource("
	default:
		return "", 0
	}
	end := strings.IndexByte(s, ')')
	if end < 0 {
		return "", 0
	}
	id := godotres.Unquote(strings.TrimSpace(s[len(prefix):end]))
	if target, ok := table[id]; ok && target != "" {
		return target, end + 1
	}
	return "", 0
}

func nodeLabel(path, root string) string {
	if path == "." {
		return root
	}
	return path
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

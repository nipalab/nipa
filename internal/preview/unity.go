package preview

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"path"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	maxPropDepth   = 64
	maxPropValue   = 200
	unityPathDepth = 256
	maxPathLen     = 512
)

var unityClasses = map[int]string{
	1: "GameObject", 4: "Transform", 20: "Camera", 21: "Material", 23: "MeshRenderer",
	25: "Renderer", 28: "Texture2D", 29: "OcclusionCullingSettings", 33: "MeshFilter",
	43: "Mesh", 54: "Rigidbody", 64: "MeshCollider", 65: "BoxCollider", 74: "AnimationClip",
	82: "AudioSource", 81: "AudioListener", 91: "AnimatorController", 95: "Animator",
	104: "RenderSettings", 108: "Light", 111: "Animation", 114: "MonoBehaviour",
	115: "MonoScript", 135: "SphereCollider", 136: "CapsuleCollider", 137: "SkinnedMeshRenderer",
	143: "CharacterController", 157: "LightmapSettings", 196: "NavMeshSettings",
	198: "ParticleSystem", 199: "ParticleSystemRenderer", 212: "SpriteRenderer",
	221: "AnimatorOverrideController", 222: "CanvasRenderer", 223: "Canvas",
	224: "RectTransform", 225: "CanvasGroup", 1001: "PrefabInstance", 1101: "AnimatorStateTransition",
	1102: "AnimatorState", 1107: "AnimatorStateMachine", 134: "PhysicMaterial",
}

// Main-object file IDs of common asset types; references to them render as
// the bare asset path, other file IDs as path#fileID.
var unityMainIDs = map[string]bool{
	"11500000": true, "100100000": true, "2100000": true, "2800000": true, "7400000": true,
	"9100000": true, "11400000": true, "102900000": true, "13400000": true, "22100000": true,
}

var unityStructural = map[string]bool{"m_Component": true, "m_Children": true, "m_Father": true, "m_GameObject": true}

type unityDoc struct {
	class    int
	id       string
	stripped bool
	typ      string
	body     *yaml.Node
}

func (d *unityDoc) field(key string) *yaml.Node {
	if d.body == nil || d.body.Kind != yaml.MappingNode {
		return nil
	}
	return mapValue(d.body, key)
}

func (d *unityDoc) ref(key string) string {
	if n := d.field(key); n != nil {
		if id, _, ok := refOf(n); ok && id != "0" {
			return id
		}
	}
	return ""
}

func (d *unityDoc) scalar(key string) string {
	if n := d.field(key); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}

type unityParser struct {
	resolve func(string) string
	docs    map[string]*unityDoc
	order   []*unityDoc
	owner   map[string]string
	xform   map[string]string
	paths   map[string]string
	refs    map[string]bool
	props   int
}

// ParseUnityYAML reads a Unity text-serialized scene, prefab or asset.
// resolve maps an asset GUID to its project path; it may be nil, and an
// unresolved GUID renders as guid:<guid>.
func ParseUnityYAML(data []byte, resolve func(guid string) string) (*Scene, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !bytes.HasPrefix(data, []byte("%YAML")) {
		return nil, fmt.Errorf("%w: binary Unity asset, set Asset Serialization to Force Text", ErrUnsupported)
	}
	if resolve == nil {
		resolve = func(string) string { return "" }
	}
	p := &unityParser{resolve: resolve, docs: map[string]*unityDoc{}, owner: map[string]string{}, xform: map[string]string{}, paths: map[string]string{}, refs: map[string]bool{}}
	if err := p.split(data); err != nil {
		return nil, err
	}
	for _, d := range p.order {
		if d.class == 1 {
			for _, c := range seqItems(d.field("m_Component")) {
				if id, _, ok := refOf(mapValue(c, "component")); ok && id != "0" {
					p.owner[id] = d.id
					if p.isTransform(id) {
						p.xform[d.id] = id
					}
				}
			}
		}
	}
	scene := &Scene{Engine: "unity", Objects: make([]SceneObject, 0, len(p.order))}
	for _, d := range p.order {
		obj := SceneObject{ID: d.id, Type: p.typeOf(d), Path: p.path(d.id, 0), Parent: p.parent(d), Props: map[string]string{}}
		if err := p.flattenDoc(d, obj.Props); err != nil {
			return nil, err
		}
		scene.Objects = append(scene.Objects, obj)
	}
	scene.Refs = sortedSet(p.refs)
	return scene, nil
}

func (p *unityParser) split(data []byte) error {
	var cur *unityDoc
	var body []byte
	flush := func() error {
		if cur == nil {
			return nil
		}
		var n yaml.Node
		if err := yaml.Unmarshal(body, &n); err != nil {
			return fmt.Errorf("%w: object &%s: %v", errScene, cur.id, err)
		}
		if len(n.Content) == 1 && n.Content[0].Kind == yaml.MappingNode && len(n.Content[0].Content) >= 2 {
			cur.typ = n.Content[0].Content[0].Value
			cur.body = n.Content[0].Content[1]
		}
		if _, dup := p.docs[cur.id]; !dup {
			p.docs[cur.id] = cur
			p.order = append(p.order, cur)
		}
		return nil
	}
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i+1], data[i+1:]
		} else {
			data = nil
		}
		if !bytes.HasPrefix(line, []byte("--- ")) {
			if cur != nil {
				body = append(body, line...)
			}
			continue
		}
		if err := flush(); err != nil {
			return err
		}
		d, err := parseUnityHeader(strings.TrimSpace(string(line)))
		if err != nil {
			return err
		}
		if len(p.order) >= MaxSceneObjects {
			return fmt.Errorf("%w: more than %d objects", errSceneTooLarge, MaxSceneObjects)
		}
		cur, body = d, body[:0]
	}
	return flush()
}

func parseUnityHeader(line string) (*unityDoc, error) {
	f := strings.Fields(line)
	if len(f) < 3 || !strings.HasPrefix(f[1], "!u!") || !strings.HasPrefix(f[2], "&") {
		return nil, fmt.Errorf("%w: document header %q", errScene, line)
	}
	class, err := strconv.Atoi(f[1][3:])
	id := f[2][1:]
	if _, idErr := strconv.ParseInt(id, 10, 64); err != nil || idErr != nil {
		return nil, fmt.Errorf("%w: document header %q", errScene, line)
	}
	return &unityDoc{class: class, id: id, stripped: len(f) > 3 && f[3] == "stripped"}, nil
}

func (p *unityParser) className(d *unityDoc) string {
	if d.typ != "" {
		return d.typ
	}
	if name, ok := unityClasses[d.class]; ok {
		return name
	}
	return "Class" + strconv.Itoa(d.class)
}

func (p *unityParser) typeOf(d *unityDoc) string {
	if d.class == 114 {
		if _, g, ok := refOf(d.field("m_Script")); ok && g != "" {
			if res := p.resolve(g); res != "" {
				return strings.TrimSuffix(path.Base(res), path.Ext(res))
			}
		}
	}
	return p.className(d)
}

func (p *unityParser) gameObjectOf(d *unityDoc) string {
	if id := d.ref("m_GameObject"); id != "" {
		return id
	}
	return p.owner[d.id]
}

// parent returns the ID of the object d hangs under in the hierarchy.
func (p *unityParser) parent(d *unityDoc) string {
	if d.stripped {
		return d.ref("m_PrefabInstance")
	}
	switch {
	case d.class == 1:
		return p.transformParent(p.docs[p.xform[d.id]])
	case d.class == 1001:
		if mod := d.field("m_Modification"); mod != nil {
			if id, _, ok := refOf(mapValue(mod, "m_TransformParent")); ok && id != "0" {
				return p.transformTarget(id)
			}
		}
		return ""
	}
	return p.gameObjectOf(d)
}

func (p *unityParser) isTransform(id string) bool {
	d := p.docs[id]
	return d != nil && (d.class == 4 || d.class == 224)
}

func (p *unityParser) transformParent(t *unityDoc) string {
	if t == nil {
		return ""
	}
	if father := t.ref("m_Father"); father != "" {
		return p.transformTarget(father)
	}
	return ""
}

// transformTarget maps a transform to the object children are shown under:
// its game object, or the prefab instance of a stripped transform.
func (p *unityParser) transformTarget(id string) string {
	d := p.docs[id]
	if d == nil {
		return ""
	}
	if d.stripped {
		if inst := d.ref("m_PrefabInstance"); inst != "" {
			return inst
		}
		return id
	}
	if g := p.gameObjectOf(d); g != "" {
		return g
	}
	return id
}

func (p *unityParser) path(id string, depth int) string {
	if v, ok := p.paths[id]; ok {
		return v
	}
	d := p.docs[id]
	if d == nil {
		return "#" + id
	}
	if depth > unityPathDepth {
		return p.className(d)
	}
	p.paths[id] = "#" + id
	var out string
	switch {
	case d.stripped:
		out = p.under(d, depth, p.className(d)+" (prefab)")
	case d.class == 1 || d.class == 1001:
		out = p.under(d, depth, clip(p.nameOf(d)))
	default:
		if g := p.gameObjectOf(d); g != "" && p.docs[g] != nil {
			out = joinPath(p.path(g, depth+1), " > ", p.typeOf(d))
		} else if name := d.scalar("m_Name"); name != "" {
			out = name
		} else {
			out = p.typeOf(d)
		}
	}
	p.paths[id] = out
	return out
}

// joinPath keeps object paths bounded on very deep hierarchies by eliding
// the start of long parent paths.
func joinPath(parent, sep, name string) string {
	if len(parent) > maxPathLen {
		cut := len(parent) - maxPathLen
		for cut < len(parent) && !utf8RuneStart(parent[cut]) {
			cut++
		}
		parent = "…" + parent[cut:]
	}
	return parent + sep + name
}

func (p *unityParser) under(d *unityDoc, depth int, name string) string {
	if parent := p.parent(d); parent != "" && parent != d.id && p.docs[parent] != nil {
		if d.stripped {
			return joinPath(p.path(parent, depth+1), " > ", name)
		}
		return joinPath(p.path(parent, depth+1), "/", name)
	}
	return name
}

func (p *unityParser) nameOf(d *unityDoc) string {
	if d.class == 1001 {
		mod := d.field("m_Modification")
		for _, m := range seqItems(mapValue(mod, "m_Modifications")) {
			if n := mapValue(m, "propertyPath"); n != nil && n.Value == "m_Name" {
				if v := mapValue(m, "value"); v != nil && v.Value != "" {
					return v.Value
				}
			}
		}
		src := d.field("m_SourcePrefab")
		if mod != nil && src == nil {
			src = mapValue(mod, "m_SourcePrefab")
		}
		if _, g, ok := refOf(src); ok && g != "" {
			if res := p.resolve(g); res != "" {
				return strings.TrimSuffix(path.Base(res), path.Ext(res))
			}
		}
		return "PrefabInstance"
	}
	if name := d.scalar("m_Name"); name != "" {
		return name
	}
	return p.className(d)
}

func (p *unityParser) flattenDoc(d *unityDoc, props map[string]string) error {
	if d.body == nil || d.body.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(d.body.Content); i += 2 {
		key, val := d.body.Content[i].Value, d.body.Content[i+1]
		if unityStructural[key] && !d.stripped {
			continue
		}
		if d.class == 1001 && key == "m_Modification" && val.Kind == yaml.MappingNode {
			if err := p.flattenModification(val, props); err != nil {
				return err
			}
			continue
		}
		if err := p.flatten(key, val, props, 0); err != nil {
			return err
		}
	}
	return nil
}

func (p *unityParser) flattenModification(mod *yaml.Node, props map[string]string) error {
	for i := 0; i+1 < len(mod.Content); i += 2 {
		key, val := mod.Content[i].Value, mod.Content[i+1]
		if key != "m_Modifications" {
			if err := p.flatten("m_Modification."+key, val, props, 1); err != nil {
				return err
			}
			continue
		}
		for _, m := range seqItems(val) {
			target := "?"
			if id, g, ok := refOf(mapValue(m, "target")); ok {
				target = id
				if g != "" {
					p.dependency(g)
				}
			}
			prop := ""
			if n := mapValue(m, "propertyPath"); n != nil {
				prop = n.Value
			}
			value := ""
			if n := mapValue(m, "value"); n != nil && n.Kind == yaml.ScalarNode {
				value = n.Value
			}
			if n := mapValue(m, "objectReference"); n != nil {
				if id, _, ok := refOf(n); ok && id != "0" {
					value = p.renderRef(n)
				}
			}
			if err := p.set(props, "override "+target+"."+prop, clip(value)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *unityParser) set(props map[string]string, key, value string) error {
	if _, ok := props[key]; !ok {
		p.props++
		if p.props > MaxSceneProps {
			return fmt.Errorf("%w: more than %d properties", errSceneTooLarge, MaxSceneProps)
		}
	}
	props[key] = value
	return nil
}

func (p *unityParser) flatten(key string, n *yaml.Node, props map[string]string, depth int) error {
	if depth > maxPropDepth {
		return p.set(props, key, "…")
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return p.set(props, key, clip(n.Value))
	case yaml.AliasNode:
		return p.set(props, key, "*"+n.Value)
	case yaml.MappingNode:
		if _, _, ok := refOf(n); ok {
			return p.set(props, key, p.renderRef(n))
		}
		if len(n.Content) == 0 {
			return p.set(props, key, "{}")
		}
		if v, ok := flowScalars(n); ok {
			return p.set(props, key, v)
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := p.flatten(key+"."+n.Content[i].Value, n.Content[i+1], props, depth+1); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return p.set(props, key, "[]")
		}
		names := singleKeys(n)
		for i, item := range n.Content {
			if names != nil {
				if err := p.flatten(key+"."+names[i], item.Content[1], props, depth+1); err != nil {
					return err
				}
				continue
			}
			if err := p.flatten(key+"["+strconv.Itoa(i)+"]", item, props, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// singleKeys returns the item keys of a list of one-key maps (material
// properties, for example) when they are unique, so they key the values
// instead of indexes that shift on every insert.
func singleKeys(n *yaml.Node) []string {
	names := make([]string, len(n.Content))
	seen := map[string]bool{}
	for i, item := range n.Content {
		if item.Kind != yaml.MappingNode || len(item.Content) != 2 || item.Content[0].Kind != yaml.ScalarNode {
			return nil
		}
		name := item.Content[0].Value
		if seen[name] {
			return nil
		}
		seen[name] = true
		names[i] = name
	}
	return names
}

func flowScalars(n *yaml.Node) (string, bool) {
	if n.Style&yaml.FlowStyle == 0 || len(n.Content) > 16 {
		return "", false
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i+1].Kind != yaml.ScalarNode {
			return "", false
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n.Content[i].Value + ": " + n.Content[i+1].Value)
	}
	b.WriteByte('}')
	return clip(b.String()), true
}

// refOf reports whether n is a {fileID, guid?, type?} reference.
func refOf(n *yaml.Node) (fileID, guid string, ok bool) {
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) == 0 {
		return "", "", false
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		v := n.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return "", "", false
		}
		switch n.Content[i].Value {
		case "fileID":
			fileID, ok = v.Value, true
		case "guid":
			guid = v.Value
		case "type":
		default:
			return "", "", false
		}
	}
	return fileID, guid, ok
}

func (p *unityParser) renderRef(n *yaml.Node) string {
	id, g, _ := refOf(n)
	switch {
	case g == "" && id == "0":
		return "None"
	case g == "":
		if p.docs[id] != nil {
			return p.path(id, 0)
		}
		return "#" + id
	case strings.HasPrefix(g, "0000000000000000"):
		return "Built-in#" + id
	}
	target := p.dependency(g)
	if unityMainIDs[id] {
		return target
	}
	return target + "#" + id
}

func (p *unityParser) dependency(guid string) string {
	target := p.resolve(guid)
	if target == "" {
		target = "guid:" + guid
	}
	if !strings.HasPrefix(guid, "0000000000000000") {
		p.refs[target] = true
	}
	return target
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func seqItems(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

// clip shortens long values (mesh buffers, curves) while keeping a hash so a
// change past the cut still shows.
func clip(v string) string {
	if len(v) <= maxPropValue {
		return v
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(v))
	cut := maxPropValue
	for cut > 0 && !utf8RuneStart(v[cut]) {
		cut--
	}
	return fmt.Sprintf("%s… (%d bytes, %08x)", v[:cut], len(v), h.Sum32())
}

func utf8RuneStart(b byte) bool { return b&0xc0 != 0x80 }

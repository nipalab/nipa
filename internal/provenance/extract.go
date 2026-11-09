package provenance

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits bound what Extract reads from untrusted files. Zero fields take
// the defaults.
type Limits struct {
	MaxChunk     int64
	MaxInflate   int64
	MaxStore     int64
	MaxCBORDepth int
}

const (
	defaultMaxChunk   = 8 << 20
	defaultMaxInflate = 1 << 20
	defaultMaxStore   = 16 << 20
	defaultCBORDepth  = 32
	maxJPEGPackets    = 1024
	maxComfyHops      = 64
)

var (
	ErrNotPNG  = errors.New("not a PNG file")
	ErrNotJPEG = errors.New("not a JPEG file")

	pngSignature = []byte("\x89PNG\r\n\x1a\n")

	a1111Seed  = regexp.MustCompile(`(?:^|,)\s*Seed:\s*([0-9]+)`)
	a1111Model = regexp.MustCompile(`(?:^|,)\s*Model:\s*([^,]+)`)
)

func (l Limits) withDefaults() Limits {
	if l.MaxChunk <= 0 {
		l.MaxChunk = defaultMaxChunk
	}
	if l.MaxInflate <= 0 {
		l.MaxInflate = defaultMaxInflate
	}
	if l.MaxStore <= 0 {
		l.MaxStore = defaultMaxStore
	}
	if l.MaxCBORDepth <= 0 {
		l.MaxCBORDepth = defaultCBORDepth
	}
	return l
}

// Supported reports whether Extract understands the file by extension.
func Supported(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// Extract reads declared generator metadata from an asset. It returns false
// when the file carries none or its format is not supported. The record has
// no content hash or timestamp; callers add them.
func Extract(name string, r io.ReadSeeker, lim Limits) (*Record, bool, error) {
	lim = lim.withDefaults()
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return extractPNG(r, lim)
	case ".jpg", ".jpeg":
		return extractJPEG(r, lim)
	}
	return nil, false, nil
}

type pngMeta struct {
	text map[string]string
	c2pa []byte
}

func walkPNG(r io.ReadSeeker, lim Limits) (*pngMeta, error) {
	var sig [8]byte
	if _, err := io.ReadFull(r, sig[:]); err != nil || !bytes.Equal(sig[:], pngSignature) {
		return nil, ErrNotPNG
	}
	meta := &pngMeta{text: map[string]string{}}
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, fmt.Errorf("png: truncated before IEND: %w", io.ErrUnexpectedEOF)
		}
		length := int64(binary.BigEndian.Uint32(hdr[:4]))
		if length > 1<<31-1 {
			return nil, errors.New("png: invalid chunk length")
		}
		typ := string(hdr[4:])
		if typ == "IEND" {
			return meta, nil
		}
		wanted := typ == "tEXt" || typ == "zTXt" || typ == "iTXt" || typ == "caBX"
		if !wanted || length > lim.MaxChunk {
			if _, err := r.Seek(length+4, io.SeekCurrent); err != nil {
				return nil, err
			}
			continue
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("png: truncated %s chunk: %w", typ, io.ErrUnexpectedEOF)
		}
		if _, err := r.Seek(4, io.SeekCurrent); err != nil {
			return nil, err
		}
		if typ == "caBX" {
			meta.c2pa = data
			continue
		}
		if key, text, ok := pngText(typ, data, lim); ok && (key == "prompt" || key == "parameters") {
			meta.text[key] = text
		}
	}
}

func pngText(typ string, data []byte, lim Limits) (string, string, bool) {
	key, rest, ok := bytes.Cut(data, []byte{0})
	if !ok || len(key) == 0 || len(key) > 79 {
		return "", "", false
	}
	switch typ {
	case "tEXt":
		return latin1(key), latin1(rest), true
	case "zTXt":
		if len(rest) < 1 || rest[0] != 0 {
			return "", "", false
		}
		text, ok := inflate(rest[1:], lim.MaxInflate)
		if !ok {
			return "", "", false
		}
		return latin1(key), latin1(text), true
	}
	if len(rest) < 2 {
		return "", "", false
	}
	compressed, method := rest[0], rest[1]
	rest = rest[2:]
	for range 2 {
		if _, rest, ok = bytes.Cut(rest, []byte{0}); !ok {
			return "", "", false
		}
	}
	if compressed == 1 {
		if method != 0 {
			return "", "", false
		}
		if rest, ok = inflate(rest, lim.MaxInflate); !ok {
			return "", "", false
		}
	}
	if !utf8.Valid(rest) {
		return "", "", false
	}
	return latin1(key), string(rest), true
}

func inflate(data []byte, limit int64) ([]byte, bool) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil || int64(len(out)) > limit {
		return nil, false
	}
	return out, true
}

func latin1(b []byte) string {
	if utf8.Valid(b) && !bytes.ContainsFunc(b, func(r rune) bool { return r >= 0x80 }) {
		return string(b)
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes)
}

func extractPNG(r io.ReadSeeker, lim Limits) (*Record, bool, error) {
	meta, err := walkPNG(r, lim)
	if err != nil {
		return nil, false, err
	}
	var c2pa *Record
	var c2paErr error
	if meta.c2pa != nil {
		c2pa, _, c2paErr = parseC2PA(meta.c2pa, lim)
	}
	rec := textRecord(meta.text)
	switch {
	case rec != nil:
		if c2pa != nil {
			rec.DigitalSourceType = c2pa.DigitalSourceType
		}
		return rec, true, nil
	case c2pa != nil:
		return c2pa, true, nil
	case c2paErr != nil:
		return nil, false, c2paErr
	}
	return nil, false, nil
}

func textRecord(text map[string]string) *Record {
	if p, ok := text["prompt"]; ok {
		if rec := parseComfyUI(p); rec != nil {
			return rec
		}
	}
	if p, ok := text["parameters"]; ok {
		return parseA1111(p)
	}
	return nil
}

func parseA1111(text string) *Record {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	end := len(lines)
	params := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "Steps:") {
			end, params = i, lines[i]
			break
		}
	}
	for i := range end {
		if strings.HasPrefix(lines[i], "Negative prompt:") {
			end = i
			break
		}
	}
	rec := &Record{Tool: "Automatic1111", Source: SourcePNGText}
	if prompt := strings.TrimSpace(strings.Join(lines[:end], "\n")); prompt != "" {
		rec.PromptSHA256 = PromptHash(prompt)
	}
	if m := a1111Seed.FindStringSubmatch(params); m != nil {
		if seed, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			rec.Seed = &seed
		}
	}
	if m := a1111Model.FindStringSubmatch(params); m != nil {
		rec.Model = cleanText(m[1])
	}
	return rec
}

type comfyNode struct {
	ClassType string                     `json:"class_type"`
	Inputs    map[string]json.RawMessage `json:"inputs"`
}

func parseComfyUI(text string) *Record {
	var nodes map[string]comfyNode
	if json.Unmarshal([]byte(text), &nodes) != nil || len(nodes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(nodes))
	for id, n := range nodes {
		if n.ClassType == "" {
			return nil
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return nodeIDLess(ids[i], ids[j]) })
	rec := &Record{Tool: "ComfyUI", Source: SourcePNGText}
	for _, id := range ids {
		n := nodes[id]
		if rec.Model == "" && strings.HasPrefix(n.ClassType, "CheckpointLoader") {
			var ckpt string
			if json.Unmarshal(n.Inputs["ckpt_name"], &ckpt) == nil {
				rec.Model = cleanText(ckpt)
			}
		}
	}
	for _, id := range ids {
		n := nodes[id]
		if !strings.HasPrefix(n.ClassType, "KSampler") {
			continue
		}
		for _, key := range []string{"seed", "noise_seed"} {
			var seed json.Number
			if raw, ok := n.Inputs[key]; ok && json.Unmarshal(raw, &seed) == nil {
				if v, err := strconv.ParseUint(seed.String(), 10, 64); err == nil {
					rec.Seed = &v
					break
				}
			}
		}
		if prompt, ok := comfyPositive(nodes, n.Inputs["positive"]); ok {
			rec.PromptSHA256 = PromptHash(prompt)
		}
		break
	}
	return rec
}

func comfyLink(raw json.RawMessage) (string, bool) {
	var link []json.RawMessage
	if json.Unmarshal(raw, &link) != nil || len(link) != 2 {
		return "", false
	}
	var id string
	if json.Unmarshal(link[0], &id) == nil {
		return id, true
	}
	var num json.Number
	if json.Unmarshal(link[0], &num) == nil {
		return num.String(), true
	}
	return "", false
}

// comfyPositive follows the sampler's positive input to the first
// CLIPTextEncode node with literal text.
func comfyPositive(nodes map[string]comfyNode, positive json.RawMessage) (string, bool) {
	start, ok := comfyLink(positive)
	if !ok {
		return "", false
	}
	queue, seen := []string{start}, map[string]bool{start: true}
	for hops := 0; len(queue) > 0 && hops < maxComfyHops; hops++ {
		id := queue[0]
		queue = queue[1:]
		n, ok := nodes[id]
		if !ok {
			continue
		}
		if strings.HasPrefix(n.ClassType, "CLIPTextEncode") {
			var text string
			if json.Unmarshal(n.Inputs["text"], &text) == nil {
				return text, true
			}
		}
		keys := make([]string, 0, len(n.Inputs))
		for k := range n.Inputs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if next, ok := comfyLink(n.Inputs[k]); ok && !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return "", false
}

func nodeIDLess(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		return ai < bi
	case aerr == nil:
		return true
	case berr == nil:
		return false
	}
	return a < b
}

func extractJPEG(r io.ReadSeeker, lim Limits) (*Record, bool, error) {
	stores, err := walkJPEG(r, lim)
	if err != nil {
		return nil, false, err
	}
	var firstErr error
	for _, store := range stores {
		rec, ok, err := parseC2PA(store, lim)
		if ok {
			return rec, true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return nil, false, firstErr
}

type jpegPacket struct {
	seq  uint32
	data []byte
}

// walkJPEG collects the APP11 JUMBF stores before the first scan, one per
// box instance, reassembled in sequence order.
func walkJPEG(r io.ReadSeeker, lim Limits) ([][]byte, error) {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi != [2]byte{0xFF, 0xD8} {
		return nil, ErrNotJPEG
	}
	instances := map[uint16][]jpegPacket{}
	var order []uint16
	var total int64
	packets := 0
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, one); err != nil {
			return nil, fmt.Errorf("jpeg: truncated: %w", io.ErrUnexpectedEOF)
		}
		if one[0] != 0xFF {
			return nil, errors.New("jpeg: expected a marker")
		}
		marker := byte(0xFF)
		for marker == 0xFF {
			if _, err := io.ReadFull(r, one); err != nil {
				return nil, fmt.Errorf("jpeg: truncated: %w", io.ErrUnexpectedEOF)
			}
			marker = one[0]
		}
		if marker == 0xDA || marker == 0xD9 {
			break
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			continue
		}
		var lenBuf [2]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return nil, fmt.Errorf("jpeg: truncated: %w", io.ErrUnexpectedEOF)
		}
		length := int64(binary.BigEndian.Uint16(lenBuf[:])) - 2
		if length < 0 {
			return nil, errors.New("jpeg: invalid segment length")
		}
		if marker != 0xEB {
			if _, err := r.Seek(length, io.SeekCurrent); err != nil {
				return nil, err
			}
			continue
		}
		seg := make([]byte, length)
		if _, err := io.ReadFull(r, seg); err != nil {
			return nil, fmt.Errorf("jpeg: truncated APP11: %w", io.ErrUnexpectedEOF)
		}
		if len(seg) < 8 || seg[0] != 'J' || seg[1] != 'P' {
			continue
		}
		if packets++; packets > maxJPEGPackets {
			return nil, errors.New("jpeg: too many APP11 segments")
		}
		if total += int64(len(seg)); total > lim.MaxStore {
			return nil, errors.New("jpeg: JUMBF data too large")
		}
		en := binary.BigEndian.Uint16(seg[2:])
		if _, ok := instances[en]; !ok {
			order = append(order, en)
		}
		instances[en] = append(instances[en], jpegPacket{seq: binary.BigEndian.Uint32(seg[4:]), data: seg[8:]})
	}
	out := make([][]byte, 0, len(order))
	for _, en := range order {
		if store, ok := assemble(instances[en]); ok {
			out = append(out, store)
		}
	}
	return out, nil
}

func assemble(packets []jpegPacket) ([]byte, bool) {
	sort.SliceStable(packets, func(i, j int) bool { return packets[i].seq < packets[j].seq })
	var buf bytes.Buffer
	for i, p := range packets {
		if i == 0 {
			buf.Write(p.data)
			continue
		}
		skip := 8
		if len(p.data) >= 4 && binary.BigEndian.Uint32(p.data) == 1 {
			skip = 16
		}
		if len(p.data) < skip {
			return nil, false
		}
		buf.Write(p.data[skip:])
	}
	return buf.Bytes(), buf.Len() > 0
}

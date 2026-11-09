package provenance

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

var errJUMBF = errors.New("invalid JUMBF")

const maxJUMBFBoxes = 4096

type box struct {
	typ     string
	payload []byte
}

// boxes splits data into ISO BMFF style boxes.
func boxes(data []byte) ([]box, error) {
	var out []box
	for len(data) > 0 {
		if len(out) >= maxJUMBFBoxes {
			return nil, fmt.Errorf("%w: too many boxes", errJUMBF)
		}
		if len(data) < 8 {
			return nil, fmt.Errorf("%w: truncated box header", errJUMBF)
		}
		size := uint64(binary.BigEndian.Uint32(data))
		typ := string(data[4:8])
		hdr := uint64(8)
		switch size {
		case 0:
			size = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return nil, fmt.Errorf("%w: truncated box header", errJUMBF)
			}
			size, hdr = binary.BigEndian.Uint64(data[8:]), 16
		}
		if size < hdr || size > uint64(len(data)) {
			return nil, fmt.Errorf("%w: box %q size %d out of range", errJUMBF, typ, size)
		}
		out = append(out, box{typ: typ, payload: data[hdr:size]})
		data = data[size:]
	}
	return out, nil
}

type superbox struct {
	label   string
	content []box
}

// parseSuperbox decodes a jumb payload: a jumd description box followed by
// content boxes.
func parseSuperbox(payload []byte) (*superbox, error) {
	children, err := boxes(payload)
	if err != nil {
		return nil, err
	}
	if len(children) == 0 || children[0].typ != "jumd" {
		return nil, fmt.Errorf("%w: missing description box", errJUMBF)
	}
	d := children[0].payload
	if len(d) < 17 {
		return nil, fmt.Errorf("%w: short description box", errJUMBF)
	}
	sb := &superbox{content: children[1:]}
	if d[16]&0x02 != 0 {
		rest := d[17:]
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, fmt.Errorf("%w: unterminated label", errJUMBF)
		}
		sb.label = string(rest[:end])
	}
	return sb, nil
}

func (s *superbox) children() []*superbox {
	var out []*superbox
	for _, b := range s.content {
		if b.typ != "jumb" {
			continue
		}
		if child, err := parseSuperbox(b.payload); err == nil {
			out = append(out, child)
		}
	}
	return out
}

func (s *superbox) lastChild() *superbox {
	for i := len(s.content) - 1; i >= 0; i-- {
		if s.content[i].typ != "jumb" {
			continue
		}
		if child, err := parseSuperbox(s.content[i].payload); err == nil {
			return child
		}
	}
	return nil
}

func (s *superbox) cbor() []byte {
	for _, b := range s.content {
		if b.typ == "cbor" {
			return b.payload
		}
	}
	return nil
}

func labelIs(label, name string) bool {
	return label == name || strings.HasPrefix(label, name+".")
}

const (
	trainedMedia   = "trainedAlgorithmicMedia"
	compositeMedia = "compositeWithTrainedAlgorithmicMedia"
	maxLabelText   = 256
)

// parseC2PA reads the active (last) manifest of a C2PA JUMBF store: the
// claim generator and an AI digital source type from the actions assertion.
// Signatures are not verified.
func parseC2PA(store []byte, lim Limits) (*Record, bool, error) {
	top, err := boxes(store)
	if err != nil {
		return nil, false, err
	}
	if len(top) == 0 || top[0].typ != "jumb" {
		return nil, false, fmt.Errorf("%w: not a JUMBF superbox", errJUMBF)
	}
	root, err := parseSuperbox(top[0].payload)
	if err != nil {
		return nil, false, err
	}
	if root.label != "c2pa" {
		return nil, false, nil
	}
	active := root.lastChild()
	if active == nil {
		return nil, false, nil
	}
	var claim map[string]any
	var sourceType string
	for _, sb := range active.children() {
		switch {
		case labelIs(sb.label, "c2pa.claim"):
			v, err := decodeCBOR(sb.cbor(), lim.MaxCBORDepth)
			if err != nil {
				return nil, false, err
			}
			claim, _ = v.(map[string]any)
		case sb.label == "c2pa.assertions":
			for _, a := range sb.children() {
				if !labelIs(a.label, "c2pa.actions") {
					continue
				}
				v, err := decodeCBOR(a.cbor(), lim.MaxCBORDepth)
				if err != nil {
					return nil, false, err
				}
				if t := aiSourceType(v); t != "" && sourceType == "" {
					sourceType = t
				}
			}
		}
	}
	if claim == nil {
		return nil, false, nil
	}
	verified := false
	rec := &Record{Tool: claimGenerator(claim), DigitalSourceType: sourceType, Source: SourceC2PA, C2PAVerified: &verified}
	return rec, true, nil
}

func claimGenerator(claim map[string]any) string {
	if s, ok := claim["claim_generator"].(string); ok && strings.TrimSpace(s) != "" {
		return cleanText(s)
	}
	info := claim["claim_generator_info"]
	if list, ok := info.([]any); ok && len(list) > 0 {
		info = list[0]
	}
	if m, ok := info.(map[string]any); ok {
		if s, ok := m["name"].(string); ok {
			return cleanText(s)
		}
	}
	return ""
}

func aiSourceType(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	actions, _ := m["actions"].([]any)
	for _, a := range actions {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		s, _ := am["digitalSourceType"].(string)
		switch s[strings.LastIndexByte(s, '/')+1:] {
		case trainedMedia:
			return trainedMedia
		case compositeMedia:
			return compositeMedia
		}
	}
	return ""
}

// cleanText keeps a short single-line copy of untrusted metadata text.
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > maxLabelText {
		s = strings.ToValidUTF8(s[:maxLabelText], "")
	}
	return s
}

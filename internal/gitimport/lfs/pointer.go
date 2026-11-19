package lfs

import (
	"strconv"
	"strings"
)

const MaxPointerSize = 1024

const (
	specV1     = "https://git-lfs.github.com/spec/v1"
	specLegacy = "https://hawser.github.com/spec/v1"
)

type Pointer struct {
	OID  string
	Size int64
}

func ParsePointer(data []byte) (Pointer, bool) {
	if len(data) == 0 || len(data) > MaxPointerSize {
		return Pointer{}, false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return Pointer{}, false
	}
	key, value, ok := strings.Cut(lines[0], " ")
	if !ok || key != "version" || (value != specV1 && value != specLegacy) {
		return Pointer{}, false
	}
	var p Pointer
	var haveOID, haveSize bool
	prev := ""
	for _, line := range lines[1:] {
		key, value, ok := strings.Cut(line, " ")
		if !ok || key == "" || value == "" || key < prev {
			return Pointer{}, false
		}
		prev = key
		switch key {
		case "oid":
			oid, ok := strings.CutPrefix(value, "sha256:")
			if !ok || !ValidOID(oid) || haveOID {
				return Pointer{}, false
			}
			p.OID, haveOID = oid, true
		case "size":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 || haveSize || strings.HasPrefix(value, "+") {
				return Pointer{}, false
			}
			p.Size, haveSize = n, true
		default:
			if !strings.HasPrefix(key, "ext-") {
				return Pointer{}, false
			}
		}
	}
	if !haveOID || !haveSize {
		return Pointer{}, false
	}
	return p, true
}

func ValidOID(oid string) bool {
	if len(oid) != 64 {
		return false
	}
	for i := 0; i < len(oid); i++ {
		c := oid[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (p Pointer) String() string {
	return "version " + specV1 + "\noid sha256:" + p.OID + "\nsize " + strconv.FormatInt(p.Size, 10) + "\n"
}

package godotmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzUID(f *testing.F) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		f.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte("[remap]\nuid=\"uid://\"\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		uid := UID(data)
		if uid == "" {
			return
		}
		if !strings.HasPrefix(uid, uidScheme) || validUID(uid) != uid {
			t.Fatalf("invalid uid %q", uid)
		}
	})
}

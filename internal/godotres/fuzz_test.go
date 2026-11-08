package godotres

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*"))
	for _, name := range files {
		if data, err := os.ReadFile(name); err == nil {
			f.Add(data)
		}
	}
	f.Add([]byte("[a b=[1,\n2] c=\"x y\"]\nk = {\n\"a\": (1)\n}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			return
		}
		out := Write(file)
		if !bytes.Equal(out, data) {
			t.Fatalf("round trip changed the input:\n%q\n%q", data, out)
		}
		again, err := Parse(out)
		if err != nil {
			t.Fatalf("written output does not parse: %v", err)
		}
		if !reflect.DeepEqual(file, again) {
			t.Fatalf("reparse differs")
		}
	})
}

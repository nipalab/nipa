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

func FuzzMerge3(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*"))
	var seeds [][]byte
	for _, name := range files {
		if data, err := os.ReadFile(name); err == nil {
			seeds = append(seeds, data)
		}
	}
	for i, a := range seeds {
		f.Add(a, seeds[(i+1)%len(seeds)])
		f.Add(a, bytes.Replace(a, []byte("\n\n"), []byte("\n\n[node name=\"Z\" type=\"Node\" parent=\".\"]\n\n"), 1))
	}
	f.Fuzz(func(t *testing.T, base, ours []byte) {
		if _, err := Parse(bytes.TrimPrefix(base, bom)); err != nil {
			return
		}
		if _, err := Parse(bytes.TrimPrefix(ours, bom)); err != nil {
			return
		}
		same, conflicts, err := Merge3(ours, ours, ours)
		if err != nil || len(conflicts) != 0 || !bytes.Equal(same, ours) {
			t.Fatalf("Merge3(x, x, x) != x: %v %v\n%q\n%q", err, conflicts, ours, same)
		}
		oneSided, conflicts, err := Merge3(base, ours, base)
		if err != nil || len(conflicts) != 0 || !bytes.Equal(oneSided, ours) {
			t.Fatalf("Merge3(b, o, b) != o: %v %v\n%q\n%q", err, conflicts, ours, oneSided)
		}
		merged, _, err := Merge3(base, base, ours)
		if err != nil {
			t.Fatalf("Merge3(b, b, t): %v", err)
		}
		if _, err := Parse(merged); err != nil {
			t.Fatalf("merged output does not parse: %v\n%q", err, merged)
		}
	})
}

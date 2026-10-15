package chunker

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func benchData(size int, text bool) []byte {
	if text {
		line := []byte("asset metadata record: name=texture_0001 compression=none size=4096 checksum=deadbeef\n")
		buf := bytes.NewBuffer(make([]byte, 0, size))
		for buf.Len() < size {
			_, _ = buf.Write(line)
		}
		return buf.Bytes()[:size]
	}
	data := make([]byte, size)
	r := rand.New(rand.NewSource(42))
	_, _ = r.Read(data)
	return data
}

func BenchmarkEncodeText(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20, 32 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			data := benchData(size, true)
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := EncodeBytes(data, "docs/notes.txt", false, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEncodePacked(b *testing.B) {
	for _, size := range []int{16 << 20, 64 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			data := benchData(size, false)
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := EncodeBytes(data, "textures/hero_diffuse.png", true, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkEncodeUnknownBinary(b *testing.B) {
	data := benchData(16<<20, false)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := EncodeBytes(data, "assets/blob.bin", true, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScanRaw(b *testing.B) {
	data := benchData(16<<20, false)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := Scan(bytes.NewReader(data), func(Chunk) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSum4MiB(b *testing.B) {
	data := benchData(4<<20, false)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Sum(data)
	}
}

// Command media writes the video, font and embedded-thumbnail fixtures in
// internal/preview/testdata/media:
//
//	go run ./internal/preview/testdata/gen/media internal/preview/testdata/media
package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/image/font/gofont/goregular"
)

func main() {
	dir := "internal/preview/testdata/media"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	thumb := pattern(8, 6)
	files := map[string][]byte{
		"clip.mp4":         mp4(false),
		"clip.mov":         mp4(true),
		"clip.webm":        matroska("webm", "V_VP9", "A_OPUS"),
		"clip.mkv":         matroska("matroska", "V_MPEG4/ISO/AVC", "A_AAC"),
		"scene.blend":      blend(blendOld, binary.LittleEndian, 8, thumb),
		"scene_be32.blend": blend(blendOld, binary.BigEndian, 4, thumb),
		"scene_v5.blend":   blend(blendLarge, binary.LittleEndian, 8, thumb),
		"scene_gz.blend":   gz(blend(blendOld, binary.LittleEndian, 8, thumb)),
		"scene_zstd.blend": zst(blend(blendOld, binary.LittleEndian, 8, thumb)),
		"empty.blend":      blend(blendOld, binary.LittleEndian, 8, nil),
		"mesh.uasset":      unreal(thumb, false),
		"level.umap":       unreal(thumb, true),
		"regular.woff":     woff(goregular.TTF),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func pattern(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 40), B: 200, A: 255})
		}
	}
	return img
}

func box(typ string, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, typ...), body...)
}

func u16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }
func zeros(n int) []byte  { return make([]byte, n) }

func timeBox(typ string, v1 bool, scale uint32, duration uint64, extra int) []byte {
	if v1 {
		return box(typ, []byte{1, 0, 0, 0}, u64(0), u64(0), u32(scale), u64(duration), zeros(extra))
	}
	return box(typ, []byte{0, 0, 0, 0}, u32(0), u32(0), u32(scale), u32(uint32(duration)), zeros(extra))
}

func tkhd(v1 bool, w, h uint32) []byte {
	if v1 {
		return box("tkhd", []byte{1, 0, 0, 3}, u64(0), u64(0), u32(1), u32(0), u64(0), zeros(8), zeros(8), zeros(36), u32(w<<16), u32(h<<16))
	}
	return box("tkhd", []byte{0, 0, 0, 3}, u32(0), u32(0), u32(1), u32(0), u32(0), zeros(8), zeros(8), zeros(36), u32(w<<16), u32(h<<16))
}

func track(v1 bool, handler string, entry []byte, w, h, scale uint32, duration uint64, samples uint32) []byte {
	hdlr := box("hdlr", zeros(4), zeros(4), []byte(handler), zeros(12), []byte("handler\x00"))
	stsd := box("stsd", zeros(4), u32(1), entry)
	stts := box("stts", zeros(4), u32(2), u32(samples-1), u32(uint32(duration)/samples), u32(1), u32(uint32(duration)/samples))
	stbl := box("stbl", stsd, stts)
	minf := box("minf", stbl)
	mdia := box("mdia", timeBox("mdhd", v1, scale, duration, 4), hdlr, minf)
	return box("trak", tkhd(v1, w, h), mdia)
}

func mp4(mov bool) []byte {
	brand := "isom"
	if mov {
		brand = "qt  "
	}
	video := box("avc1", zeros(6), u16(1), zeros(16), u16(320), u16(240), zeros(50))
	audio := box("mp4a", zeros(6), u16(1), zeros(8), u16(2), u16(16), zeros(4), u32(48000<<16))
	moov := box("moov",
		timeBox("mvhd", mov, 1000, 2000, 80),
		track(mov, "vide", video, 320, 240, 12800, 25600, 50),
		track(mov, "soun", audio, 0, 0, 48000, 96000, 94),
	)
	payload := bytes.Repeat([]byte{0xAB}, 4096)
	mdat := box("mdat", payload)
	if mov {
		mdat = append(append(u32(1), "mdat"...), u64(uint64(16+len(payload)))...)
		mdat = append(mdat, payload...)
	}
	return bytes.Join([][]byte{box("ftyp", []byte(brand), u32(0), []byte(brand)), mdat, moov}, nil)
}

func ebmlID(id uint32) []byte {
	switch {
	case id >= 1<<24:
		return u32(id)
	case id >= 1<<16:
		return u32(id)[1:]
	case id >= 1<<8:
		return u16(uint16(id))
	}
	return []byte{byte(id)}
}

func el(id uint32, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	size := u64(uint64(len(body)) | 1<<56)
	return append(append(ebmlID(id), size...), body...)
}

func uintEl(id uint32, v uint64) []byte { return el(id, u64(v)) }
func floatEl(id uint32, v float64) []byte {
	return el(id, u64(math.Float64bits(v)))
}

func matroska(docType, video, audio string) []byte {
	header := el(0x1A45DFA3, el(0x4286, []byte{1}), el(0x4282, []byte(docType)))
	info := el(0x1549A966, uintEl(0x2AD7B1, 1000000), floatEl(0x4489, 2500))
	tracks := el(0x1654AE6B,
		el(0xAE, uintEl(0xD7, 1), uintEl(0x83, 1), el(0x86, []byte(video)), uintEl(0x23E383, 41666667),
			el(0xE0, uintEl(0xB0, 1280), uintEl(0xBA, 720))),
		el(0xAE, uintEl(0xD7, 2), uintEl(0x83, 2), el(0x86, []byte(audio)),
			el(0xE1, floatEl(0xB5, 44100), uintEl(0x9F, 2))),
	)
	cluster := el(0x1F43B675, uintEl(0xE7, 0), el(0xA3, bytes.Repeat([]byte{0xCD}, 2048)))
	segment := append(append(ebmlID(0x18538067), 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF), bytes.Join([][]byte{info, tracks, cluster}, nil)...)
	return append(header, segment...)
}

type blendKind int

const (
	blendOld blendKind = iota
	blendLarge
)

func blend(kind blendKind, order binary.ByteOrder, pointer int, thumb *image.NRGBA) []byte {
	var out bytes.Buffer
	endian := byte('v')
	if order == binary.BigEndian {
		endian = 'V'
	}
	if kind == blendLarge {
		out.WriteString("BLENDER17-01")
		out.WriteByte(endian)
		out.WriteString("0500")
	} else {
		out.WriteString("BLENDER")
		if pointer == 4 {
			out.WriteByte('_')
		} else {
			out.WriteByte('-')
		}
		out.WriteByte(endian)
		out.WriteString("405")
	}
	block := func(code string, data []byte) {
		out.WriteString(code)
		if kind == blendLarge {
			_ = binary.Write(&out, order, int32(0))
			_ = binary.Write(&out, order, uint64(0xDEADBEEF))
			_ = binary.Write(&out, order, int64(len(data)))
			_ = binary.Write(&out, order, int64(1))
		} else {
			_ = binary.Write(&out, order, int32(len(data)))
			out.Write(make([]byte, pointer))
			_ = binary.Write(&out, order, int32(0))
			_ = binary.Write(&out, order, int32(1))
		}
		out.Write(data)
	}
	block("REND", make([]byte, 72))
	if thumb != nil {
		w, h := thumb.Rect.Dx(), thumb.Rect.Dy()
		var data bytes.Buffer
		_ = binary.Write(&data, order, int32(w))
		_ = binary.Write(&data, order, int32(h))
		for y := h - 1; y >= 0; y-- {
			data.Write(thumb.Pix[y*thumb.Stride : y*thumb.Stride+w*4])
		}
		block("TEST", data.Bytes())
	}
	block("GLOB", make([]byte, 128))
	block("ENDB", nil)
	return out.Bytes()
}

func gz(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func zst(data []byte) []byte {
	enc, _ := zstd.NewWriter(nil)
	defer enc.Close()
	return enc.EncodeAll(data, nil)
}

func le32(v int32) []byte { return binary.LittleEndian.AppendUint32(nil, uint32(v)) }

func unreal(thumb *image.NRGBA, asJPEG bool) []byte {
	var img bytes.Buffer
	w, h := thumb.Rect.Dx(), thumb.Rect.Dy()
	if asJPEG {
		_ = jpeg.Encode(&img, thumb, &jpeg.Options{Quality: 90})
		h = -h
	} else {
		_ = png.Encode(&img, thumb)
	}
	var out bytes.Buffer
	out.Write(le32(-0x61D57C3F))
	out.Write(le32(-8))
	out.Write(make([]byte, 200))
	out.Write([]byte("\x89PNG\r\n\x1a\n not a thumbnail"))
	out.Write(le32(int32(w)))
	out.Write(le32(int32(h)))
	out.Write(le32(int32(img.Len())))
	out.Write(img.Bytes())
	out.Write(make([]byte, 64))
	return out.Bytes()
}

func woff(ttf []byte) []byte {
	numTables := int(binary.BigEndian.Uint16(ttf[4:6]))
	type entry struct {
		tag, checksum   uint32
		comp            []byte
		origLen, offset uint32
	}
	entries := make([]entry, numTables)
	offset := uint32(44 + 20*numTables)
	for i := range entries {
		rec := ttf[12+16*i:]
		tag, sum := binary.BigEndian.Uint32(rec[0:4]), binary.BigEndian.Uint32(rec[4:8])
		off, length := binary.BigEndian.Uint32(rec[8:12]), binary.BigEndian.Uint32(rec[12:16])
		table := ttf[off : off+length]
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		_, _ = zw.Write(table)
		_ = zw.Close()
		comp := buf.Bytes()
		if len(comp) >= len(table) {
			comp = table
		}
		entries[i] = entry{tag: tag, checksum: sum, comp: comp, origLen: length, offset: offset}
		offset += uint32(len(comp)+3) &^ 3
	}
	var out bytes.Buffer
	out.WriteString("wOFF")
	out.Write(ttf[0:4])
	out.Write(u32(offset))
	out.Write(u16(uint16(numTables)))
	out.Write(u16(0))
	out.Write(u32(uint32(len(ttf))))
	out.Write(zeros(24))
	for _, e := range entries {
		out.Write(u32(e.tag))
		out.Write(u32(e.offset))
		out.Write(u32(uint32(len(e.comp))))
		out.Write(u32(e.origLen))
		out.Write(u32(e.checksum))
	}
	for _, e := range entries {
		out.Write(e.comp)
		for out.Len()%4 != 0 {
			out.WriteByte(0)
		}
	}
	return out.Bytes()
}

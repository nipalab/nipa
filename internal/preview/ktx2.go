package preview

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

const (
	ktx2HeaderSize     = 80
	ktx2SuperNone      = 0
	ktx2SuperBasisLZ   = 1
	ktx2SuperZstd      = 2
	ktx2SuperZlib      = 3
	ktx2MaxLevelBytes  = 1 << 30
	ktx2LevelIndexSize = 24
)

var ktx2Magic = []byte{0xAB, 'K', 'T', 'X', ' ', '2', '0', 0xBB, '\r', '\n', 0x1A, '\n'}

var errKTX2Header = errors.New("preview: invalid ktx2 header")

var vkFormats = map[uint32]pixelFormat{
	9:   channelFormat("R8_UNORM", 1, [4]int{0, -1, -1, -1}, true),
	15:  channelFormat("R8_SRGB", 1, [4]int{0, -1, -1, -1}, true),
	16:  channelFormat("R8G8_UNORM", 2, [4]int{0, 1, -1, -1}, false),
	23:  channelFormat("R8G8B8_UNORM", 3, [4]int{0, 1, 2, -1}, false),
	29:  channelFormat("R8G8B8_SRGB", 3, [4]int{0, 1, 2, -1}, false),
	37:  channelFormat("R8G8B8A8_UNORM", 4, [4]int{0, 1, 2, 3}, false),
	43:  channelFormat("R8G8B8A8_SRGB", 4, [4]int{0, 1, 2, 3}, false),
	44:  channelFormat("B8G8R8A8_UNORM", 4, [4]int{2, 1, 0, 3}, false),
	50:  channelFormat("B8G8R8A8_SRGB", 4, [4]int{2, 1, 0, 3}, false),
	76:  floatFormat("R16_SFLOAT", 1, 2),
	83:  floatFormat("R16G16_SFLOAT", 2, 2),
	91:  rgba16,
	97:  floatFormat("R16G16B16A16_SFLOAT", 4, 2),
	100: floatFormat("R32_SFLOAT", 1, 4),
	103: floatFormat("R32G32_SFLOAT", 2, 4),
	109: floatFormat("R32G32B32A32_SFLOAT", 4, 4),
	122: r11g11b10Float,
	123: rgb9e5Float,
	131: blockFormat("BC1_RGB_UNORM", 8, false, decodeBC1),
	132: blockFormat("BC1_RGB_SRGB", 8, false, decodeBC1),
	133: blockFormat("BC1_RGBA_UNORM", 8, true, decodeBC1),
	134: blockFormat("BC1_RGBA_SRGB", 8, true, decodeBC1),
	135: blockFormat("BC2_UNORM", 16, true, decodeBC2),
	136: blockFormat("BC2_SRGB", 16, true, decodeBC2),
	137: blockFormat("BC3_UNORM", 16, true, decodeBC3),
	138: blockFormat("BC3_SRGB", 16, true, decodeBC3),
	139: blockFormat("BC4_UNORM", 8, false, decodeBC4(false)),
	140: blockFormat("BC4_SNORM", 8, false, decodeBC4(true)),
	141: blockFormat("BC5_UNORM", 16, false, decodeBC5(false)),
	142: blockFormat("BC5_SNORM", 16, false, decodeBC5(true)),
	143: bc6hFormat("BC6H_UFLOAT", false),
	144: bc6hFormat("BC6H_SFLOAT", true),
	145: blockFormat("BC7_UNORM", 16, true, decodeBC7),
	146: blockFormat("BC7_SRGB", 16, true, decodeBC7),
}

func decodeKTX2(data []byte, sel Select) (*Image, error) {
	tex, err := parseKTX2(data)
	if err != nil {
		return nil, err
	}
	img, err := tex.decode(sel)
	if img != nil {
		img.Format = "ktx2"
	}
	return img, err
}

func parseKTX2(data []byte) (*texture, error) {
	if len(data) < ktx2HeaderSize || !bytes.Equal(data[:12], ktx2Magic) {
		return nil, errKTX2Header
	}
	u32 := func(off int) int { return int(binary.LittleEndian.Uint32(data[off:])) }
	vkFormat := binary.LittleEndian.Uint32(data[12:])
	width, height, depth := u32(20), u32(24), u32(28)
	layers, faces, levels, scheme := max(u32(32), 1), max(u32(36), 1), max(u32(40), 1), u32(44)
	if width <= 0 || width > maxTextureSide || height > maxTextureSide || levels > 32 || layers > 2048 || (faces != 1 && faces != 6) {
		return nil, errKTX2Header
	}
	if depth > 1 {
		return nil, fmt.Errorf("%w: volume textures", ErrUnsupported)
	}
	height = max(height, 1)
	if scheme == ktx2SuperBasisLZ || vkFormat == 0 {
		return nil, fmt.Errorf("%w: Basis Universal textures", ErrUnsupported)
	}
	if scheme != ktx2SuperNone && scheme != ktx2SuperZstd && scheme != ktx2SuperZlib {
		return nil, fmt.Errorf("%w: KTX2 supercompression %d", ErrUnsupported, scheme)
	}
	format, ok := vkFormats[vkFormat]
	if !ok {
		return nil, fmt.Errorf("%w: Vulkan format %d", ErrUnsupported, vkFormat)
	}
	if len(data) < ktx2HeaderSize+levels*ktx2LevelIndexSize {
		return nil, errKTX2Header
	}
	type levelRange struct{ offset, length, uncompressed uint64 }
	ranges := make([]levelRange, levels)
	for l := range ranges {
		at := ktx2HeaderSize + l*ktx2LevelIndexSize
		ranges[l] = levelRange{
			binary.LittleEndian.Uint64(data[at:]),
			binary.LittleEndian.Uint64(data[at+8:]),
			binary.LittleEndian.Uint64(data[at+16:]),
		}
	}
	return &texture{
		container: "ktx2", format: format, width: width, height: height,
		levels: levels, faces: layers * faces, cube: faces == 6,
		surface: func(level, face int) ([]byte, error) {
			r := ranges[level]
			if r.offset > uint64(len(data)) || r.length > uint64(len(data))-r.offset {
				return nil, errTruncatedTexture
			}
			raw := data[r.offset : r.offset+r.length]
			size := format.surfaceSize(mipSize(width, level), mipSize(height, level))
			start := face * size
			if scheme != ktx2SuperNone {
				var err error
				if raw, err = inflateLevel(raw, scheme, r.uncompressed, uint64(start+size)); err != nil {
					return nil, err
				}
			}
			if start+size > len(raw) {
				return nil, errTruncatedTexture
			}
			return raw[start : start+size], nil
		},
	}, nil
}

func inflateLevel(raw []byte, scheme int, uncompressed, need uint64) ([]byte, error) {
	if uncompressed > ktx2MaxLevelBytes {
		return nil, ErrTooLarge
	}
	var r io.Reader
	if scheme == ktx2SuperZstd {
		dec, err := zstd.NewReader(bytes.NewReader(raw), zstd.WithDecoderMaxMemory(ktx2MaxLevelBytes))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		r = dec
	} else {
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("preview: ktx2 zlib: %w", err)
		}
		r = zr
	}
	out, err := io.ReadAll(io.LimitReader(r, int64(min(uncompressed, need))))
	if err != nil {
		return nil, fmt.Errorf("preview: ktx2 supercompression: %w", err)
	}
	return out, nil
}

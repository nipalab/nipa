package preview

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ddsHeaderSize   = 128
	ddsDX10Size     = 20
	ddsFlagFourCC   = 0x4
	ddsFlagRGB      = 0x40
	ddsFlagLum      = 0x20000
	ddsFlagAlpha    = 0x2
	ddsCapsCubemap  = 0x200
	ddsCapsVolume   = 0x200000
	ddsMiscCube     = 0x4
	ddsDimTexture3D = 4
)

var errDDSHeader = errors.New("preview: invalid dds header")

var dxgiFormats = map[uint32]pixelFormat{
	2:  floatFormat("R32G32B32A32_FLOAT", 4, 4),
	10: floatFormat("R16G16B16A16_FLOAT", 4, 2),
	11: rgba16,
	24: rgb10a2,
	26: r11g11b10Float,
	27: channelFormat("R8G8B8A8", 4, [4]int{0, 1, 2, 3}, false),
	28: channelFormat("R8G8B8A8_UNORM", 4, [4]int{0, 1, 2, 3}, false),
	29: channelFormat("R8G8B8A8_UNORM_SRGB", 4, [4]int{0, 1, 2, 3}, false),
	34: floatFormat("R16G16_FLOAT", 2, 2),
	41: floatFormat("R32_FLOAT", 1, 4),
	49: channelFormat("R8G8_UNORM", 2, [4]int{0, 1, -1, -1}, false),
	54: floatFormat("R16_FLOAT", 1, 2),
	61: channelFormat("R8_UNORM", 1, [4]int{0, -1, -1, -1}, true),
	65: channelFormat("A8_UNORM", 1, [4]int{-1, -1, -1, 0}, false),
	67: rgb9e5Float,
	70: blockFormat("BC1", 8, true, decodeBC1),
	71: blockFormat("BC1_UNORM", 8, true, decodeBC1),
	72: blockFormat("BC1_UNORM_SRGB", 8, true, decodeBC1),
	73: blockFormat("BC2", 16, true, decodeBC2),
	74: blockFormat("BC2_UNORM", 16, true, decodeBC2),
	75: blockFormat("BC2_UNORM_SRGB", 16, true, decodeBC2),
	76: blockFormat("BC3", 16, true, decodeBC3),
	77: blockFormat("BC3_UNORM", 16, true, decodeBC3),
	78: blockFormat("BC3_UNORM_SRGB", 16, true, decodeBC3),
	79: blockFormat("BC4", 8, false, decodeBC4(false)),
	80: blockFormat("BC4_UNORM", 8, false, decodeBC4(false)),
	81: blockFormat("BC4_SNORM", 8, false, decodeBC4(true)),
	82: blockFormat("BC5", 16, false, decodeBC5(false)),
	83: blockFormat("BC5_UNORM", 16, false, decodeBC5(false)),
	84: blockFormat("BC5_SNORM", 16, false, decodeBC5(true)),
	87: channelFormat("B8G8R8A8_UNORM", 4, [4]int{2, 1, 0, 3}, false),
	88: channelFormat("B8G8R8X8_UNORM", 4, [4]int{2, 1, 0, -1}, false),
	91: channelFormat("B8G8R8A8_UNORM_SRGB", 4, [4]int{2, 1, 0, 3}, false),
	93: channelFormat("B8G8R8X8_UNORM_SRGB", 4, [4]int{2, 1, 0, -1}, false),
	94: bc6hFormat("BC6H", false),
	95: bc6hFormat("BC6H_UF16", false),
	96: bc6hFormat("BC6H_SF16", true),
	97: blockFormat("BC7", 16, true, decodeBC7),
	98: blockFormat("BC7_UNORM", 16, true, decodeBC7),
	99: blockFormat("BC7_UNORM_SRGB", 16, true, decodeBC7),
}

var fourCCFormats = map[string]pixelFormat{
	"DXT1": blockFormat("DXT1", 8, true, decodeBC1),
	"DXT2": blockFormat("DXT2", 16, true, decodeBC2),
	"DXT3": blockFormat("DXT3", 16, true, decodeBC2),
	"DXT4": blockFormat("DXT4", 16, true, decodeBC3),
	"DXT5": blockFormat("DXT5", 16, true, decodeBC3),
	"ATI1": blockFormat("ATI1", 8, false, decodeBC4(false)),
	"BC4U": blockFormat("BC4U", 8, false, decodeBC4(false)),
	"BC4S": blockFormat("BC4S", 8, false, decodeBC4(true)),
	"ATI2": blockFormat("ATI2", 16, false, decodeBC5(false)),
	"BC5U": blockFormat("BC5U", 16, false, decodeBC5(false)),
	"BC5S": blockFormat("BC5S", 16, false, decodeBC5(true)),
}

var d3dFormats = map[uint32]pixelFormat{
	36:  rgba16,
	111: floatFormat("R16F", 1, 2),
	112: floatFormat("G16R16F", 2, 2),
	113: floatFormat("A16B16G16R16F", 4, 2),
	114: floatFormat("R32F", 1, 4),
	115: floatFormat("G32R32F", 2, 4),
	116: floatFormat("A32B32G32R32F", 4, 4),
}

func decodeDDS(data []byte, sel Select) (*Image, error) {
	tex, err := parseDDS(data)
	if err != nil {
		return nil, err
	}
	return tex.decode(sel)
}

func parseDDS(data []byte) (*texture, error) {
	if len(data) < ddsHeaderSize || string(data[:4]) != "DDS " || binary.LittleEndian.Uint32(data[4:]) != 124 {
		return nil, errDDSHeader
	}
	u32 := func(off int) uint32 { return binary.LittleEndian.Uint32(data[off:]) }
	height, width := int(u32(12)), int(u32(16))
	depth := int(u32(24))
	levels := max(int(u32(28)), 1)
	pfFlags, fourCC := u32(80), string(data[84:88])
	caps2 := u32(112)
	if width <= 0 || height <= 0 || width > maxTextureSide || height > maxTextureSide || levels > 32 {
		return nil, errDDSHeader
	}
	if caps2&ddsCapsVolume != 0 && depth > 1 {
		return nil, fmt.Errorf("%w: volume textures", ErrUnsupported)
	}
	offset := ddsHeaderSize
	faces := 1
	cube := false
	var format pixelFormat
	var ok bool
	switch {
	case pfFlags&ddsFlagFourCC != 0 && fourCC == "DX10":
		if len(data) < ddsHeaderSize+ddsDX10Size {
			return nil, errDDSHeader
		}
		offset += ddsDX10Size
		dxgi, dim, misc, arraySize := u32(128), u32(132), u32(136), int(u32(140))
		if dim == ddsDimTexture3D {
			return nil, fmt.Errorf("%w: volume textures", ErrUnsupported)
		}
		if format, ok = dxgiFormats[dxgi]; !ok {
			return nil, fmt.Errorf("%w: DXGI format %d", ErrUnsupported, dxgi)
		}
		faces = max(arraySize, 1)
		if misc&ddsMiscCube != 0 {
			faces *= 6
			cube = true
		}
	case pfFlags&ddsFlagFourCC != 0:
		if format, ok = fourCCFormats[fourCC]; !ok {
			if format, ok = d3dFormats[u32(84)]; !ok {
				return nil, fmt.Errorf("%w: DDS format %q", ErrUnsupported, fourCC)
			}
		}
	case pfFlags&(ddsFlagRGB|ddsFlagLum|ddsFlagAlpha) != 0:
		var err error
		masks := [4]uint32{u32(92), u32(96), u32(100), u32(104)}
		if pfFlags&(ddsFlagRGB|ddsFlagLum) == 0 {
			masks = [4]uint32{0, 0, 0, u32(104)}
		}
		if format, err = maskFormat(int(u32(88)), masks, pfFlags&ddsFlagLum != 0); err != nil {
			return nil, err
		}
	default:
		return nil, errDDSHeader
	}
	if caps2&ddsCapsCubemap != 0 && !cube {
		faces = 0
		for bit := uint32(0x400); bit <= 0x8000; bit <<= 1 {
			if caps2&bit != 0 {
				faces++
			}
		}
		faces = max(faces, 1)
		cube = faces == 6
	}
	if faces > 6*2048 {
		return nil, errDDSHeader
	}
	chain := 0
	for l := range levels {
		chain += format.surfaceSize(mipSize(width, l), mipSize(height, l))
	}
	return &texture{
		container: "dds", format: format, width: width, height: height, levels: levels, faces: faces, cube: cube,
		surface: func(level, face int) ([]byte, error) {
			start := offset + face*chain
			for l := range level {
				start += format.surfaceSize(mipSize(width, l), mipSize(height, l))
			}
			end := start + format.surfaceSize(mipSize(width, level), mipSize(height, level))
			if end > len(data) {
				return nil, errTruncatedTexture
			}
			return data[start:end], nil
		},
	}, nil
}

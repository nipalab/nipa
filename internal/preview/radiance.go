package preview

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errHDRHeader = errors.New("preview: invalid radiance hdr")

func decodeRadiance(data []byte) (*Image, error) {
	if !bytes.HasPrefix(data, []byte("#?")) {
		return nil, errHDRHeader
	}
	pos := 0
	nextLine := func() (string, bool) {
		end := bytes.IndexByte(data[pos:], '\n')
		if end < 0 {
			return "", false
		}
		line := string(data[pos : pos+end])
		pos += end + 1
		return strings.TrimRight(line, "\r"), true
	}
	for {
		line, ok := nextLine()
		if !ok {
			return nil, errHDRHeader
		}
		if line == "" {
			break
		}
		if f, ok := strings.CutPrefix(line, "FORMAT="); ok && f != "32-bit_rle_rgbe" {
			return nil, fmt.Errorf("%w: radiance format %s", ErrUnsupported, f)
		}
	}
	res, ok := nextLine()
	if !ok {
		return nil, errHDRHeader
	}
	fields := strings.Fields(res)
	if len(fields) != 4 || fields[2] != "+X" || (fields[0] != "-Y" && fields[0] != "+Y") {
		return nil, fmt.Errorf("%w: radiance orientation %q", ErrUnsupported, res)
	}
	height, err1 := strconv.Atoi(fields[1])
	width, err2 := strconv.Atoi(fields[3])
	if err1 != nil || err2 != nil {
		return nil, errHDRHeader
	}
	if err := checkFloatSize(width, height); err != nil {
		return nil, err
	}
	var rgbe []byte
	scan := make([]byte, width*4)
	for range height {
		n, err := readRGBEScanline(data[pos:], scan)
		if err != nil {
			return nil, err
		}
		pos += n
		rgbe = append(rgbe, scan...)
	}
	img := newFloatImage(width, height)
	for y := range height {
		row := y
		if fields[0] == "+Y" {
			row = height - 1 - y
		}
		for x := range width {
			px := img.Pix[(row*width+x)*4:]
			s := rgbe[(y*width+x)*4:]
			px[3] = 1
			if s[3] == 0 {
				continue
			}
			f := math.Ldexp(1, int(s[3])-136)
			for c := range 3 {
				px[c] = float32((float64(s[c]) + 0.5) * f)
			}
		}
	}
	out := &Image{Format: "hdr", Encoding: "RGBE", BitDepth: 32}
	out.setHDR(img)
	return out, nil
}

var errHDRTruncated = errors.New("preview: truncated radiance hdr")

// readRGBEScanline reads one scanline into scan (RGBE per pixel), handling
// the adaptive run-length format, the old run format and flat pixels.
func readRGBEScanline(data []byte, scan []byte) (int, error) {
	width := len(scan) / 4
	if width >= 8 && width < 0x8000 && len(data) >= 4 && data[0] == 2 && data[1] == 2 && data[2]&0x80 == 0 {
		if int(data[2])<<8|int(data[3]) != width {
			return 0, errHDRHeader
		}
		pos := 4
		for c := range 4 {
			for x := 0; x < width; {
				if pos >= len(data) {
					return 0, errHDRTruncated
				}
				count := int(data[pos])
				pos++
				if count > 128 {
					count -= 128
					if count > width-x || pos >= len(data) {
						return 0, errHDRTruncated
					}
					for range count {
						scan[x*4+c] = data[pos]
						x++
					}
					pos++
					continue
				}
				if count == 0 || count > width-x || pos+count > len(data) {
					return 0, errHDRTruncated
				}
				for i := range count {
					scan[x*4+c] = data[pos+i]
					x++
				}
				pos += count
			}
		}
		return pos, nil
	}
	pos, shift := 0, 0
	for x := 0; x < width; {
		if pos+4 > len(data) {
			return 0, errHDRTruncated
		}
		p := data[pos : pos+4]
		pos += 4
		if p[0] == 1 && p[1] == 1 && p[2] == 1 && x > 0 {
			count := int(p[3]) << shift
			if count > width-x {
				return 0, errHDRTruncated
			}
			for range count {
				copy(scan[x*4:x*4+4], scan[(x-1)*4:x*4])
				x++
			}
			shift += 8
			continue
		}
		copy(scan[x*4:x*4+4], p)
		x++
		shift = 0
	}
	return pos, nil
}

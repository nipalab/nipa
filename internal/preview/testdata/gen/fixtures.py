# Regenerates the texture fixtures in ../ (see README.md in this folder).
import struct, subprocess, sys, os, zlib, compression.zstd as zstd
from PIL import Image
import etcpak

out = sys.argv[1]
os.chdir(out)
run = lambda *a: subprocess.check_call(list(a))

run('magick', '-size', '16x8', 'gradient:red-blue', '(', '-size', '8x16', 'gradient:white-black', '-rotate', '90', ')',
    '-alpha', 'off', '-compose', 'copy_opacity', '-composite', 'tex.png')
run('magick', 'tex.png', '-alpha', 'off', 'tex_rgb.png')
src = Image.open('tex.png').convert('RGBA')
rgba = src.tobytes()

for name, comp in [('dxt1', 'dxt1'), ('dxt5', 'dxt5'), ('rgba', 'none')]:
    run('magick', 'tex.png', '-define', 'dds:compression=' + comp, '-define', 'dds:mipmaps=2', f'dds_{name}.dds')
    Image.open(f'dds_{name}.dds').convert('RGBA').save(f'dds_{name}.ref.png')
src.convert('RGB').save('dds_bc5.dds', pixel_format='BC5')
Image.open('dds_bc5.dds').convert('RGBA').save('dds_bc5.ref.png')
src.convert('L').save('dds_l8.dds')
Image.open('dds_l8.dds').convert('RGBA').save('dds_l8.ref.png')

def dds(w, h, mips, fourcc, body, dxgi=None, misc=0, array=1, caps2=0, pf_flags=4, bits=0, masks=(0, 0, 0, 0)):
    hdr = struct.pack('<4sIIIIIII44x', b'DDS ', 124, 0x1007 | 0x20000, h, w, 0, 0, mips)
    pf = struct.pack('<II4sI4I', 32, pf_flags, fourcc, bits, *masks)
    caps = struct.pack('<IIII4x', 0x1000, caps2, 0, 0)
    ext = struct.pack('<IIIII', dxgi, 3, misc, array, 0) if dxgi is not None else b''
    return hdr + pf + caps + ext + body

open('dds_bc7.dds', 'wb').write(dds(16, 8, 1, b'DX10', etcpak.compress_bc7(rgba, 16, 8, None), dxgi=98))
Image.open('dds_bc7.dds').convert('RGBA').save('dds_bc7.ref.png')
faces = [(255, 0, 0), (0, 255, 0), (0, 0, 255), (255, 255, 0), (0, 255, 255), (255, 0, 255)]
cube = b''
for c in faces:
    cube += bytes([*c, 255]) * 16 + bytes([c[0] // 2, c[1] // 2, c[2] // 2, 255]) * 4
open('dds_cube.dds', 'wb').write(dds(4, 4, 2, b'DX10', cube, dxgi=28, misc=4, caps2=0xfe00))
half = lambda v: struct.pack('<e', v)
fp16 = b''.join(half(v) for px in [(2.0, 0.5, 0.0, 1.0), (0.25, 4.0, 1.0, 0.5), (0.0, 0.0, 8.0, 1.0), (1.0, 1.0, 1.0, 0.0)] for v in px)
open('dds_rgba16f.dds', 'wb').write(dds(2, 2, 1, b'DX10', fp16, dxgi=10))
open('dds_d3d113.dds', 'wb').write(dds(2, 2, 1, struct.pack('<I', 113), fp16))

VK = {'rgba8': 37, 'bc7': 145, 'rgba16f': 97}
def ktx2(w, h, vk, levels, faces=1, scheme=0, layers=0):
    head = b'\xabKTX 20\xbb\r\n\x1a\n' + struct.pack('<9I', vk, 1, w, h, 0, layers, faces, len(levels), scheme)
    index_end = 80 + 24 * len(levels)
    head += struct.pack('<4I2Q', 0, 0, 0, 0, 0, 0)
    body, idx = b'', b''
    off = index_end
    datas = []
    for raw in levels:
        packed = raw
        if scheme == 2:
            packed = zstd.compress(raw)
        elif scheme == 3:
            packed = zlib.compress(raw)
        datas.append((off, packed, len(raw)))
        off += len(packed)
    for o, p, n in datas:
        idx += struct.pack('<3Q', o, len(p), n)
        body += p
    return head + idx + body

half_img = src.resize((8, 4)).tobytes()
for scheme, name in [(0, 'rgba8'), (2, 'zstd'), (3, 'zlib')]:
    open(f'ktx2_{name}.ktx2', 'wb').write(ktx2(16, 8, 37, [rgba, half_img], scheme=scheme))
src.resize((8, 4)).save('ktx2_mip1.ref.png')
open('ktx2_bc7.ktx2', 'wb').write(ktx2(16, 8, 145, [etcpak.compress_bc7(rgba, 16, 8, None)]))
open('ktx2_cube.ktx2', 'wb').write(ktx2(4, 4, 37, [b''.join(bytes([*c, 255]) * 16 for c in faces)], faces=6))
open('ktx2_rgba16f.ktx2', 'wb').write(ktx2(2, 2, 97, [fp16]))
open('ktx2_basis.ktx2', 'wb').write(ktx2(4, 4, 0, [b'\0' * 16], scheme=1))

for comp in ['None', 'RLE', 'ZipS', 'Zip', 'Pxr24', 'Piz']:
    run('magick', 'tex.png', '-compress', comp, f'exr_{comp.lower()}.exr')
run('magick', 'tex.png', 'hdr_rle.hdr')

def exr(channels, w, h, rows_fn, tiled=None, extra=b''):
    def attr(name, typ, val):
        return name.encode() + b'\0' + typ.encode() + b'\0' + struct.pack('<I', len(val)) + val
    chl = b''.join(n.encode() + b'\0' + struct.pack('<iB3xii', t, 0, 1, 1) for n, t in channels) + b'\0'
    hdr = struct.pack('<II', 20000630, 2 | (0x200 if tiled else 0))
    hdr += attr('channels', 'chlist', chl) + attr('compression', 'compression', b'\0')
    hdr += attr('dataWindow', 'box2i', struct.pack('<4i', 0, 0, w - 1, h - 1)) + attr('displayWindow', 'box2i', struct.pack('<4i', 0, 0, w - 1, h - 1))
    hdr += attr('lineOrder', 'lineOrder', b'\0') + attr('pixelAspectRatio', 'float', struct.pack('<f', 1)) + attr('screenWindowCenter', 'v2f', struct.pack('<2f', 0, 0)) + attr('screenWindowWidth', 'float', struct.pack('<f', 1))
    if tiled:
        hdr += attr('tiles', 'tiledesc', struct.pack('<IIB', tiled, tiled, 0))
    hdr += b'\0'
    chunks = []
    if tiled:
        for ty in range((h + tiled - 1) // tiled):
            for tx in range((w + tiled - 1) // tiled):
                x0, y0 = tx * tiled, ty * tiled
                tw, th = min(tiled, w - x0), min(tiled, h - y0)
                data = b''.join(rows_fn(y, x0, tw) for y in range(y0, y0 + th))
                chunks.append(struct.pack('<5i', tx, ty, 0, 0, len(data)) + data)
    else:
        for y in range(h):
            data = rows_fn(y, 0, w)
            chunks.append(struct.pack('<2i', y, len(data)) + data)
    off = len(hdr) + 8 * len(chunks)
    table = b''
    for c in chunks:
        table += struct.pack('<Q', off)
        off += len(c)
    return hdr + table + b''.join(chunks)

def layered(y, x0, n):
    row = b''
    for name, t in [('diffuse.B', 2), ('diffuse.G', 2), ('diffuse.R', 2), ('id', 0)]:
        for x in range(x0, x0 + n):
            v = {'diffuse.R': x * 0.5, 'diffuse.G': y * 2.0, 'diffuse.B': 3.0, 'id': x + y}[name]
            row += struct.pack('<f', v) if t == 2 else struct.pack('<I', v)
    return row
open('exr_tiled.exr', 'wb').write(exr([('diffuse.B', 2), ('diffuse.G', 2), ('diffuse.R', 2), ('id', 0)], 5, 3, layered, tiled=2))
open('exr_luma.exr', 'wb').write(exr([('Y', 1)], 3, 2, lambda y, x0, n: b''.join(half(0.5 * (x + y)) for x in range(x0, x0 + n))))
open('exr_depth.exr', 'wb').write(exr([('Z', 2)], 2, 1, lambda y, x0, n: struct.pack('<2f', 7.0, 9.0)))

open('hdr_flat.hdr', 'wb').write(b'#?RADIANCE\nFORMAT=32-bit_rle_rgbe\n\n+Y 2 +X 2\n' + bytes([128, 64, 0, 129, 1, 1, 1, 1, 0, 0, 0, 0, 255, 255, 255, 128]))

run('magick', 'tex.png', '(', '-size', '16x8', 'xc:none', '-fill', 'yellow', '-draw', 'rectangle 2,2 6,5', '-set', 'label', 'Hero', ')',
    '(', '-size', '16x8', 'xc:none', '-fill', 'white', '-draw', 'point 9,1', '-set', 'label', 'Spark', ')', 'psd_layers.psd')
run('magick', 'tex.png', '-depth', '16', 'psd_16.psd')
run('magick', 'tex.png', '-alpha', 'off', '-colorspace', 'gray', 'psd_gray.psd')
run('magick', 'tex.png', '-alpha', 'off', '-colorspace', 'CMYK', 'psd_cmyk.psd')
run('magick', 'tex.png', '-alpha', 'off', '-compress', 'None', 'psd_raw.psb')

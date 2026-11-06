const b64 = (bytes: Uint8Array): string => {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
};

export const textBase64 = (s: string): string => b64(new TextEncoder().encode(s));

export function textureSvg(palette: [string, string, string], accent: string): string {
  const [a, b, c] = palette;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient>
<radialGradient id="r" cx=".5" cy=".4" r=".6"><stop offset="0" stop-color="${c}" stop-opacity=".9"/><stop offset="1" stop-color="${c}" stop-opacity="0"/></radialGradient></defs>
<rect width="512" height="512" fill="url(#g)"/><rect width="512" height="512" fill="url(#r)"/>
<g fill="none" stroke="${accent}" stroke-width="10" stroke-linecap="round" opacity=".85"><path d="M96 400V120l160 280V120"/><path d="M300 400h110M300 340h110M300 280h110"/></g>
<g fill="#fff" opacity=".18"><circle cx="400" cy="90" r="46"/><circle cx="110" cy="440" r="30"/></g></svg>`;
  return `data:image/svg+xml;base64,${btoa(svg)}`;
}

export function torusGlb(radius: number, tube: number): string {
  const seg = 48;
  const side = 20;
  const pos: number[] = [];
  const nor: number[] = [];
  for (let i = 0; i <= seg; i++) {
    const u = (i / seg) * Math.PI * 2;
    for (let j = 0; j <= side; j++) {
      const v = (j / side) * Math.PI * 2;
      const cx = Math.cos(u) * radius;
      const cz = Math.sin(u) * radius;
      const nx = Math.cos(v) * Math.cos(u);
      const ny = Math.sin(v);
      const nz = Math.cos(v) * Math.sin(u);
      pos.push(cx + nx * tube, ny * tube, cz + nz * tube);
      nor.push(nx, ny, nz);
    }
  }
  const idx: number[] = [];
  for (let i = 0; i < seg; i++) {
    for (let j = 0; j < side; j++) {
      const a = i * (side + 1) + j;
      const b = a + side + 1;
      idx.push(a, b, a + 1, b, b + 1, a + 1);
    }
  }
  const posBytes = new Uint8Array(new Float32Array(pos).buffer);
  const norBytes = new Uint8Array(new Float32Array(nor).buffer);
  const idxBytes = new Uint8Array(new Uint16Array(idx).buffer);
  const total = posBytes.length + norBytes.length + idxBytes.length;
  const bin = new Uint8Array(total + ((4 - (total % 4)) % 4));
  bin.set(posBytes, 0);
  bin.set(norBytes, posBytes.length);
  bin.set(idxBytes, posBytes.length + norBytes.length);
  const extent = radius + tube;
  const json = {
    asset: { version: "2.0", generator: "Nipa demo" },
    scene: 0,
    scenes: [{ nodes: [0] }],
    nodes: [{ mesh: 0, name: "Ring" }],
    meshes: [{ primitives: [{ attributes: { POSITION: 0, NORMAL: 1 }, indices: 2, material: 0 }] }],
    materials: [{ name: "Gold", pbrMetallicRoughness: { baseColorFactor: [0.95, 0.72, 0.25, 1], metallicFactor: 0.9, roughnessFactor: 0.35 } }],
    accessors: [
      { bufferView: 0, componentType: 5126, count: pos.length / 3, type: "VEC3", min: [-extent, -tube, -extent], max: [extent, tube, extent] },
      { bufferView: 1, componentType: 5126, count: nor.length / 3, type: "VEC3" },
      { bufferView: 2, componentType: 5123, count: idx.length, type: "SCALAR" },
    ],
    bufferViews: [
      { buffer: 0, byteOffset: 0, byteLength: posBytes.length, target: 34962 },
      { buffer: 0, byteOffset: posBytes.length, byteLength: norBytes.length, target: 34962 },
      { buffer: 0, byteOffset: posBytes.length + norBytes.length, byteLength: idxBytes.length, target: 34963 },
    ],
    buffers: [{ byteLength: bin.length }],
  };
  let jsonText = JSON.stringify(json);
  jsonText += " ".repeat((4 - (jsonText.length % 4)) % 4);
  const jsonBytes = new TextEncoder().encode(jsonText);
  const length = 12 + 8 + jsonBytes.length + 8 + bin.length;
  const out = new Uint8Array(length);
  const view = new DataView(out.buffer);
  view.setUint32(0, 0x46546c67, true);
  view.setUint32(4, 2, true);
  view.setUint32(8, length, true);
  view.setUint32(12, jsonBytes.length, true);
  view.setUint32(16, 0x4e4f534a, true);
  out.set(jsonBytes, 20);
  const binAt = 20 + jsonBytes.length;
  view.setUint32(binAt, bin.length, true);
  view.setUint32(binAt + 4, 0x004e4942, true);
  out.set(bin, binAt + 8);
  return b64(out);
}

export function waveformPeaks(seed: number, buckets: number, loud: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < buckets; i++) {
    const t = i / buckets;
    const env = Math.min(1, t * 8) * (0.55 + 0.45 * Math.sin(t * 22 + seed)) * (1 - Math.max(0, t - 0.85) * 5);
    const wobble = 0.12 * Math.sin(i * 1.7 + seed * 3);
    const amp = Math.max(0.02, Math.min(1, (env + wobble) * loud));
    out.push(-amp, amp);
  }
  return out;
}

export function spectrogramBytes(seed: number, cols: number, rows: number, bright: number): string {
  const levels = new Uint8Array(cols * rows);
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const f = r / rows;
      const base = (1 - f) ** 1.6 * 200 * bright;
      const bands = 40 * Math.max(0, Math.sin(c * 0.09 + seed) * Math.cos(f * 14 + seed));
      const pulse = 25 * Math.max(0, Math.sin(c * 0.5 + f * 3));
      levels[r * cols + c] = Math.max(0, Math.min(255, base * 0.6 + bands + pulse));
    }
  }
  return b64(levels);
}

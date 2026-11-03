import type { Pixels } from "@/lib/imageCompare";

export type ToneMap = "clamp" | "reinhard" | "aces";

export type FloatPixels = { width: number; height: number; data: Float32Array };

// unpackHdr rebuilds linear floats from the daemon's packed preview: RGBE
// mantissas in the left half, exponent (red) and alpha (green) in the right.
export function unpackHdr(packed: Pixels): FloatPixels {
  const width = Math.floor(packed.width / 2);
  const { height } = packed;
  const out = new Float32Array(width * height * 4);
  const d = packed.data;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const left = (y * packed.width + x) * 4;
      const right = (y * packed.width + width + x) * 4;
      const o = (y * width + x) * 4;
      const e = d[right];
      if (e !== 0) {
        const f = 2 ** (e - 136);
        out[o] = (d[left] + 0.5) * f;
        out[o + 1] = (d[left + 1] + 0.5) * f;
        out[o + 2] = (d[left + 2] + 0.5) * f;
      }
      out[o + 3] = d[right + 1] / 255;
    }
  }
  return { width, height, data: out };
}

const lutSize = 4096;
const srgbLut = (() => {
  const t = new Uint8ClampedArray(lutSize + 1);
  for (let i = 0; i <= lutSize; i++) {
    const v = i / lutSize;
    t[i] = Math.round((v <= 0.0031308 ? v * 12.92 : 1.055 * v ** (1 / 2.4) - 0.055) * 255);
  }
  return t;
})();

const curves: Record<ToneMap, (v: number) => number> = {
  clamp: (v) => v,
  reinhard: (v) => v / (1 + v),
  aces: (v) => (v * (2.51 * v + 0.03)) / (v * (2.43 * v + 0.59) + 0.14),
};

// tonemap applies an exposure in stops and a tone curve, then encodes sRGB.
export function tonemap(src: FloatPixels, exposure: number, curve: ToneMap): Pixels {
  const scale = 2 ** exposure;
  const map = curves[curve];
  const out = new Uint8ClampedArray(src.data.length);
  const d = src.data;
  for (let i = 0; i < d.length; i += 4) {
    for (let c = 0; c < 3; c++) {
      const v = map(Math.max(0, d[i + c] * scale));
      out[i + c] = srgbLut[Math.round(Math.min(1, v) * lutSize)];
    }
    out[i + 3] = Math.round(Math.min(1, Math.max(0, d[i + 3])) * 255);
  }
  return { width: src.width, height: src.height, data: out };
}

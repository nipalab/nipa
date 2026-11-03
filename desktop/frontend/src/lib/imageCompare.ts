export type Pixels = { width: number; height: number; data: Uint8ClampedArray };

export type Channels = { r: boolean; g: boolean; b: boolean; a: boolean };

export const allChannels: Channels = { r: true, g: true, b: true, a: true };

const channelIndex = { r: 0, g: 1, b: 2, a: 3 } as const;

// isolateChannels shows only the selected channels: a single channel becomes
// an opaque grayscale image, otherwise hidden colour channels are zeroed and
// a hidden alpha makes the image opaque.
export function isolateChannels(src: Pixels, ch: Channels): Pixels {
  const selected = (Object.keys(channelIndex) as (keyof Channels)[]).filter((k) => ch[k]);
  if (selected.length === 4) return src;
  const out = new Uint8ClampedArray(src.data.length);
  const d = src.data;
  if (selected.length === 1) {
    const at = channelIndex[selected[0]];
    for (let i = 0; i < d.length; i += 4) {
      const v = d[i + at];
      out[i] = v;
      out[i + 1] = v;
      out[i + 2] = v;
      out[i + 3] = 255;
    }
  } else {
    for (let i = 0; i < d.length; i += 4) {
      out[i] = ch.r ? d[i] : 0;
      out[i + 1] = ch.g ? d[i + 1] : 0;
      out[i + 2] = ch.b ? d[i + 2] : 0;
      out[i + 3] = ch.a ? d[i + 3] : 255;
    }
  }
  return { width: src.width, height: src.height, data: out };
}

export type Heatmap = { pixels: Pixels; changed: number; total: number };

const pixelAt = (p: Pixels, x: number, y: number): number => (x < p.width && y < p.height ? (y * p.width + x) * 4 : -1);

// heatmap marks every pixel whose largest channel delta exceeds tolerance,
// yellow for small deltas through red for large ones, over a dimmed grayscale
// of the new image. The two sides are aligned at the top-left corner; a pixel
// only one side has always counts as changed, and two fully transparent
// pixels are equal whatever their colour, like the daemon's statistics.
export function heatmap(a: Pixels, b: Pixels, tolerance: number): Heatmap {
  const width = Math.max(a.width, b.width);
  const height = Math.max(a.height, b.height);
  const out = new Uint8ClampedArray(width * height * 4);
  let changed = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const o = (y * width + x) * 4;
      const ia = pixelAt(a, x, y);
      const ib = pixelAt(b, x, y);
      let delta = 255;
      if (ia >= 0 && ib >= 0) {
        const ad = a.data;
        const bd = b.data;
        delta =
          ad[ia + 3] === 0 && bd[ib + 3] === 0
            ? 0
            : Math.max(Math.abs(ad[ia] - bd[ib]), Math.abs(ad[ia + 1] - bd[ib + 1]), Math.abs(ad[ia + 2] - bd[ib + 2]), Math.abs(ad[ia + 3] - bd[ib + 3]));
      }
      if (delta > tolerance) {
        changed++;
        const t = delta / 255;
        out[o] = 255;
        out[o + 1] = Math.round(220 * (1 - t));
        out[o + 2] = 0;
      } else {
        const src = ib >= 0 ? b.data : a.data;
        const i = ib >= 0 ? ib : ia;
        const lum = (src[i] * 0.299 + src[i + 1] * 0.587 + src[i + 2] * 0.114) * (src[i + 3] / 255);
        const v = Math.round(24 + lum * 0.25);
        out[o] = v;
        out[o + 1] = v;
        out[o + 2] = v;
      }
      out[o + 3] = 255;
    }
  }
  return { pixels: { width, height, data: out }, changed, total: width * height };
}

export const minZoom = 0.02;
export const maxZoom = 32;

export const clampZoom = (z: number): number => Math.min(maxZoom, Math.max(minZoom, z));

export const maxFitZoom = 8;

// fitZoom is the largest zoom (CSS pixels per image pixel) that shows the
// whole image inside the box with a small margin; small sprites are enlarged
// up to maxFitZoom so their pixels stay readable.
export function fitZoom(box: { width: number; height: number }, image: { width: number; height: number }): number {
  if (image.width <= 0 || image.height <= 0 || box.width <= 0 || box.height <= 0) return 1;
  return clampZoom(Math.min(maxFitZoom, (box.width - 32) / image.width, (box.height - 32) / image.height));
}

const zoomSteps = [0.02, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 6, 8, 12, 16, 24, 32];

export function stepZoom(zoom: number, dir: 1 | -1): number {
  if (dir > 0) return zoomSteps.find((s) => s > zoom + 1e-6) ?? maxZoom;
  return [...zoomSteps].reverse().find((s) => s < zoom - 1e-6) ?? minZoom;
}

export function formatPercent(ratio: number): string {
  if (ratio <= 0) return "0%";
  const pct = ratio * 100;
  if (pct < 0.01) return "<0.01%";
  if (pct < 10) return `${pct.toFixed(2)}%`;
  return `${pct.toFixed(1)}%`;
}

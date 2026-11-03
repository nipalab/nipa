import type { ImageLayer } from "@/api/desktop";

export type LayerStatus = "added" | "removed" | "changed" | "unchanged";

export type LayerRow = { layer: ImageLayer; status: LayerStatus; changes: string[] };

const blendNames: Record<string, string> = {
  norm: "Normal",
  pass: "Pass through",
  diss: "Dissolve",
  dark: "Darken",
  mul: "Multiply",
  idiv: "Color burn",
  lbrn: "Linear burn",
  lite: "Lighten",
  scrn: "Screen",
  "div ": "Color dodge",
  lddg: "Linear dodge",
  over: "Overlay",
  sLit: "Soft light",
  hLit: "Hard light",
  diff: "Difference",
  smud: "Exclusion",
  hue: "Hue",
  sat: "Saturation",
  colr: "Color",
  lum: "Luminosity",
};

export const blendName = (key: string): string => blendNames[key] ?? blendNames[key.trim()] ?? key.trim();

// key matches layers across versions by kind, name and how many earlier
// layers share that name, so renamed duplicates do not pair up wrongly.
function keyed(layers: ImageLayer[]): Map<string, ImageLayer> {
  const seen = new Map<string, number>();
  const out = new Map<string, ImageLayer>();
  for (const l of layers) {
    const base = `${l.kind}\u0000${l.name}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    out.set(`${base}\u0000${n}`, l);
  }
  return out;
}

function changesBetween(a: ImageLayer, b: ImageLayer): string[] {
  const out: string[] = [];
  if (a.hash !== b.hash) out.push("pixels");
  if (a.x !== b.x || a.y !== b.y || a.width !== b.width || a.height !== b.height) out.push("bounds");
  if (a.visible !== b.visible) out.push(b.visible ? "shown" : "hidden");
  if (a.opacity !== b.opacity) out.push(`opacity ${Math.round((a.opacity / 255) * 100)}% → ${Math.round((b.opacity / 255) * 100)}%`);
  if (a.blend !== b.blend) out.push(`${blendName(a.blend)} → ${blendName(b.blend)}`);
  if (a.depth !== b.depth) out.push("moved");
  return out;
}

// diffLayers lists the new layer stack with each layer's status; removed
// layers are placed after the layer that preceded them in the old stack.
export function diffLayers(oldLayers: ImageLayer[], newLayers: ImageLayer[]): LayerRow[] {
  const oldKeyed = keyed(oldLayers);
  const newKeyed = keyed(newLayers);
  const rows: { key: string; row: LayerRow }[] = [];
  for (const [key, layer] of newKeyed) {
    const before = oldKeyed.get(key);
    if (!before) {
      rows.push({ key, row: { layer, status: "added", changes: [] } });
      continue;
    }
    const changes = changesBetween(before, layer);
    rows.push({ key, row: { layer, status: changes.length ? "changed" : "unchanged", changes } });
  }
  let previous: string | null = null;
  for (const [key, layer] of oldKeyed) {
    if (!newKeyed.has(key)) {
      const at = previous === null ? 0 : rows.findIndex((r) => r.key === previous) + 1;
      rows.splice(at, 0, { key, row: { layer, status: "removed", changes: [] } });
    }
    previous = key;
  }
  return rows.map((r) => r.row);
}

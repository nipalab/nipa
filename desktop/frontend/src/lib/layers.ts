import type { ImageLayer } from "@/api/desktop";
import { translate } from "@/i18n";

export type LayerStatus = "added" | "removed" | "changed" | "unchanged";

export type LayerRow = { layer: ImageLayer; status: LayerStatus; changes: string[] };

const blendNames: Record<string, string> = {
  norm: translate("layers.normal"),
  pass: translate("layers.passThrough"),
  diss: translate("layers.dissolve"),
  dark: translate("layers.darken"),
  mul: translate("layers.multiply"),
  idiv: translate("layers.colorBurn"),
  lbrn: translate("layers.linearBurn"),
  lite: translate("layers.lighten"),
  scrn: translate("layers.screen"),
  "div ": translate("layers.colorDodge"),
  lddg: translate("layers.linearDodge"),
  over: translate("layers.overlay"),
  sLit: translate("layers.softLight"),
  hLit: translate("layers.hardLight"),
  diff: translate("layers.difference"),
  smud: translate("layers.exclusion"),
  hue: translate("layers.hue"),
  sat: translate("layers.saturation"),
  colr: translate("layers.color"),
  lum: translate("layers.luminosity"),
};

export const blendName = (key: string): string => blendNames[key] ?? blendNames[key.trim()] ?? key.trim();

// key matches layers across versions by kind, name and how many earlier
// layers share that name, so renamed duplicates do not pair up wrongly.
function keyed(layers: ImageLayer[]): Map<string, ImageLayer> {
  const seen = new Map<string, number>();
  const out = new Map<string, ImageLayer>();
  for (const l of layers) {
    const base = translate("layers.text", { kind: l.kind, name: l.name });
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    out.set(translate("layers.text2", { base, n }), l);
  }
  return out;
}

function changesBetween(a: ImageLayer, b: ImageLayer): string[] {
  const out: string[] = [];
  if (a.hash !== b.hash) out.push(translate("layers.pixels"));
  if (a.x !== b.x || a.y !== b.y || a.width !== b.width || a.height !== b.height) out.push(translate("layers.bounds"));
  if (a.visible !== b.visible) out.push(b.visible ? translate("layers.shown") : translate("layers.hidden"));
  if (a.opacity !== b.opacity) out.push(translate("layers.opacity", { value: Math.round((a.opacity / 255) * 100), value2: Math.round((b.opacity / 255) * 100) }));
  if (a.blend !== b.blend) out.push(translate("layers.text3", { value: blendName(a.blend), value2: blendName(b.blend) }));
  if (a.depth !== b.depth) out.push(translate("layers.moved"));
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

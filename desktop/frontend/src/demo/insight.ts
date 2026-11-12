import type { AssetReferences, Origin, OriginInput, Provenance } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import { ago, HERO, ICON, MODEL, ROCK, SCENE } from "./data";

export const HERO_MAT = "Assets/Materials/Hero.mat";
export const ROCK_MAT = "Assets/Materials/Rock.mat";
export const GROUND_MAT = "Assets/Materials/Ground.mat";
export const HERO_PREFAB = "Assets/Characters/Hero/Hero.prefab";
export const GOBLIN_PREFAB = "Assets/Characters/Goblin/Goblin.prefab";
const DESERT = "Assets/Levels/Desert.unity";
const CAVE = "Assets/Levels/Cave/Cave.unity";
const GRASS = "Assets/Textures/Environment/grass_albedo.png";
const SAND = "Assets/Textures/Environment/sand_albedo.png";
const BARK = "Assets/Textures/Environment/bark_albedo.png";
const NORMAL = "Assets/Characters/Hero/hero_normal.png";
const GOBLIN_TEX = "Assets/Characters/Goblin/goblin_albedo.png";
const GOBLIN_FBX = "Assets/Characters/Goblin/goblin.fbx";

export const demoMaterials: [string, number][] = [
  [HERO_MAT, 1_240],
  [ROCK_MAT, 1_180],
  [GROUND_MAT, 1_310],
  [GOBLIN_PREFAB, 28_400],
];

const uses: Record<string, string[]> = {
  [HERO_MAT]: [HERO, NORMAL],
  [ROCK_MAT]: [ROCK],
  [GROUND_MAT]: [GRASS, SAND],
  [HERO_PREFAB]: [HERO_MAT, MODEL],
  [GOBLIN_PREFAB]: [GOBLIN_TEX, GOBLIN_FBX],
  [SCENE]: [HERO_PREFAB, ROCK_MAT, GROUND_MAT],
  [DESERT]: [HERO_PREFAB, GROUND_MAT, ROCK_MAT],
  [CAVE]: [ROCK_MAT, GOBLIN_PREFAB],
};

const broken: Record<string, string> = { [CAVE]: "guid:9f2c41d07be34a6c8e1f0a55d2b7c613" };

const guid = (path: string) => {
  let h = 0;
  for (const c of path) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return `guid:${h.toString(16).padStart(8, "0")}${"0".repeat(24)}`;
};

const engineAsset = (path: string) => path.startsWith("Assets/") && !path.endsWith(".cs");

// demoRefs answers AssetRefs for the demo project's scenes, prefabs,
// materials and textures; bark_albedo.png is used by nothing.
export function demoRefs(path: string): AssetReferences {
  if (!engineAsset(path)) return { path, asset: false, uses: [], usedBy: [], unknown: [], scanned: 0, truncated: false, partial: false };
  const out = (uses[path] ?? []).map((p) => ({ path: p, raw: guid(p), resolved: true }));
  if (broken[path]) out.push({ path: "", raw: broken[path], resolved: false });
  const usedBy = Object.entries(uses)
    .filter(([, targets]) => targets.includes(path))
    .map(([src]) => src)
    .sort((a, b) => a.localeCompare(b));
  return { path, asset: true, uses: out, usedBy, unknown: [], scanned: Object.keys(uses).length, truncated: false, partial: false };
}

const seeded: Record<string, Origin> = {
  [HERO]: {
    path: HERO,
    status: "edited",
    required: true,
    hasRecord: true,
    exists: true,
    tool: "ComfyUI",
    model: "sdxl_base_1.0",
    seed: "1849203317",
    source: "png-text",
    recordedAt: ago(60 * 24 * 9),
    license: "CreativeML Open RAIL++-M",
    humanEdited: true,
    promptHash: "5f1d7a9c0b2e4f6a8c1d3e5f7a9b0c2d4e6f8a1b3c5d7e9f0a2b4c6d8e0f1a3b",
    digitalSourceType: "",
    note: "Base pass generated, armor trim painted by Mina",
  },
  [ROCK]: {
    path: ROCK,
    status: "recorded",
    required: true,
    hasRecord: true,
    exists: true,
    tool: "Adobe Firefly",
    model: "Image 3",
    seed: "",
    source: "c2pa",
    recordedAt: ago(60 * 24 * 21),
    license: "Adobe Firefly terms",
    humanEdited: false,
    promptHash: "",
    digitalSourceType: "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia", // NOSONAR: IPTC identifier, not a fetched URL
    note: "",
  },
};

const text = (v: unknown) => (typeof v === "string" ? v.trim() : "");

const requiresOrigin = (path: string) => /^Assets\/(UI|Textures|Characters)\/.+\.png$/.test(path);

// demoProvenance keeps the records the demo session added on top of the
// seeded ones; icon_sword.png is required but missing until a record is added.
export function demoProvenance() {
  const records: Record<string, Origin> = JSON.parse(JSON.stringify(seeded));
  return {
    read: (paths: string[]): Provenance => ({
      enabled: true,
      origins: paths.flatMap((p) => {
        if (records[p]) return [records[p]];
        if (!requiresOrigin(p)) return [];
        return [{ ...blank(p), required: true, exists: true, status: "missing" }];
      }),
    }),
    set: (input: OriginInput) => {
      const origin: Origin = {
        ...blank(input.path),
        status: "recorded",
        required: requiresOrigin(input.path),
        hasRecord: true,
        exists: true,
        tool: text(input.tool),
        model: text(input.model),
        seed: text(input.seed),
        source: "manual",
        recordedAt: new Date().toISOString(),
        license: text(input.license),
        humanEdited: !!input.humanEdited,
        promptHash: text(input.prompt) ? "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" : "",
        note: text(input.note),
      };
      records[input.path] = origin;
      return { origin, created: false };
    },
  };
}

function blank(path: string): Origin {
  return { path, status: "", required: false, hasRecord: false, exists: false, tool: "", model: "", seed: "", source: "", recordedAt: "", license: "", humanEdited: false, promptHash: "", digitalSourceType: "", note: "" };
}

export const UNUSED_TEXTURE = BARK;
export const MISSING_ORIGIN = ICON;

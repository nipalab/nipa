import type { FileDetails, FileDiff, FileEntry, FileListing, FileSearch, ImageInfo } from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import { BALANCE, commits, HERO, ICON, ITEMS, MODEL, MOVE, MUSIC, ROCK, SCENE, type DemoChanges } from "./data";
import { demoMaterials } from "./insight";
import { textureSvg } from "./media";
import { rank } from "@/lib/fuzzy";

const kinds: [RegExp, string][] = [
  [/\.(png|tga|psd|jpg|exr)$/, "image"],
  [/\.(glb|gltf|fbx|obj|blend)$/, "model"],
  [/\.(wav|ogg|mp3)$/, "audio"],
  [/\.(csv|tsv|xlsx)$/, "table"],
  [/\.(unity|prefab|asset|mat)$/, "scene"],
];

const kindOf = (path: string) => kinds.find(([re]) => re.test(path))?.[1] ?? "text";

const tracked: [string, number][] = [
  ["README.md", 2_140],
  [".nipaignore", 96],
  [".nipaassets.yml", 410],
  ["Assets/Audio/Music/theme_main.wav", 41_200_000],
  ["Assets/Audio/Music/theme_battle.wav", 38_900_000],
  ["Assets/Audio/Sfx/footstep_grass_01.wav", 182_000],
  ["Assets/Audio/Sfx/footstep_stone_01.wav", 176_400],
  ["Assets/Audio/Sfx/sword_swing.wav", 94_100],
  [HERO, 3_410_222],
  ["Assets/Characters/Hero/hero_normal.png", 3_120_870],
  ["Assets/Characters/Hero/hero_rig.blend", 22_800_120],
  [MODEL, 960_000],
  ["Assets/Characters/Hero/Hero.prefab", 41_220],
  ["Assets/Characters/Goblin/goblin_albedo.png", 2_204_810],
  ["Assets/Characters/Goblin/goblin.fbx", 6_720_400],
  [ITEMS, 4_820],
  [BALANCE, 51_904],
  ["Assets/Data/enemies.csv", 2_210],
  [SCENE, 88_104],
  ["Assets/Levels/Desert.unity", 120_380],
  ["Assets/Levels/Cave/Cave.unity", 76_020],
  [ROCK, 1_905_000],
  ["Assets/Textures/Environment/grass_albedo.png", 1_620_300],
  ["Assets/Textures/Environment/bark_albedo.png", 1_880_100],
  ["Assets/Textures/Environment/sand_albedo.png", 1_402_500],
  ["Assets/UI/icon_shield.png", 17_440],
  ["Assets/UI/icon_potion.png", 16_980],
  ["ProjectSettings/ProjectVersion.txt", 82],
  [MOVE, 1_204],
  ["Source/Player/PlayerInput.cs", 2_960],
  ["Source/Combat/Damage.cs", 2_204],
  ...demoMaterials,
];

const palettes: Record<string, [[string, string, string], string]> = {
  "Assets/Characters/Hero/hero_normal.png": [["#4338ca", "#6366f1", "#a5b4fc"], "#eef2ff"],
  "Assets/Characters/Goblin/goblin_albedo.png": [["#365314", "#4d7c0f", "#a3e635"], "#f7fee7"],
  "Assets/Textures/Environment/grass_albedo.png": [["#14532d", "#166534", "#4ade80"], "#dcfce7"],
  "Assets/Textures/Environment/bark_albedo.png": [["#431407", "#7c2d12", "#c2410c"], "#ffedd5"],
  "Assets/Textures/Environment/sand_albedo.png": [["#a16207", "#ca8a04", "#fde047"], "#fefce8"],
  "Assets/UI/icon_shield.png": [["#1e3a8a", "#1d4ed8", "#93c5fd"], "#eff6ff"],
  "Assets/UI/icon_potion.png": [["#831843", "#be185d", "#f9a8d4"], "#fdf2f8"],
};

export const fileImages: Record<string, ImageInfo> = Object.fromEntries(
  Object.entries(palettes).map(([path, [palette, accent]]) => [
    path,
    {
      width: 1024,
      height: 1024,
      format: "png",
      bitDepth: 8,
      hasAlpha: true,
      size: tracked.find(([p]) => p === path)?.[1] ?? 0,
      previewWidth: 128,
      previewHeight: 128,
      src: textureSvg(palette, accent),
      encoding: "",
      levels: 1,
      faces: 1,
      cube: false,
      level: 0,
      face: 0,
      hdr: false,
      layers: null,
    },
  ]),
);

const texts: Record<string, string> = {
  "README.md": "# Starfall\n\nAction RPG prototype.\n\n- Unity 6 project under Assets/\n- Gameplay code under Source/\n- Lock binary assets before editing them.\n",
  [MOVE]: "using UnityEngine;\n\npublic class Movement : MonoBehaviour\n{\n    public float speed = 6f;\n    public float dashSpeed = 18f;\n\n    void Update()\n    {\n        var input = new Vector3(Input.GetAxis(\"Horizontal\"), 0, Input.GetAxis(\"Vertical\"));\n        transform.position += input * speed * Time.deltaTime;\n    }\n}\n",
};

const entry = (path: string, size: number, status: string, staged: boolean, isTracked: boolean): FileEntry => {
  const kind = kindOf(path);
  return {
    name: path.slice(path.lastIndexOf("/") + 1),
    path,
    isDir: false,
    size,
    binary: kind !== "text" && (kind !== "table" || path.endsWith(".xlsx")),
    kind,
    status,
    staged,
    childrenChanged: false,
    tracked: isTracked,
    hash: `h-${path}`,
    mtime: status ? 1 : 0,
  };
};

// demoFiles lists one folder of the demo project, statuses from the changes.
export function demoFiles(dir: string, changes: DemoChanges): FileListing & { entries: FileEntry[] } {
  const status = new Map<string, { status: string; staged: boolean }>();
  for (const f of changes.unstaged) status.set(f.path, { status: f.status, staged: false });
  for (const f of changes.staged) status.set(f.path, { status: f.status, staged: true });
  const all: [string, number, boolean][] = [...tracked.map(([p, s]): [string, number, boolean] => [p, s, true]), [ICON, 18_220, false]];
  const prefix = dir ? `${dir}/` : "";
  const dirs = new Map<string, FileEntry>();
  const files: FileEntry[] = [];
  for (const [path, size, isTracked] of all) {
    if (!path.startsWith(prefix)) continue;
    const rest = path.slice(prefix.length);
    const cut = rest.indexOf("/");
    const st = status.get(path);
    if (cut < 0) {
      files.push(entry(path, size, st?.status ?? "", st?.staged ?? false, isTracked));
      continue;
    }
    const name = rest.slice(0, cut);
    const folder = dirs.get(name) ?? { ...entry(prefix + name, 0, "", false, true), isDir: true, kind: "", binary: false, hash: "" };
    if (st) folder.childrenChanged = true;
    dirs.set(name, folder);
  }
  const byName = (a: FileEntry, b: FileEntry) => a.name.localeCompare(b.name);
  return { dir, entries: [...[...dirs.values()].sort(byName), ...files.sort(byName)] };
}

export function demoFileInfo(path: string, changes: DemoChanges): FileDetails {
  const dir = path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "";
  const found = demoFiles(dir, changes).entries.find((e) => e.path === path) ?? entry(path, 0, "", false, true);
  const last = path === HERO ? commits[0] : path === MOVE ? commits[1] : path === SCENE ? commits[2] : path === MUSIC ? commits[4] : commits[(path.length % 6) + 3];
  return { entry: found, lastCommit: found.tracked ? last : null };
}

export function demoFileView(path: string): FileDiff | null {
  const kind = kindOf(path);
  const size = tracked.find(([p]) => p === path)?.[1] ?? 18_220;
  const view: FileDiff & { hunks: NonNullable<FileDiff["hunks"]> } = { path, status: "", kind, old: null, new: { path, mode: 420, size, binary: kind !== "text" && kind !== "table", hash: "" }, additions: 0, deletions: 0, hunks: [] };
  const text = texts[path];
  if (kind === "text") {
    const lines = (text ?? `// ${path}\n`).replace(/\n$/, "").split("\n");
    view.hunks = [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: lines.length, lines: lines.map((t, i) => ({ kind: "context", old: 0, new: i + 1, text: t })) }];
  }
  return view;
}

// demoSearch ranks the demo project's tracked files like the daemon does.
export function demoSearch(query: string, limit: number): FileSearch {
  const hits = rank(tracked, query, ([p]) => p, limit || 50).map(({ item: [path, size] }) => {
    const kind = kindOf(path);
    return { path, name: path.slice(path.lastIndexOf("/") + 1), size, binary: kind !== "text" && kind !== "table", kind };
  });
  return { hits, truncated: false };
}

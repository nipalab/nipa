import type {
  Activity,
  AudioSide,
  Blame,
  SceneMerge,
  Branch,
  Changes,
  Commit,
  ConflictSet,
  ConflictTexts,
  DiffHunk,
  DiskUsage,
  Draft,
  FileChange,
  FileDiff,
  FileSide,
  FontCompare,
  FontSide,
  ImageInfo,
  LLMSettings,
  LockStatus,
  MergeRequest,
  PushChecks,
  RemoteProject,
  Repo,
  SceneCompare,
  Shelf,
  Tag,
  TableCompare,
  VideoCompare,
  VideoSide,
} from "../../bindings/github.com/nipalab/nipa/desktop/internal/service/models";
import { glyphSheetSvg, spectrogramBytes, textureSvg, waveformPeaks } from "./media";

export const ROOT = "/home/alex/studio/starfall";
export const SERVER = "nipalab.com";

const now = Date.now();
export const ago = (minutes: number): string => new Date(now - minutes * 60_000).toISOString();

export const people = {
  me: { id: "u-alex", name: "Alex Rivera" },
  mina: { id: "u-mina", name: "Mina Okafor" },
  jon: { id: "u-jon", name: "Jon Backstrom" },
  sam: { id: "u-sam", name: "Sam Patel" },
};

export const repos: Repo[] = [
  { root: ROOT, name: "starfall", url: `${SERVER}/acme/starfall`, branch: "feature/hero-rework", sparse: null },
  { root: "/home/alex/studio/starfall-audio", name: "starfall-audio", url: `${SERVER}/acme/starfall-audio`, branch: "main", sparse: ["Music", "Sfx"] },
  { root: "/home/alex/studio/launcher", name: "launcher", url: `${SERVER}/acme/launcher`, branch: "main", sparse: null },
];

const side = (path: string, size: number, binary: boolean): FileSide => ({ path, mode: 420, size, binary, hash: "" });

export const HERO = "Assets/Characters/Hero/hero_albedo.png";
export const MODEL = "Assets/Characters/Hero/ring_of_dawn.glb";
export const MUSIC = "Assets/Audio/Music/theme_main.wav";
export const ITEMS = "Assets/Data/items.csv";
export const BALANCE = "Assets/Data/balance.xlsx";
export const MOVE = "Source/Player/Movement.cs";
export const ICON = "Assets/UI/icon_sword.png";
export const CUTSCENE = "Assets/Video/intro_cutscene.mp4";
export const FONT = "Assets/UI/Fonts/Starfall-Regular.ttf";
export const SCULPT = "Art/Source/hero_sculpt.blend";
export const SCENE = "Assets/Levels/Forest.unity";
export const ROCK = "Assets/Textures/Environment/rock_albedo.png";
export const DAMAGE = "Source/Combat/Damage.cs";

export type DemoChanges = Omit<Changes, "staged" | "unstaged" | "conflicts" | "resolved"> & { staged: FileChange[]; unstaged: FileChange[]; conflicts: string[]; resolved: string[] };

export const changes = (scenario: string): DemoChanges => {
  if (scenario === "conflicts") {
    return {
      branch: "feature/hero-rework",
      staged: [],
      unstaged: [
        { path: DAMAGE, status: "modified", kind: "text" },
        { path: ROCK, status: "modified", kind: "image" },
        { path: ITEMS, status: "modified", kind: "table" },
      ],
      conflicts: [DAMAGE, ROCK],
      resolved: [],
      pending: "merge",
    };
  }
  return {
    branch: "feature/hero-rework",
    staged: [
      { path: HERO, status: "modified", kind: "image" },
      { path: MODEL, status: "modified", kind: "model" },
    ],
    unstaged: [
      { path: MUSIC, status: "modified", kind: "audio" },
      { path: ITEMS, status: "modified", kind: "table" },
      { path: BALANCE, status: "modified", kind: "table" },
      { path: MOVE, status: "modified", kind: "text" },
      { path: SCENE, status: "modified", kind: "scene" },
      { path: ICON, status: "untracked", kind: "image" },
      { path: CUTSCENE, status: "modified", kind: "video" },
      { path: FONT, status: "modified", kind: "font" },
      { path: SCULPT, status: "modified", kind: "opaque" },
    ],
    conflicts: [],
    resolved: [],
  };
};

const L = (kind: "context" | "added" | "removed", o: number, n: number, text: string) => ({ kind, old: o, new: n, text });

const moveHunks: DiffHunk[] = [
  {
    oldStart: 18,
    oldLines: 11,
    newStart: 18,
    newLines: 17,
    lines: [
      L("context", 18, 18, "    void Update()"),
      L("context", 19, 19, "    {"),
      L("context", 20, 20, "        var input = ReadInput();"),
      L("removed", 21, 0, "        velocity = input * speed;"),
      L("removed", 22, 0, "        controller.Move(velocity * Time.deltaTime);"),
      L("added", 0, 21, "        var target = input * speed;"),
      L("added", 0, 22, "        velocity = Vector3.MoveTowards(velocity, target, acceleration * Time.deltaTime);"),
      L("added", 0, 23, "        if (IsDashing)"),
      L("added", 0, 24, "        {"),
      L("added", 0, 25, "            velocity += dashDirection * dashForce;"),
      L("added", 0, 26, "            dashTimer -= Time.deltaTime;"),
      L("added", 0, 27, "        }"),
      L("added", 0, 28, "        controller.Move(velocity * Time.deltaTime);"),
      L("context", 23, 29, "    }"),
      L("context", 24, 30, ""),
      L("context", 25, 31, "    bool IsDashing => dashTimer > 0f;"),
      L("context", 26, 32, "}"),
    ],
  },
];

const sceneHunks: DiffHunk[] = [
  {
    oldStart: 112,
    oldLines: 6,
    newStart: 112,
    newLines: 6,
    lines: [
      L("context", 112, 112, "  m_Name: Lantern"),
      L("removed", 113, 0, "  m_LocalPosition: {x: 4.2, y: 1.1, z: -2}"),
      L("added", 0, 113, "  m_LocalPosition: {x: 4.2, y: 1.4, z: -2.5}"),
      L("context", 114, 114, "  m_LocalScale: {x: 1, y: 1, z: 1}"),
      L("context", 115, 115, "  m_Layer: 0"),
      L("context", 116, 116, "  m_IsActive: 1"),
    ],
  },
];

const diffOf = (path: string, kind: string, status: string, oldSize: number, newSize: number, hunks: DiffHunk[] | null, extra: Partial<FileDiff> = {}): FileDiff => {
  const binary = kind !== "text" && kind !== "table" && kind !== "scene";
  const lines = hunks?.flatMap((h) => h.lines ?? []) ?? [];
  return {
    path,
    status,
    kind,
    old: status === "added" || status === "untracked" ? null : side(path, oldSize, binary),
    new: status === "deleted" ? null : side(path, newSize, binary),
    additions: lines.filter((l) => l.kind === "added").length,
    deletions: lines.filter((l) => l.kind === "removed").length,
    hunks,
    ...extra,
  };
};

export const diffs: Record<string, FileDiff> = {
  [HERO]: diffOf(HERO, "image", "modified", 3_410_222, 3_602_118, null),
  [MODEL]: diffOf(MODEL, "model", "modified", 812_400, 958_660, null),
  [MUSIC]: diffOf(MUSIC, "audio", "modified", 28_450_000, 29_804_000, null),
  [ITEMS]: diffOf(ITEMS, "table", "modified", 4_820, 5_330, []),
  [BALANCE]: diffOf(BALANCE, "table", "modified", 48_210, 51_904, null, { old: side(BALANCE, 48_210, true), new: side(BALANCE, 51_904, true) }),
  [MOVE]: diffOf(MOVE, "text", "modified", 1_204, 1_618, moveHunks),
  [SCENE]: diffOf(SCENE, "scene", "modified", 88_104, 88_130, sceneHunks),
  [ICON]: diffOf(ICON, "image", "untracked", 0, 18_220, null, { untracked: true }),
  [ROCK]: diffOf(ROCK, "image", "modified", 1_905_000, 2_040_000, null),
  [DAMAGE]: diffOf(DAMAGE, "text", "modified", 2_204, 2_390, moveHunks),
  [CUTSCENE]: diffOf(CUTSCENE, "video", "modified", 48_210_442, 51_902_118, null),
  [FONT]: diffOf(FONT, "font", "modified", 168_204, 181_996, null),
  [SCULPT]: diffOf(SCULPT, "opaque", "modified", 84_120_400, 86_004_112, null),
};

const imageInfo = (palette: [string, string, string], accent: string, size: number): ImageInfo => ({
  width: 512,
  height: 512,
  format: "png",
  bitDepth: 8,
  hasAlpha: true,
  size,
  previewWidth: 512,
  previewHeight: 512,
  src: textureSvg(palette, accent),
  encoding: "",
  levels: 1,
  faces: 1,
  cube: false,
  level: 0,
  face: 0,
  hdr: false,
  layers: null,
  thumbnail: false,
});

export const images: Record<string, { old: ImageInfo | null; new: ImageInfo | null }> = {
  [HERO]: {
    old: imageInfo(["#1e3a8a", "#312e81", "#3b82f6"], "#e0e7ff", 3_410_222),
    new: imageInfo(["#6d28d9", "#be185d", "#f59e0b"], "#fff7ed", 3_602_118),
  },
  [ICON]: { old: null, new: imageInfo(["#0f766e", "#164e63", "#2dd4bf"], "#ecfeff", 18_220) },
  [ROCK]: {
    old: imageInfo(["#3f3f46", "#27272a", "#a1a1aa"], "#e4e4e7", 1_905_000),
    new: imageInfo(["#52525b", "#44403c", "#d6d3d1"], "#fafaf9", 2_040_000),
  },
  [SCULPT]: {
    old: { ...imageInfo(["#292524", "#57534e", "#d6d3d1"], "#f5f5f4", 84_120_400), width: 256, height: 256, format: "Blender thumbnail", thumbnail: true },
    new: { ...imageInfo(["#292524", "#78716c", "#fbbf24"], "#fffbeb", 86_004_112), width: 256, height: 256, format: "Blender thumbnail", thumbnail: true },
  },
};

const audioSide = (path: string, seed: number, loud: number, size: number, rms: number, lufs: number): AudioSide => ({
  path,
  format: "wav",
  size,
  stats: { codec: "PCM 16-bit", sampleRate: 48000, channels: 2, bitDepth: 16, frames: 48000 * 142, duration: 142, peakDb: -0.4 - seed * 0.2, rmsDb: rms, loudness: lufs, clipped: 0, truncated: false },
  peaks: [waveformPeaks(seed, 900, loud), waveformPeaks(seed + 1.3, 900, loud)],
  spectrogram: spectrogramBytes(seed, 480, 128, loud),
  specColumns: 480,
  specRows: 128,
  specMinHz: 0,
  specMaxHz: 24000,
  playback: "",
});

const videoSide = (path: string, size: number, bitrate: number, duration: number): VideoSide => ({
  path,
  format: "mp4",
  size,
  stats: { container: "mp4", duration, width: 1920, height: 1080, videoCodec: "H.264", frameRate: 30, audioCodec: "AAC", sampleRate: 48000, channels: 2, bitrate, tracks: 2 },
  statsError: "",
  url: "",
  media: "",
});

export const video: VideoCompare = {
  old: { ...videoSide(CUTSCENE, 48_210_442, 8_571_000, 45), statsError: "playback is not available in the demo" },
  new: { ...videoSide(CUTSCENE, 51_902_118, 8_812_000, 47.12), statsError: "playback is not available in the demo" },
};

const fontInfo = (version: string, glyphs: number, codepoints: number, xHeight: number) => ({
  family: "Starfall",
  style: "Regular",
  fullName: "Starfall Regular",
  version,
  glyphs,
  unitsPerEm: 1000,
  ascent: 920,
  descent: 240,
  lineGap: 0,
  capHeight: 700,
  xHeight,
  codepoints,
  scripts: [
    { script: "Latin", count: codepoints - 180 },
    { script: "Common", count: 120 },
    { script: "Greek", count: 60 },
  ],
  ranges: [
    { first: 0x20, last: 0x7e },
    { first: 0xa0, last: 0x17f },
  ],
});

const fontSide = (version: string, glyphs: number, codepoints: number, xHeight: number, weight: number, size: number): FontSide => ({
  path: FONT,
  format: "ttf",
  size,
  info: fontInfo(version, glyphs, codepoints, xHeight),
  sheet: glyphSheetSvg(weight),
});

export const font: FontCompare = {
  old: fontSide("1.002", 412, 398, 500, 400, 168_204),
  new: fontSide("1.100", 476, 458, 512, 500, 181_996),
  changes: {
    added: [
      { first: 0x391, last: 0x3a9 },
      { first: 0x2190, last: 0x2193 },
    ],
    removed: [],
    addedCount: 60,
    removedCount: 0,
    metrics: [
      { name: "Version", old: "1.002", new: "1.100" },
      { name: "Glyphs", old: "412", new: "476" },
      { name: "x-height", old: "500", new: "512" },
    ],
  },
};

export const audio = {
  old: audioSide(MUSIC, 1, 0.85, 28_450_000, -16.2, -14.8),
  new: audioSide(MUSIC, 2, 1, 29_804_000, -12.9, -11.6),
};

export const table: TableCompare = {
  columns: [
    { name: "id", oldName: "id", old: 0, new: 0 },
    { name: "name", oldName: "name", old: 1, new: 1 },
    { name: "damage", oldName: "damage", old: 2, new: 2 },
    { name: "weight", oldName: "weight", old: 3, new: 3 },
    { name: "rarity", oldName: "", old: -1, new: 4 },
  ],
  rows: [
    { status: "same", oldLine: 2, newLine: 2, cells: ["1001", "Iron Sword", "12", "3.5", "common"], old: null, changed: null },
    { status: "changed", oldLine: 3, newLine: 3, cells: ["1002", "Ring of Dawn", "18", "0.2", "rare"], old: ["1002", "Ring of Dawn", "14", "0.2", ""], changed: [2, 4] },
    { status: "changed", oldLine: 4, newLine: 4, cells: ["1003", "Storm Bow", "22", "2.1", "epic"], old: ["1003", "Storm Bow", "20", "2.4", ""], changed: [2, 3, 4] },
    { status: "added", oldLine: 0, newLine: 5, cells: ["1004", "Lantern of Echoes", "0", "1.0", "rare"], old: null, changed: null },
    { status: "removed", oldLine: 5, newLine: 0, cells: ["0999", "Debug Hammer", "999", "0", ""], old: null, changed: null },
    { status: "same", oldLine: 6, newLine: 6, cells: ["1005", "Healing Draught", "0", "0.4", "common"], old: null, changed: null },
  ],
  keyColumn: 0,
  delimiter: ",",
  rowsAdded: 1,
  rowsRemoved: 1,
  rowsChanged: 2,
  rowsMoved: 0,
  cellsChanged: 5,
  columnsAdded: 1,
  columnsRemoved: 0,
  sheets: [],
};

const sheetRow = (status: string, oldLine: number, newLine: number, cells: string[], old: string[] | null = null) => ({
  status,
  oldLine,
  newLine,
  cells,
  old,
  changed: old ? cells.flatMap((c, i) => (c === old[i] ? [] : [i])) : null,
});

const balanceSheets: Record<string, Omit<TableCompare, "sheet" | "sheets" | "delimiter" | "rowsAdded" | "rowsRemoved" | "rowsChanged" | "rowsMoved" | "cellsChanged" | "columnsAdded" | "columnsRemoved">> = {
  Weapons: {
    columns: ["id", "name", "tier", "damage", "speed", "crit", "cost"].map((name, i) => ({ name, oldName: name, old: i, new: i })),
    keyColumn: 0,
    rows: [
      sheetRow("same", 2, 2, ["W-001", "Iron Sword", "1", "12", "1.0", "0.05", "120"]),
      sheetRow("changed", 3, 3, ["W-002", "Storm Bow", "2", "22", "0.9", "0.10", "340"], ["W-002", "Storm Bow", "2", "20", "0.8", "0.10", "340"]),
      sheetRow("same", 4, 4, ["W-003", "Ember Staff", "2", "18", "0.7", "0.08", "360"]),
      sheetRow("changed", 5, 5, ["W-004", "Tide Hammer", "3", "34", "0.6", "0.12", "720"], ["W-004", "Tide Hammer", "3", "30", "0.6", "0.12", "650"]),
      sheetRow("same", 6, 6, ["W-005", "Glass Dagger", "1", "8", "1.6", "0.25", "90"]),
      sheetRow("added", 0, 7, ["W-006", "Lantern Pike", "3", "28", "0.8", "0.06", "680"]),
      sheetRow("same", 7, 8, ["W-007", "Bone Axe", "2", "24", "0.7", "0.07", "300"]),
      sheetRow("changed", 8, 9, ["W-008", "Sunsteel Blade", "4", "41", "1.0", "0.15", "1250"], ["W-008", "Sunsteel Blade", "4", "45", "1.0", "0.18", "1250"]),
    ],
  },
  Armor: {
    columns: ["id", "name", "slot", "armor", "weight"].map((name, i) => ({ name, oldName: name, old: i, new: i })),
    keyColumn: 0,
    rows: [
      sheetRow("changed", 2, 2, ["A-001", "Leather Cap", "head", "4", "0.8"], ["A-001", "Leather Cap", "head", "3", "0.8"]),
      sheetRow("same", 3, 3, ["A-002", "Chain Vest", "chest", "12", "6.5"]),
      sheetRow("removed", 4, 0, ["A-003", "Debug Plate", "chest", "999", "0"]),
    ],
  },
  "Loot tables": {
    columns: ["table", "item", "weight"].map((name, i) => ({ name, oldName: "", old: -1, new: i })),
    keyColumn: -1,
    rows: [sheetRow("added", 0, 2, ["forest", "W-006", "5"]), sheetRow("added", 0, 3, ["forest", "A-001", "20"])],
  },
  Notes: {
    columns: [{ name: "note", oldName: "note", old: 0, new: 0 }],
    keyColumn: -1,
    rows: [sheetRow("same", 2, 2, ["Balance pass for milestone 3"])],
  },
  Deprecated: {
    columns: [{ name: "id", oldName: "id", old: 0, new: -1 }],
    keyColumn: -1,
    rows: [sheetRow("removed", 2, 0, ["W-000"])],
  },
};

const sheetStatus: Record<string, string> = { Weapons: "changed", Armor: "changed", "Loot tables": "added", Notes: "same", Deprecated: "removed" };

export const workbook = (sheet: string): TableCompare => {
  const name = sheet in balanceSheets ? sheet : "Weapons";
  const counts = (rows: TableCompare["rows"]) => ({
    rowsAdded: rows!.filter((r) => r.status === "added").length,
    rowsRemoved: rows!.filter((r) => r.status === "removed").length,
    rowsChanged: rows!.filter((r) => r.status === "changed").length,
    cellsChanged: rows!.reduce((n, r) => n + (r.changed?.length ?? 0), 0),
  });
  const cur = balanceSheets[name];
  return {
    ...cur,
    ...counts(cur.rows),
    rowsMoved: 0,
    columnsAdded: cur.columns!.filter((c) => c.old < 0).length,
    columnsRemoved: cur.columns!.filter((c) => c.new < 0).length,
    delimiter: "",
    sheet: name,
    sheets: Object.entries(balanceSheets).map(([n, s]) => ({ name: n, status: sheetStatus[n], ...counts(s.rows) })),
  };
};

export const scene: SceneCompare = {
  engine: "unity",
  objects: [
    { id: "100", path: "Forest", type: "GameObject", parent: "" },
    { id: "210", path: "Forest/Lantern", type: "GameObject", parent: "100" },
    { id: "211", path: "Forest/Lantern > Transform", type: "Transform", parent: "210" },
    { id: "212", path: "Forest/Lantern > Light", type: "Light", parent: "210" },
    { id: "300", path: "Forest/Firefly Swarm", type: "GameObject", parent: "100" },
    { id: "400", path: "Forest/Old Signpost", type: "GameObject", parent: "100" },
  ],
  changes: [
    { status: "changed", id: "211", path: "Forest/Lantern > Transform", type: "Transform", props: [{ key: "m_LocalPosition", old: "{x: 4.2, y: 1.1, z: -2}", new: "{x: 4.2, y: 1.4, z: -2.5}" }] },
    {
      status: "changed",
      id: "212",
      path: "Forest/Lantern > Light",
      type: "Light",
      props: [
        { key: "m_Color", old: "{r: 1, g: 0.8, b: 0.5, a: 1}", new: "{r: 1, g: 0.7, b: 0.4, a: 1}" },
        { key: "m_Intensity", old: "1.2", new: "1.6" },
      ],
    },
    { status: "added", id: "300", path: "Forest/Firefly Swarm", type: "GameObject", props: [{ key: "m_Name", old: "", new: "Firefly Swarm" }] },
    { status: "removed", id: "400", path: "Forest/Old Signpost", type: "GameObject", props: [{ key: "m_Name", old: "Old Signpost", new: "" }] },
  ],
  added: 1,
  removed: 1,
  changed: 2,
  refs: [{ path: "Assets/VFX/Fireflies.prefab", status: "added" }],
};

const commit = (n: number, minutes: number, author: string, message: string): Commit => ({
  id: `c${n}`,
  hash: `${(n * 2654435761).toString(16).padStart(8, "0")}a1b2c3d4e5f6`.slice(0, 40),
  parents: n > 1 ? [`c${n - 1}`] : [],
  author,
  email: `${author.split(" ")[0].toLowerCase()}@acme.dev`,
  message,
  date: ago(minutes),
});

export const commits: Commit[] = [
  commit(12, 42, "Alex Rivera", "Rebuild hero armor albedo with the new dawn palette"),
  commit(11, 180, "Mina Okafor", "Add dash ability to player movement"),
  commit(10, 420, "Jon Backstrom", "Rework forest level lighting and lantern placement"),
  commit(9, 1_300, "Alex Rivera", "Replace ring of dawn mesh with the higher-poly version"),
  commit(8, 1_900, "Sam Patel", "Normalize loudness of the main theme (-14 LUFS)"),
  commit(7, 3_100, "Mina Okafor", "Balance weapon damage values in items.csv"),
  commit(6, 4_800, "Jon Backstrom", "Bake lightmaps for the cave level"),
  commit(5, 7_200, "Alex Rivera", "Import enemy skeleton rig and animations"),
  commit(4, 10_000, "Sam Patel", "Add footstep sound set for stone and grass"),
  commit(3, 14_400, "Mina Okafor", "Fix camera jitter when climbing ledges"),
  commit(2, 20_000, "Jon Backstrom", "Set up Unity project layout"),
  commit(1, 30_000, "Alex Rivera", "Initial import"),
];

export const commitDiffs = (): FileDiff[] => [diffs[HERO], diffs[MODEL], diffs[ITEMS], diffs[MOVE]];

export const branches: Branch[] = [
  { name: "main", commitId: "c11", default: true, protected: true },
  { name: "feature/hero-rework", commitId: "c12", default: false, protected: false },
  { name: "feature/dash-ability", commitId: "c11", default: false, protected: false },
  { name: "level/forest-lighting", commitId: "c10", default: false, protected: false },
  { name: "audio/loudness-pass", commitId: "c8", default: false, protected: false },
];

export const tags: Tag[] = [
  { name: "v0.9.0-alpha", commitId: "c8", message: "Vertical slice for the publisher demo", createdAt: ago(9_000), createdBy: "Mina Okafor" },
  { name: "v0.8.2", commitId: "c5", message: "Playtest build", createdAt: ago(18_000), createdBy: "Alex Rivera" },
  { name: "v0.8.0", commitId: "c2", message: "First internal build", createdAt: ago(30_000), createdBy: "Alex Rivera" },
];

const mr = (number: number, title: string, source: string, status: string, author: string, minutes: number, approvals: number, changesRequested: number, draft = false): MergeRequest => ({
  number,
  title,
  description: "",
  status,
  draft,
  source,
  target: "main",
  authorId: author,
  createdAt: ago(minutes + 400),
  updatedAt: ago(minutes),
  mergeCommitId: status === "merged" ? "c11" : "",
  mergeBaseId: "c8",
  mergedSourceId: status === "merged" ? "c11" : "",
  approvals,
  changesRequested,
  assignees: [],
});

export const mergeRequests: MergeRequest[] = [
  {
    ...mr(42, "Rework hero armor textures and ring mesh", "feature/hero-rework", "open", people.me.id, 38, 1, 0),
    description: "New dawn palette for the hero, replaces the ring mesh with the high-poly version and adds a dash to the movement controller.\n\nTested on PC and Steam Deck.",
    assignees: [people.mina],
  },
  mr(41, "Add dash ability", "feature/dash-ability", "open", people.mina.id, 210, 0, 1),
  mr(40, "Forest level lighting pass", "level/forest-lighting", "open", people.jon.id, 500, 2, 0),
  mr(39, "Loudness pass on music", "audio/loudness-pass", "merged", people.sam.id, 1_800, 1, 0),
  mr(38, "WIP: Cave lightmaps", "level/cave-bake", "open", people.jon.id, 3_000, 0, 0, true),
];

export const activityFor = (n: number): Activity => ({
  reviews: [
    { id: "r1", reviewer: people.mina, state: "approved", body: "The new palette reads much better in the dark areas. Nice work.", stale: false, dismissed: false, createdAt: ago(25) },
  ],
  threads: [
    {
      id: "t1",
      path: MOVE,
      oldLine: 0,
      newLine: 25,
      outdated: false,
      resolved: false,
      resolvedBy: "",
      createdAt: ago(30),
      comments: [
        { id: "k1", author: people.mina, body: "Should the dash force be scaled by `Time.deltaTime` too? It will feel different at 30 vs 144 fps.", createdAt: ago(30), edited: false },
        { id: "k2", author: people.me, body: "Good catch, I'll apply it to the velocity instead and add a test.", createdAt: ago(22), edited: false },
      ],
    },
  ],
  timeline: [
    { id: "e1", kind: "opened", actor: people.me, subject: { id: "", name: "" }, body: "", commitId: "", createdAt: ago(400 + n) },
    { id: "e2", kind: "review_requested", actor: people.me, subject: people.mina, body: "", commitId: "", createdAt: ago(390 + n) },
    { id: "e3", kind: "pushed", actor: people.me, subject: { id: "", name: "" }, body: "", commitId: "c12", createdAt: ago(60) },
  ],
  checks: [
    { name: "build-windows", state: "success", url: "", reporter: "ci", updatedAt: ago(50) },
    { name: "asset-validation", state: "success", url: "", reporter: "ci", updatedAt: ago(48) },
    { name: "unit-tests", state: "success", url: "", reporter: "ci", updatedAt: ago(45) },
  ],
});

export const locks = (): LockStatus => ({
  userId: people.me.id,
  branch: "feature/hero-rework",
  defaultBranch: false,
  canReleaseAny: true,
  locks: [
    { id: "l1", path: MUSIC, branch: "feature/hero-rework", global: false, heldBy: people.me.id, heldByName: people.me.name, mine: true, mergeRequest: 0, acquiredAt: ago(50), inScope: true, requests: [{ id: "q1", requesterId: people.jon.id, requesterName: people.jon.name, message: "Need the track for the trailer cut", requestedAt: ago(20) }] },
    { id: "l2", path: "Assets/Characters/Hero/hero_rig.blend", branch: "", global: true, heldBy: people.jon.id, heldByName: people.jon.name, mine: false, mergeRequest: 0, acquiredAt: ago(240), inScope: true, requests: [] },
    { id: "l3", path: "Assets/Levels/Cave/", branch: "", global: true, heldBy: people.sam.id, heldByName: people.sam.name, mine: false, mergeRequest: 41, acquiredAt: ago(1_500), inScope: true, requests: [] },
    { id: "l4", path: "Assets/Textures/Environment/rock_albedo.png", branch: "feature/hero-rework", global: false, heldBy: people.me.id, heldByName: people.me.name, mine: true, mergeRequest: 0, acquiredAt: ago(130), inScope: true, requests: [] },
  ],
  files: [
    { path: HERO, state: "needed", lockPath: "", heldByName: "", tracked: true },
    { path: MODEL, state: "needed", lockPath: "", heldByName: "", tracked: true },
    { path: MUSIC, state: "mine", lockPath: MUSIC, heldByName: people.me.name, tracked: true },
    { path: ICON, state: "free", lockPath: "", heldByName: "", tracked: false },
  ],
});

export const pushChecks = (staged: string[]): PushChecks => {
  const violations = [
    { path: HERO, rule: "characters", limit: "max_texture_size", actual: "4096", max: "2048", message: "4096x4096 exceeds 2048x2048" },
    { path: MODEL, rule: "characters", limit: "max_triangles", actual: "48210", max: "30000", message: "48,210 triangles exceed 30,000" },
  ].filter((v) => staged.includes(v.path));
  const touchesHero = staged.includes(HERO);
  return {
    violations,
    broken: touchesHero ? [{ from: SCENE, raw: "guid:7f3c9a2e41d84b0c9e5a1f6b2d8c4e07", target: "Assets/Characters/Hero/hero_old.mat", reason: "removed" }] : [],
    newlyUnused: touchesHero ? ["Assets/Characters/Hero/hero_albedo_old.png"] : [],
    truncated: false,
    scanned: touchesHero ? 1284 : 0,
    warnings: [],
  };
};

export const diskUsage = (root: string): DiskUsage => {
  const repo = repos.find((r) => r.root === root);
  return {
    workingFiles: 18_420,
    workingBytes: 46_800_000_000,
    cacheObjects: 3_912,
    cacheBytes: 1_720_000_000,
    pins: [MUSIC],
    sparse: repo?.sparse ?? [],
  };
};

export const cleanUp = { objects: 1_204, bytes: 812_000_000, skipped: false, reason: "" };

export const llmSettings: LLMSettings = { endpoint: "http://localhost:11434/v1", model: "qwen2.5-coder:14b", keySaved: false, configured: true };

export const commitDraft = (staged: string[]): Draft => ({
  title: "",
  text: `Update hero textures and ring model\n\n${staged
    .slice(0, 4)
    .map((p) => `- ${p.split("/").pop()}`)
    .join("\n")}`,
  model: llmSettings.model,
  source: "",
  target: "",
});

export const mrDraft = (source: string, target: string): Draft => ({
  title: "Hero art pass: new albedo and ring model",
  text: "## Summary\n\nRepaints the hero albedo at 4K and replaces the ring model with a lighter mesh.\n\n## Notes\n\n- The old hero material is no longer used.",
  model: llmSettings.model,
  source: source || "art/hero-pass",
  target: target || "main",
});

export const projects: RemoteProject[] = [
  { org: "acme", orgName: "Acme Games", orgRole: "owner", project: "starfall", name: "Starfall", description: "Action RPG, Unity 6" },
  { org: "acme", orgName: "Acme Games", orgRole: "owner", project: "starfall-audio", name: "Starfall Audio", description: "Music, sfx and voice-over" },
  { org: "acme", orgName: "Acme Games", orgRole: "owner", project: "launcher", name: "Launcher", description: "Desktop launcher and patcher" },
  { org: "pixelforge", orgName: "Pixel Forge", orgRole: "member", project: "tiny-tactics", name: "Tiny Tactics", description: "Turn-based tactics, art pipeline" },
];

export const shelves: Shelf[] = [
  {
    id: "mx3k2a",
    name: "Hero armor experiments",
    createdAt: ago(95),
    branch: "feature/hero-rework",
    baseCommitId: "c12",
    size: 2_412_000,
    entries: [
      { path: HERO, status: "modified", staged: false, untracked: false, locked: true, size: 2_400_000, binary: true, kind: "image" },
      { path: MOVE, status: "modified", staged: true, untracked: false, locked: false, size: 3_100, binary: false, kind: "text" },
    ],
  },
  {
    id: "mx1q7c",
    name: "Before switching to main",
    createdAt: ago(1_560),
    branch: "feature/hero-rework",
    baseCommitId: "c10",
    size: 18_000,
    entries: [{ path: ITEMS, status: "modified", staged: false, untracked: false, locked: false, size: 18_000, binary: false, kind: "table" }],
  },
];

export const conflictSet: ConflictSet = {
  op: "merge",
  base: { id: "c8", label: "Common ancestor" },
  ours: { id: "c12", label: "feature/hero-rework" },
  theirs: { id: "c11", label: "main" },
  files: [
    { path: DAMAGE, kind: "text", binary: false, resolved: false, viewer: "text" },
    { path: ROCK, kind: "binary", binary: true, resolved: false, viewer: "image" },
  ],
};

const damageBase = `public static class Damage
{
    public static int Compute(Attack attack, Defense defense)
    {
        var raw = attack.Power - defense.Armor;
        return Mathf.Max(1, raw);
    }
}
`;

export const conflictTexts: ConflictTexts = {
  base: { text: damageBase, missing: false },
  ours: {
    text: damageBase.replace("var raw = attack.Power - defense.Armor;", "var raw = attack.Power * attack.Crit - defense.Armor;"),
    missing: false,
  },
  theirs: {
    text: damageBase.replace("var raw = attack.Power - defense.Armor;", "var raw = attack.Power - defense.Armor * defense.Resist;"),
    missing: false,
  },
  working: {
    text: damageBase.replace(
      "        var raw = attack.Power - defense.Armor;\n",
      "<<<<<<< ours\n        var raw = attack.Power * attack.Crit - defense.Armor;\n=======\n        var raw = attack.Power - defense.Armor * defense.Resist;\n>>>>>>> theirs\n",
    ),
    missing: false,
  },
};

export const sceneMerge: SceneMerge = { merged: "[gd_scene load_steps=3 format=3]\n", clean: true, conflicts: [] };

// blame attributes a demo file's lines in runs of earlier commits; lines the
// working copy added are not pushed yet.
export const blame = (path: string, revision: string): Blame => {
  const rows = diffs[path]?.hunks?.flatMap((h) => h.lines ?? []) ?? [];
  const total = Math.max(0, ...rows.map((l) => l.new));
  const added = new Set(rows.filter((l) => l.kind === "added").map((l) => l.new));
  const owners = ["c2", "c3", "c7", "c3"];
  const lines = Array.from({ length: total }, (_, i) =>
    added.has(i + 1) ? { commitId: revision ? "c11" : "", uncommitted: !revision } : { commitId: owners[Math.floor(i / 5) % owners.length], uncommitted: false },
  );
  const used = new Set(lines.map((l) => l.commitId));
  return { path, revision, lines, commits: commits.filter((c) => used.has(c.id)), truncated: false };
};

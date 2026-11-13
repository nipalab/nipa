export type Theme = "dark" | "light";

export type Step = { tab: string } | { button: string } | { text: string } | { wait: number };

export type Screen = {
  name: string;
  scenario: "default" | "conflicts";
  steps: Step[];
  settle: number;
};

export const VIEWPORT = { width: 1440, height: 900 } as const;
export const THEMES: Theme[] = ["dark", "light"];

export const screens: Screen[] = [
  { name: "changes-image", scenario: "default", steps: [{ text: "hero_albedo.png" }], settle: 1500 },
  { name: "changes-model", scenario: "default", steps: [{ text: "ring_of_dawn.glb" }], settle: 3500 },
  { name: "changes-audio", scenario: "default", steps: [{ text: "theme_main.wav" }], settle: 1500 },
  { name: "changes-table", scenario: "default", steps: [{ text: "items.csv" }], settle: 1000 },
  { name: "changes-workbook", scenario: "default", steps: [{ text: "balance.xlsx" }], settle: 1000 },
  { name: "changes-text", scenario: "default", steps: [{ text: "Movement.cs" }], settle: 1000 },
  {
    name: "files",
    scenario: "default",
    steps: [{ tab: "Files" }, { text: "Assets" }, { text: "Characters" }, { text: "Hero" }, { button: "Thumbnails" }, { text: "hero_albedo.png" }],
    settle: 1500,
  },
  {
    name: "files-references",
    scenario: "default",
    steps: [{ tab: "Files" }, { text: "Assets" }, { text: "Textures" }, { text: "Environment" }, { text: "rock_albedo.png" }],
    settle: 1500,
  },
  { name: "history", scenario: "default", steps: [{ tab: "History" }, { text: "Rebuild hero armor albedo" }], settle: 1500 },
  { name: "branches", scenario: "default", steps: [{ tab: "Branches & tags" }], settle: 800 },
  { name: "merge-request", scenario: "default", steps: [{ tab: "Merge requests" }, { text: "Rework hero armor textures" }], settle: 1500 },
  { name: "merge-request-files", scenario: "default", steps: [{ tab: "Merge requests" }, { text: "Rework hero armor textures" }, { tab: "File changes" }, { text: "Movement.cs" }], settle: 1500 },
  { name: "conflicts", scenario: "conflicts", steps: [{ tab: "Conflicts" }, { text: "Damage.cs" }], settle: 1500 },
  { name: "locks", scenario: "default", steps: [{ tab: "Locks" }], settle: 800 },
  { name: "clone", scenario: "default", steps: [{ button: "Clone a repository" }], settle: 1200 },
  { name: "settings", scenario: "default", steps: [{ button: "Settings" }], settle: 800 },
];

export const fileName = (screen: Screen, theme: Theme): string => `${screen.name}-${theme}.png`;

export const demoUrl = (base: string, screen: Screen): string => `${base.replace(/\/$/, "")}/?scenario=${screen.scenario}`;

export const selectScreens = (names: string[]): Screen[] => {
  if (names.length === 0) return screens;
  const unknown = names.filter((n) => !screens.some((s) => s.name === n));
  if (unknown.length > 0) throw new Error(`Unknown screen: ${unknown.join(", ")}`);
  return screens.filter((s) => names.includes(s.name));
};

export type Options = { out: string; base: string; names: string[]; themes: Theme[] };

export function parseArgs(argv: string[], env: Record<string, string | undefined>): Options {
  const opts: Options = { out: "../../../nipa-web/public/screenshots", base: env.NIPA_DEMO_URL ?? "http://127.0.0.1:9245", names: [], themes: THEMES };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--out") opts.out = argv[++i] ?? opts.out;
    else if (arg === "--base") opts.base = argv[++i] ?? opts.base;
    else if (arg === "--screen") opts.names.push(argv[++i] ?? "");
    else if (arg === "--theme") {
      const t = argv[++i];
      if (t !== "dark" && t !== "light") throw new Error(`Unknown theme: ${t}`);
      opts.themes = [t];
    } else throw new Error(`Unknown argument: ${arg}`);
  }
  return opts;
}

import { demoUrl, fileName, parseArgs, screens, selectScreens } from "./screens";

describe("screens", () => {
  it("has unique names", () => {
    expect(new Set(screens.map((s) => s.name)).size).toBe(screens.length);
  });

  it("builds file names and urls", () => {
    expect(fileName(screens[0], "dark")).toBe("changes-image-dark.png");
    const conflicts = screens.find((s) => s.name === "conflicts")!;
    expect(demoUrl("http://x:1/", conflicts)).toBe("http://x:1/?scenario=conflicts");
  });

  it("selects screens", () => {
    expect(selectScreens([])).toBe(screens);
    expect(selectScreens(["locks"]).map((s) => s.name)).toEqual(["locks"]);
    expect(() => selectScreens(["nope"])).toThrow("Unknown screen: nope");
  });

  it("parses arguments", () => {
    expect(parseArgs([], {}).base).toBe("http://127.0.0.1:9245");
    expect(parseArgs([], { NIPA_DEMO_URL: "http://a" }).base).toBe("http://a");
    const o = parseArgs(["--out", "o", "--base", "b", "--screen", "locks", "--theme", "light"], {});
    expect(o).toMatchObject({ out: "o", base: "b", names: ["locks"], themes: ["light"] });
    expect(() => parseArgs(["--theme", "x"], {})).toThrow("Unknown theme");
    expect(() => parseArgs(["--wat"], {})).toThrow("Unknown argument");
  });
});

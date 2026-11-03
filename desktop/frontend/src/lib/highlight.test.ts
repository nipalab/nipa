import { describe, expect, it } from "vitest";
import { escapeHtml, highlightLine, languageFor } from "./highlight";

describe("highlight", () => {
  it("maps game and code extensions to languages", () => {
    expect(languageFor("src/Player.cs")).toBe("csharp");
    expect(languageFor("Assets/Hero.prefab")).toBe("yaml");
    expect(languageFor("shaders/Lit.HLSL")).toBe("cpp");
    expect(languageFor("scenes/main.tscn")).toBe("ini");
    expect(languageFor("scripts/player.gd")).toBe("python");
    expect(languageFor("Makefile")).toBeUndefined();
    expect(languageFor("art/hero.psd")).toBeUndefined();
  });

  it("escapes plain text", () => {
    expect(escapeHtml(`<a href="x">&</a>`)).toBe("&lt;a href=&quot;x&quot;&gt;&amp;&lt;/a&gt;");
    expect(highlightLine("<b>", undefined)).toBe("&lt;b&gt;");
  });

  it("highlights code without leaking markup", () => {
    const html = highlightLine('func main() { s := "<x>" }', "go");
    expect(html).toContain('<span class="hljs-keyword">func</span>');
    expect(html).toContain("&lt;x&gt;");
  });
});

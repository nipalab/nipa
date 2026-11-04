import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const html = readFileSync(resolve(__dirname, "../index.html"), "utf8");
const doc = new DOMParser().parseFromString(html, "text/html");
const policy = doc.querySelector('meta[http-equiv="Content-Security-Policy"]')?.getAttribute("content") ?? "";
const directives = new Map(
  policy
    .split(";")
    .map((d) => d.trim().split(/\s+/))
    .filter((d) => d[0])
    .map(([name, ...sources]) => [name, sources]),
);

describe("webview content security policy", () => {
  it("keeps model loaders off the network", () => {
    for (const name of ["img-src", "media-src", "font-src", "connect-src"]) {
      const sources = directives.get(name);
      expect(sources, name).toBeDefined();
      for (const source of sources!) {
        expect(source, `${name} ${source}`).toMatch(/^('self'|data:|blob:|ws:\/\/(127\.0\.0\.1|localhost):\*)$/);
      }
    }
  });
});

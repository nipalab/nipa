import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import csharp from "highlight.js/lib/languages/csharp";
import css from "highlight.js/lib/languages/css";
import glsl from "highlight.js/lib/languages/glsl";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import lua from "highlight.js/lib/languages/lua";
import markdown from "highlight.js/lib/languages/markdown";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const languages = { bash, c, cpp, csharp, css, glsl, go, ini, java, javascript, json, lua, markdown, python, rust, typescript, xml, yaml };
for (const [name, lang] of Object.entries(languages)) hljs.registerLanguage(name, lang);

const byExtension: Record<string, string> = {
  sh: "bash", bash: "bash",
  c: "c", h: "c",
  cc: "cpp", cpp: "cpp", cxx: "cpp", hpp: "cpp", hh: "cpp", hlsl: "cpp", usf: "cpp", ush: "cpp", shader: "cpp", cginc: "cpp",
  cs: "csharp",
  css: "css",
  glsl: "glsl", vert: "glsl", frag: "glsl", comp: "glsl", geom: "glsl",
  go: "go",
  ini: "ini", cfg: "ini", toml: "ini", import: "ini", godot: "ini", tres: "ini", tscn: "ini",
  java: "java",
  js: "javascript", jsx: "javascript", mjs: "javascript", cjs: "javascript",
  json: "json", ldtk: "json", gltf: "json",
  lua: "lua",
  md: "markdown", markdown: "markdown",
  py: "python", gd: "python",
  rs: "rust",
  ts: "typescript", tsx: "typescript",
  xml: "xml", html: "xml", svg: "xml", tmx: "xml", uxml: "xml", plist: "xml", csproj: "xml",
  yaml: "yaml", yml: "yaml", unity: "yaml", prefab: "yaml", mat: "yaml", asset: "yaml", anim: "yaml", controller: "yaml", meta: "yaml",
};

export function languageFor(path: string): string | undefined {
  const name = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  const dot = name.lastIndexOf(".");
  return dot < 0 ? undefined : byExtension[name.slice(dot + 1)];
}

export function escapeHtml(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

// highlightLine returns safe HTML for one line. Lines are highlighted on their
// own, so constructs spanning lines (block comments) may be coloured only
// partly; that is the price of virtualized rows.
export function highlightLine(text: string, language: string | undefined): string {
  if (!language) return escapeHtml(text);
  return hljs.highlight(text, { language, ignoreIllegals: true }).value;
}

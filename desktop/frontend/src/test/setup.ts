import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

afterEach(() => {
  cleanup();
  localStorage.clear();
  document.documentElement.classList.remove("dark");
});

// jsdom lays nothing out, so virtualized lists would render no rows. Give
// scroll containers a viewport and measured rows a line height.
const viewport = (el: HTMLElement) => (el.hasAttribute("data-index") ? 20 : 800);
for (const prop of ["clientHeight", "offsetHeight"] as const) {
  Object.defineProperty(HTMLElement.prototype, prop, { configurable: true, get(this: HTMLElement) { return viewport(this); } });
}
for (const prop of ["clientWidth", "offsetWidth"] as const) {
  Object.defineProperty(HTMLElement.prototype, prop, { configurable: true, get: () => 800 });
}

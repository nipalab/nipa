import { chromium } from "playwright-core";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { VIEWPORT, demoUrl, fileName, parseArgs, selectScreens, type Step } from "../src/demo/screens.ts";

const opts = parseArgs(process.argv.slice(2), process.env);
const out = resolve(process.cwd(), opts.out);
await mkdir(out, { recursive: true });

const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_BIN ?? "/snap/bin/chromium", args: ["--no-sandbox", "--use-gl=swiftshader", "--enable-unsafe-swiftshader"] });

async function run(page: import("playwright-core").Page, step: Step) {
  if ("tab" in step) await page.getByRole("tab", { name: step.tab }).click();
  else if ("button" in step) await page.getByRole("button", { name: step.button }).first().click();
  else if ("text" in step) await page.getByText(step.text).first().click();
  else await page.waitForTimeout(step.wait);
}

for (const theme of opts.themes) {
  for (const screen of selectScreens(opts.names)) {
    const context = await browser.newContext({ viewport: VIEWPORT, deviceScaleFactor: 2 });
    await context.addInitScript((t) => {
      localStorage.setItem("nipa.theme", t);
      localStorage.setItem("nipa.selectedRepo", "/home/alex/studio/starfall");
    }, theme);
    const page = await context.newPage();
    await page.goto(demoUrl(opts.base, screen));
    await page.getByRole("navigation", { name: "Repositories" }).waitFor();
    await page.waitForTimeout(500);
    for (const step of screen.steps) await run(page, step);
    await page.waitForTimeout(screen.settle);
    const file = `${out}/${fileName(screen, theme)}`;
    await page.screenshot({ path: file });
    console.log(file);
    await context.close();
  }
}
await browser.close();

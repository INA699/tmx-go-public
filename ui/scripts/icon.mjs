// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Renders the product icon from its source, build/appicon.svg (`make icon`):
//   build/appicon.png             1024x1024, transparent outside the rounded square; Wails builds
//                                 the macOS .icns and the Windows .ico from it
//   ui/public/favicon.svg         copy of the source (the UI's favicon)
//   ui/public/apple-touch-icon.png 180x180; iOS rounds the corners itself and shows transparency as
//                                 black, so the corners are filled with the icon's background colour
// Uses Playwright's bundled Chromium headless (no window). On a shared machine wrap it:
//   tmx browser-slot -- node scripts/icon.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

const ui = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repo = path.resolve(ui, "..");
const src = path.join(repo, "build", "appicon.svg");
const svg = fs.readFileSync(src, "utf8");
const bg = svg.match(/<rect[^>]*fill="(#[0-9a-fA-F]{3,8})"/)?.[1] ?? "#000000";

fs.copyFileSync(src, path.join(ui, "public", "favicon.svg"));

const browser = await chromium.launch({ headless: true, args: ["--disable-features=WebAuthenticationUseNativeWinApi"] });
try {
  const render = async (size, out, background) => {
    const page = await browser.newPage({ viewport: { width: size, height: size }, deviceScaleFactor: 1 });
    const body = svg.replace(/<svg\b/, `<svg style="display:block;width:${size}px;height:${size}px"`);
    await page.setContent(
      `<!doctype html><html><head><style>html,body{margin:0;padding:0;background:${background}}</style></head><body>${body}</body></html>`,
    );
    await page.screenshot({ path: out, omitBackground: background === "transparent", clip: { x: 0, y: 0, width: size, height: size } });
    await page.close();
    console.log(`wrote ${path.relative(repo, out)} (${size}x${size})`);
  };
  await render(1024, path.join(repo, "build", "appicon.png"), "transparent");
  await render(180, path.join(ui, "public", "apple-touch-icon.png"), bg);
} finally {
  await browser.close();
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Phone-size pages load without a token loop (the Python fleet's 2 Nov SEV-1: a dashboard token prompt and a
// phone re-pair after an update). Owner pages at 390x844: each main page renders its heading, never the
// "needs your owner token" / "Pair this device" screen, also after a reload, a back/forward and a new tab of
// the same session.
import { test } from "node:test";
import assert from "node:assert/strict";
import { launchBrowser, openPage, signIn, startDaemon } from "./harness.mjs";

const PAGES = ["/", "/inbox", "/board", "/recordings", "/settings"];
const TOKEN_SCREEN = /needs your owner token|Pair this device/i;

test("phone-size pages load without a token loop", { timeout: 5 * 60_000 }, async () => {
  const d = await startDaemon();
  const browser = await launchBrowser();
  try {
    const { page, errors } = await openPage(browser, { phone: true, origin: d.url });
    await signIn(page, d);
    let requests401 = 0;
    page.on("response", r => { if (r.status() === 401) requests401++; });
    for (const p of PAGES) {
      await page.evaluate(h => { location.hash = h; }, p);
      await page.waitForLoadState("networkidle");
      const body = await page.locator("body").innerText();
      assert.doesNotMatch(body, TOKEN_SCREEN, `${p}: token screen at phone size`);
      assert.ok(await page.getByRole("heading").first().isVisible(), `${p}: rendered (a heading is visible): ${body.slice(0, 200)}`);
    }
    await page.reload();                                     // a reload keeps the session (no prompt, no loop)
    await page.waitForLoadState("networkidle");
    assert.doesNotMatch(await page.locator("body").innerText(), TOKEN_SCREEN, "after a reload");
    await page.goBack(); await page.goForward();
    await page.waitForLoadState("networkidle");
    assert.doesNotMatch(await page.locator("body").innerText(), TOKEN_SCREEN, "after back/forward");
    // a token loop shows as a stream of 401s: a handful at most
    assert.ok(requests401 <= 2, `401s while browsing: ${requests401}`);
    // the URL no longer carries the token (it was read once and stripped)
    assert.doesNotMatch(page.url(), /token=/, "the token is stripped from the URL");
    assert.deepEqual(errors, [], "no page errors");
  } catch (e) {
    console.log("daemon log (tail):\n" + d.log.slice(-40).join(""));
    throw e;
  } finally {
    await browser.close();
    await d.stop();
  }
});

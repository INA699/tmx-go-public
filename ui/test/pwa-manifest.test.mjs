import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');

test('index.html links the web app manifest and PWA meta tags', () => {
  assert.match(html, /<link rel="manifest" href="\/manifest\.webmanifest" \/>/);
  assert.match(html, /<meta name="theme-color" content="#202a25" \/>/);
  assert.match(html, /<meta name="mobile-web-app-capable" content="yes" \/>/);
  assert.match(html, /<meta name="apple-mobile-web-app-capable" content="yes" \/>/);
  assert.match(html, /<meta name="apple-mobile-web-app-status-bar-style" content="[^"]+" \/>/);
  assert.match(html, /<link rel="apple-touch-icon" href="\/apple-touch-icon\.png" \/>/);
});

test('manifest.webmanifest declares name, standalone display, and a maskable icon', () => {
  const manifestPath = new URL('../public/manifest.webmanifest', import.meta.url);
  assert.ok(existsSync(manifestPath), 'public/manifest.webmanifest is missing');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  assert.equal(manifest.name, 'Tiamatrix');
  assert.equal(manifest.short_name, 'Tiamatrix');
  assert.equal(manifest.start_url, '/');
  assert.equal(manifest.scope, '/');
  assert.equal(manifest.display, 'standalone');
  assert.ok(manifest.theme_color);
  assert.ok(manifest.background_color);
  assert.ok(Array.isArray(manifest.icons) && manifest.icons.length >= 2);
  assert.ok(manifest.icons.some(i => i.purpose === 'maskable'), 'manifest is missing a maskable icon');
  for (const icon of manifest.icons) {
    const iconPath = new URL('../public' + icon.src, import.meta.url);
    assert.ok(existsSync(iconPath), `icon file missing: ${icon.src}`);
  }
});

// One mark everywhere: the favicon is the app icon's source, and no other logo is left behind.
test('the favicon is the app icon source', () => {
  const favicon = readFileSync(new URL('../public/favicon.svg', import.meta.url), 'utf8');
  const appicon = readFileSync(new URL('../../build/appicon.svg', import.meta.url), 'utf8');
  assert.equal(favicon, appicon);
  assert.ok(!existsSync(new URL('../public/brand/header-mark.svg', import.meta.url)), 'the Pocket dragon header mark is still there');
});

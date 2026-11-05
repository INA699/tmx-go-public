import test from "node:test";
import assert from "node:assert/strict";
import { SENSITIVE_LABEL, sensitiveDefault, isSensitive, sensitivityOf, setSensitiveDefault } from "../src/lib/sensitive.ts";

function memory() {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)), removeItem: (k) => m.delete(k) };
}

test("sensitive: the Options checkbox is kept for the next recordings and sent as sensitivity", () => {
  const s = memory();
  assert.equal(sensitiveDefault(s), false, "off by default: any model may write the report");
  setSensitiveDefault(true, s);
  assert.equal(sensitiveDefault(s), true);
  assert.equal(sensitivityOf(sensitiveDefault(s)), "clinic", "the API value is unchanged");
  setSensitiveDefault(false, s);
  assert.equal(sensitiveDefault(s), false);
  assert.equal(sensitivityOf(false), "");
  assert.equal(sensitiveDefault(null), false, "no storage: off");
  const broken = { getItem: () => { throw new Error("denied"); }, setItem: () => { throw new Error("denied"); }, removeItem: () => {} };
  assert.doesNotThrow(() => setSensitiveDefault(true, broken));
  assert.equal(sensitiveDefault(broken), false);
});

test("sensitive: a choice saved before the rename still applies", () => {
  const s = memory();
  s.setItem("tmx.rec.clinic", "1");
  assert.equal(sensitiveDefault(s), true);
});

test("sensitive: a recording is sensitive only when tagged so, in plain words", () => {
  assert.equal(isSensitive({ sensitivity: "clinic" }), true);
  assert.equal(isSensitive({ sensitivity: "" }), false);
  assert.equal(isSensitive({}), false);
  assert.equal(isSensitive(null), false);
  assert.equal(SENSITIVE_LABEL, "Sensitive — keep on this machine");
});

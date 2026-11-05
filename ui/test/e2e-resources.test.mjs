import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { startResources } from "../e2e/resources.mjs";

const resources = new URL("../e2e/resources.mjs", import.meta.url).href;

// A separate Node test process must report the error and exit by itself. A leaked
// listening server (the original missing-TLS failure) instead hits this timeout.
for (const stage of ["startup", "browser-launch", "upgraded-socket"]) {
  test(`E2E ${stage} failure exits nonzero without hanging`, () => {
    const fixture = `
      import { test } from 'node:test';
      import http from 'node:http';
      import net from 'node:net';
      import { once } from 'node:events';
      import { startResources, serverResource } from ${JSON.stringify(resources)};
      test('early failure', async t => {
        const d = await startResources(async own => {
          const server = http.createServer();
          const resource = serverResource(server);
          own(resource.close);
          await resource.listen();
          if (${JSON.stringify(stage)} === 'upgraded-socket') {
            const upgraded = once(server, 'upgrade');
            const socket = net.connect(server.address().port, '127.0.0.1');
            socket.resume();
            await once(socket, 'connect');
            socket.write('GET / HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: Upgrade\\r\\nUpgrade: websocket\\r\\n\\r\\n');
            await upgraded;
          }
          if (${JSON.stringify(stage)} !== 'browser-launch') throw new Error('intentional setup failure');
          return {};
        });
        t.after(() => d.stop());
        throw new Error('intentional browser launch failure');
      });
    `;
    const result = spawnSync(process.execPath, ["--input-type=module", "-e", fixture], {
      encoding: "utf8", timeout: 5000, windowsHide: true,
    });
    assert.ifError(result.error);
    assert.equal(result.signal, null);
    assert.equal(result.status, 1, result.stdout + result.stderr);
    assert.match(result.stdout + result.stderr, /intentional (setup|browser launch) failure/);
  });
}

test("startup preserves the original error and runs remaining cleanup after a close fails", async () => {
  const closed = [];
  await assert.rejects(startResources(async own => {
    own(() => closed.push("first"));
    own(() => { closed.push("second"); throw new Error("close failed"); });
    throw new Error("setup failed");
  }), error => error instanceof AggregateError && error.errors[0].message === "setup failed");
  assert.deepEqual(closed, ["second", "first"]);
});

test("successful resource cleanup is idempotent", async () => {
  let closed = 0;
  const d = await startResources(async own => { own(() => closed++); return { ready: true }; });
  assert.equal(d.ready, true);
  await Promise.all([d.stop(), d.stop()]);
  assert.equal(closed, 1);
});

// Browser-free resource ownership, also used by the startup regression tests.
export async function startResources(setup) {
  const cleanups = [];
  let stopping;
  const stop = () => stopping ??= (async () => {
    const errors = [];
    for (const close of cleanups.reverse()) {
      try { await close(); } catch (error) { errors.push(error); }
    }
    if (errors.length) throw new AggregateError(errors, "E2E cleanup failed");
  })();
  try {
    return { ...await setup(close => cleanups.push(close)), stop };
  } catch (error) {
    try { await stop(); } catch (cleanupError) {
      throw new AggregateError([error, cleanupError], "E2E startup and cleanup failed");
    }
    throw error;
  }
}

// server.close alone waits forever for upgraded WebSocket connections. Own every
// socket, including proxy upstreams, and destroy them before awaiting close.
export function serverResource(server) {
  const sockets = new Set();
  const track = socket => {
    sockets.add(socket);
    socket.once("close", () => sockets.delete(socket));
    return socket;
  };
  server.on("connection", track);
  return {
    track,
    listen: (port = 0) => new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(port, "127.0.0.1", () => {
        server.removeListener("error", reject);
        resolve();
      });
    }),
    close: () => new Promise((resolve, reject) => {
      server.close(error => error && error.code !== "ERR_SERVER_NOT_RUNNING" ? reject(error) : resolve());
      for (const socket of sockets) socket.destroy();
      server.closeAllConnections?.();
    }),
  };
}

export async function stopProcess(proc, exited) {
  if (proc.exitCode !== null || proc.signalCode !== null) return;
  proc.kill("SIGTERM");
  let timer;
  try {
    await Promise.race([exited, new Promise(resolve => { timer = setTimeout(resolve, 5000); })]);
    if (proc.exitCode === null && proc.signalCode === null) {
      proc.kill("SIGKILL");
      await exited;
    }
  } finally { clearTimeout(timer); }
}

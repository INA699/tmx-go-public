---
name: tmx
description: Inspect and use tmx (Tiamatrix), its local coding agents and their MCP tools. In the default trial profile external channels are disabled.
---

# tmx (Tiamatrix)

The default config profile is `trial`, the loopback API port is 8787 and the
state directory is `tmx-go` (see below). `TMXG_HOME` explicitly overrides the state directory.
The executable and MCP server are `tmx`; Python tmx remains separate.

P2P, DHT, mDNS, NAT port mapping, WhatsApp, Companion and automatic updates are
forced off when loading trial config, even if an older config enabled them.
There are no background update checks. Do not enable external channels or
replace the production control plane as part of a trial.

Agents use their `mcp__tmx__*` tools. Owner CLI commands act with the owner's
credential and require the owner's request. Never print or copy credentials.
Discover and adopt refuse outside processes without scanning or signalling them.
Claude loads only the generated Go settings and hooks. Codex receives
the `tmx` MCP entry on its command line.

For an already qualified trial, useful owner commands are:

```sh
tmx version
tmx status
tmx ls
tmx approvals
tmx help
```

`make headless` produces `bin/tmx` (with `.exe` on Windows); `make dist` produces
`dist/tmx_<version>_<os>_<arch>`. Installation and lifecycle execution require
the disposable-VM qualification described in `docs/fleet/TRIAL.md`: egress capture, ACL
comparison and proof that Python's agents, hooks and ports remain unaffected.

The state directory is `%LOCALAPPDATA%/tmx-go` on Windows,
`~/Library/Application Support/tmx-go` on macOS, and
`~/.local/state/tmx-go` on Linux; a machine that only has a `tmxg-trial` folder
there (from builds named tmxg) keeps using that one. Read bounded tails of its logs when
troubleshooting. Never expose the loopback API publicly.

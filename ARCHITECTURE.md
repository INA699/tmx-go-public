# Architecture

Code comments and help texts refer to this page, sometimes with a section name in quotes. The
detailed design document is kept privately for now; this snapshot doesn't include it. The code is
the reference: start at `main.go` (one binary for the daemon, the CLI, the MCP server and the
desktop app), `internal/api` (HTTP API), `internal/fleet` (agents and messaging), `internal/mcp`
(the MCP tools), `internal/p2p` (the peer-to-peer network) and `ui/` (the web app).

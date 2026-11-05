package config

import "regexp"

// hostNamePattern mirrors the agent slug pattern (DESIGN.md "Agent"): safe to use as a secret
// filename component and as a UI label.
var hostNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Host is another tmx daemon this one may read for the fleet-wide Overview (WP4). True
// cross-machine aggregation is DESIGN.md plan phase 10 ("multi-machine"), not built yet, and
// P2P stays off (issue #90); until then this only merges daemons already reachable at
// 127.0.0.1 on this machine (e.g. separate workspaces/profiles on different ports), so no new
// network exposure is introduced. Its bearer token is never stored here: see
// Paths.HostTokenFile.
type Host struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

// ValidHostName reports whether name is safe to use as a secret filename component.
func ValidHostName(name string) bool { return hostNamePattern.MatchString(name) }

// The port rule (issue #83; docs/trial-port.md), for every profile:
//   - PortPythonV2 (8777): the Python v2 REST API (agents_v2/tmx_*.py). Never listened on and
//     never sent a bearer token to as a Host: Python peers are reached through their own
//     authenticated route (peers.go), not as a local Host.
//   - 43911 and 4001: a v0.1.x install's secondary API and its libp2p port.
//
// DefaultPort (8787) is this daemon's own port; a configured Host may be another local daemon on
// it (one with a different state folder and port in its own config.json).
const PortPythonV2 = 8777

// reservedPorts may be neither listened on nor used as a Host port. Unexported: nothing outside
// this package may reach in and weaken it.
var reservedPorts = map[int]bool{PortPythonV2: true, 43911: true, 4001: true}

// ValidListenPort reports whether this daemon may listen on port: in range and not one of
// reservedPorts.
func ValidListenPort(port int) bool {
	return port >= 1 && port <= 65535 && !reservedPorts[port]
}

// ValidHostPort reports whether port is safe for this daemon to send its bearer-token request
// to: above the reserved/well-known range (<1024, e.g. ssh, http, https - never send a bearer
// token there) and not one of reservedPorts. This is defense in depth, not an identity check:
// fetchHost still authenticates with that host's own token and only trusts a 200 JSON answer.
// Checking the request wouldn't land back on this daemon's own port is the caller's job (it
// alone knows that port).
func ValidHostPort(port int) bool {
	return port >= 1024 && port <= 65535 && !reservedPorts[port]
}

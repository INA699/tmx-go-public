package harness

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// The server is "tmx", as the agents' prompts and allowedTools (mcp__tmx) expect.
func TestMCPServerName(t *testing.T) {
	args := strings.Join(CodexMCPArgs(&runner.MCPServer{Command: "tmx", Args: []string{"mcp"}}), " ")
	if !strings.Contains(args, "mcp_servers.tmx.command=") || strings.Contains(args, "tmxg") || strings.Contains(args, "enabled=false") {
		t.Fatalf("args: %s", args)
	}
}

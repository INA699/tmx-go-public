package model

import "strings"

// MCPServer is the name of tmx's MCP server (`tmx mcp`); agents see its tools as
// mcp__tmx__<tool>.
const MCPServer = "tmx"

// legacyMCPServer is the name builds called "tmxg" gave the same server. Their transcripts
// stay in tmx.db with that prefix, so readers of stored tool names accept both (TmxTool).
const legacyMCPServer = "tmxg"

// TmxTool returns the tool's own name (send, report, ...) when tool is one of tmx's MCP tools
// (mcp__tmx__send, or the legacy mcp__tmxg__send), else "".
func TmxTool(tool string) string {
	for _, server := range []string{MCPServer, legacyMCPServer} {
		if name, ok := strings.CutPrefix(tool, "mcp__"+server+"__"); ok {
			return name
		}
	}
	return ""
}

// TmxToolNames returns the stored names tool may have: mcp__tmx__<tool> and the legacy
// mcp__tmxg__<tool>.
func TmxToolNames(tool string) []string {
	return []string{"mcp__" + MCPServer + "__" + tool, "mcp__" + legacyMCPServer + "__" + tool}
}

package model

import "testing"

func TestTmxTool(t *testing.T) {
	for in, want := range map[string]string{
		"mcp__tmx__send":          "send",
		"mcp__tmxg__report":       "report",
		"mcp__tmx__whatsapp_send": "whatsapp_send",
		"mcp__other__send":        "",
		"Bash":                    "",
		"mcp__tmxgx__send":        "",
	} {
		if got := TmxTool(in); got != want {
			t.Errorf("TmxTool(%q) = %q, want %q", in, got, want)
		}
	}
	if n := TmxToolNames("send"); len(n) != 2 || n[0] != "mcp__tmx__send" || n[1] != "mcp__tmxg__send" {
		t.Errorf("TmxToolNames = %v", n)
	}
}

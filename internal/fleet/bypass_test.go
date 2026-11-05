package fleet

import (
	"encoding/json"
	"os"
	"testing"
)

// Bypass mode is the owner's explicit opt-in: config.json "agent_bypass": true runs a Claude
// agent with tmx's guard hook in bypass mode (the hook alone decides what reaches the owner).
// Unset or false keeps Claude's own prompts.
func TestGuardedClaudeAgentsRunInBypassModeOnlyWhenOptedIn(t *testing.T) {
	old := launchGOOS
	launchGOOS = "linux"
	t.Cleanup(func() { launchGOOS = old })

	on, off := true, false
	for _, c := range []struct {
		name   string
		bypass *bool
	}{{"unset", nil}, {"false", &off}} {
		e := newEnv(t)
		e.bypass = c.bypass
		f := e.started()
		spec := e.chat.Last(mustAgent(t, f, "head").ID).Spec
		if spec.PermissionMode != "auto" || spec.SettingsPath == "" {
			t.Fatalf("agent_bypass %s: mode %q, settings %q", c.name, spec.PermissionMode, spec.SettingsPath)
		}
		b, _ := os.ReadFile(spec.SettingsPath)
		var cfg map[string]any
		if json.Unmarshal(b, &cfg) != nil || cfg["skipDangerousModePermissionPrompt"] != nil || cfg["hooks"] == nil {
			t.Fatalf("agent_bypass %s: settings %s", c.name, b)
		}
	}

	e := newEnv(t)
	e.bypass = &on
	f := e.started()
	f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"})
	for _, name := range []string{"head", "alpha-lead"} {
		spec := e.chat.Last(mustAgent(t, f, name).ID).Spec
		if spec.PermissionMode != "bypassPermissions" || spec.SettingsPath == "" {
			t.Fatalf("%s: mode %q, settings %q", name, spec.PermissionMode, spec.SettingsPath)
		}
		b, err := os.ReadFile(spec.SettingsPath)
		if err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if json.Unmarshal(b, &cfg) != nil || cfg["skipDangerousModePermissionPrompt"] != true || cfg["hooks"] == nil {
			t.Fatalf("%s settings: %s", name, b)
		}
	}
}

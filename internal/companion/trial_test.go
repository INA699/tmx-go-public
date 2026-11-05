package companion

import "testing"

func TestTrialCompanionRequiresIsolatedHome(t *testing.T) {
	t.Setenv(EnvAIOSHome, "")
	if h, err := AIOSHome(); err == nil {
		t.Fatalf("implicitly uses shared home %q", h)
	}
	t.Setenv(EnvAIOSHome, t.TempDir())
	if _, err := AIOSHome(); err != nil {
		t.Fatal(err)
	}
	if Name != "tmx" {
		t.Fatalf("Name = %q", Name)
	}
	for _, e := range []Entry{{ManagedBy: "tmxg"}, {Command: "/x/tmxg", Args: []string{"mcp", "--companion"}}, {Command: "/x/tmx.exe", Args: []string{"mcp", "--companion"}}} {
		if !isOurs(e) {
			t.Errorf("isOurs(%+v) = false", e)
		}
	}
	if isOurs(Entry{Command: "/x/tmx-other", Args: []string{"mcp", "--companion"}}) {
		t.Error("a foreign binary is ours")
	}
}

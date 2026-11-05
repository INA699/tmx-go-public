package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTrialProfileDefaults(t *testing.T) {
	var c Config
	c.fillDefaults()
	b, _ := json.Marshal(c)
	var fields map[string]any
	_ = json.Unmarshal(b, &fields)
	if fields["profile"] != "trial" {
		t.Errorf("profile = %v", fields["profile"])
	}
	if c.P2P.On() || c.P2P.DHTOn() || c.P2P.MDNSOn() || c.P2P.NATPortMapOn() || c.WhatsApp.On() || c.Companion.On() || *c.Update.Resolved().Auto {
		t.Error("external services enabled by default")
	}
	if c.Port != 8787 || c.URL() != "http://127.0.0.1:8787" {
		t.Errorf("port = %d", c.Port)
	}
}

func TestTrialDisablesLegacyExternalServices(t *testing.T) {
	var c Config
	err := json.Unmarshal([]byte(`{"p2p":{"enabled":true,"dht":true,"mdns":true,"nat_port_map":true},"whatsapp":{"enabled":true},"companion":{"enabled":true},"update":{"auto":true}}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	c.fillDefaults()
	if c.P2P.On() || c.P2P.DHTOn() || c.P2P.MDNSOn() || c.P2P.NATPortMapOn() || c.WhatsApp.On() || c.Companion.On() || *c.Update.Resolved().Auto {
		t.Fatal("legacy config escaped trial isolation")
	}
}

func TestDefaultHomeName(t *testing.T) {
	t.Setenv(EnvHome, "")
	h, err := defaultHome()
	if err != nil {
		t.Fatal(err)
	}
	// The real machine may hold either folder (see TestHomeInFallsBackToLegacy).
	if b := filepath.Base(h); b != HomeName && b != LegacyHomeName {
		t.Fatalf("home = %q", h)
	}
}

func TestHomeInFallsBackToLegacy(t *testing.T) {
	mk := func(t *testing.T, base string, names ...string) {
		for _, n := range names {
			if err := os.MkdirAll(filepath.Join(base, n), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	cases := []struct {
		name  string
		exist []string
		want  string
	}{
		{"fresh machine", nil, HomeName},
		{"tmx-go only", []string{HomeName}, HomeName},
		{"tmxg build only", []string{LegacyHomeName}, LegacyHomeName},
		{"both: tmx-go wins", []string{HomeName, LegacyHomeName}, HomeName},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			mk(t, base, c.exist...)
			if got := homeIn(base); got != filepath.Join(base, c.want) {
				t.Fatalf("homeIn = %q, want %q", got, filepath.Join(base, c.want))
			}
		})
	}
	t.Run("a legacy file is not a home", func(t *testing.T) {
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, LegacyHomeName), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := homeIn(base); got != filepath.Join(base, HomeName) {
			t.Fatalf("homeIn = %q", got)
		}
	})
}

func TestDefaultConfigPrivate(t *testing.T) {
	p := &P2PConfig{}
	if p.On() || p.DHTOn() || p.MDNSOn() || p.NATPortMapOn() {
		t.Fatal("empty P2P config is public")
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestProductionProfileKeepsP2PAndUpdatesOff(t *testing.T) {
	var c Config
	err := json.Unmarshal([]byte(`{"profile":"production","p2p":{"enabled":true,"dht":true,"mdns":true,"nat_port_map":true},"whatsapp":{"enabled":true},"companion":{"enabled":true},"update":{"auto":true}}`), &c)
	if err != nil {
		t.Fatal(err)
	}
	c.fillDefaults()
	if !c.Production() {
		t.Fatal("profile lost")
	}
	if c.P2P.On() || c.P2P.DHTOn() || c.P2P.MDNSOn() || c.P2P.NATPortMapOn() || *c.Update.Resolved().Auto {
		t.Fatal("production v1 enabled peer-to-peer or automatic updates")
	}
	if !c.WhatsApp.On() || !c.Companion.On() {
		t.Fatal("production ignored the owner's WhatsApp/Companion switches")
	}
}

func TestProductionProfileDefaultsOff(t *testing.T) {
	c := Config{Profile: ProfileProduction}
	c.fillDefaults()
	if c.WhatsApp.On() || c.Companion.On() || c.P2P.On() || c.Port != DefaultPort {
		t.Fatalf("production defaults: %+v", c)
	}
}

func TestLoadProfiles(t *testing.T) {
	for _, tc := range []struct {
		profile string
		ok      bool
	}{{"", true}, {"trial", true}, {"production", true}, {"Production", false}, {"prod", false}, {"dev", false}} {
		t.Run(tc.profile, func(t *testing.T) {
			p := testPaths(t)
			t.Setenv(EnvPort, "")
			b, _ := json.Marshal(map[string]any{"profile": tc.profile})
			if err := os.WriteFile(p.ConfigFile(), b, 0600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(p)
			if tc.ok != (err == nil) {
				t.Fatalf("profile %q: err=%v", tc.profile, err)
			}
			if err != nil && !strings.Contains(err.Error(), "production") {
				t.Fatalf("error does not name the valid profiles: %v", err)
			}
			if tc.ok && c.Profile != map[string]string{"": "trial"}[tc.profile]+tc.profile {
				t.Fatalf("profile = %q", c.Profile)
			}
		})
	}
}

// The port rule is the same in every profile: 8777 (Python tmx) and 8787 (tmx-go v0.1) are
// never listened on; 8787 may still be a Host, 8777 never.
func TestPortRuleExplicit(t *testing.T) {
	for _, profile := range []string{ProfileTrial, ProfileProduction} {
		for _, port := range []int{PortPythonV2, PortLegacyHTTP, 43911, 4001} {
			p := testPaths(t)
			t.Setenv(EnvPort, "")
			b, _ := json.Marshal(map[string]any{"profile": profile, "port": port})
			if err := os.WriteFile(p.ConfigFile(), b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Errorf("%s accepted listen port %d", profile, port)
			}
		}
	}
	if ValidListenPort(PortPythonV2) || ValidListenPort(PortLegacyHTTP) || !ValidListenPort(DefaultPort) {
		t.Fatal("ValidListenPort")
	}
	if ValidHostPort(PortPythonV2) || !ValidHostPort(PortLegacyHTTP) {
		t.Fatal("ValidHostPort")
	}
}

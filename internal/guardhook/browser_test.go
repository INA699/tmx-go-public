// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package guardhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/guard"
)

// Uses only an in-memory HTTP fixture, never apitest.New or daemon state/ACLs.
func TestBrowserHostPermission(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		allowed    bool
	}{
		{"default", `{}`, 200, false},
		{"allowed", `{"allow_browser_automation":true}`, 200, true},
		{"denied", `{"allow_browser_automation":false}`, 200, false},
		{"badJSON", `invalid`, 200, false},
		{"wrongType", `{"allow_browser_automation":"true"}`, 200, false},
		{"unavailable", `{}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/guard/policy" {
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				w.WriteHeader(200)
			}))
			defer srv.Close()
			o := Options{Client: client.New(srv.URL, "fixture"), Context: guard.Context{OS: "windows", AllowBrowserAutomation: true}}
			dec := run(t, o, input("mcp__browser__navigate", map[string]any{}))
			if tc.allowed {
				if dec != nil {
					t.Fatal("host permission not applied")
				}
			} else if dec == nil || dec.PermissionDecision != "deny" {
				t.Fatal("browser call not denied")
			}
		})
	}
	o := Options{Context: guard.Context{OS: "windows", AllowBrowserAutomation: true}}
	if dec := run(t, o, input("mcp__browser__navigate", nil)); dec == nil || dec.PermissionDecision != "deny" {
		t.Fatal("missing host must deny")
	}
}

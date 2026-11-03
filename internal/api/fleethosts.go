// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Fleet-wide Overview aggregation (WP4). True cross-machine aggregation is ARCHITECTURE.md plan
// phase 10 ("multi-machine"), not built yet, and P2P stays off (issue #90): this merges only
// daemons configured in config.json "hosts", read over the same 127.0.0.1 loopback every
// daemon already listens on (api/server.go Listen) - no new network exposure. Everything a
// host returns is that install's own data, never instructions or HTML to trust: every string
// below is clamped and control characters are stripped before it reaches the UI, which always
// renders it as plain text.

// hostTimeout bounds every call to another host so one unreachable daemon never stalls the
// Overview.
const hostTimeout = 2 * time.Second

// hostClient never follows a redirect: a 3xx from a configured host would otherwise resend our
// Authorization bearer token wherever that host points, off this machine's loopback if it
// chose to (security-review).
var hostClient = &http.Client{
	Timeout:       hostTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
}

func (s *Server) fleetHostRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fleet/hosts", s.ownerOnly(s.fleetHosts))
}

// HostSummary is one configured host's merged terminal/chat/resources/state, as far as it
// answered within hostTimeout.
type HostSummary struct {
	Name       string  `json:"name"`
	Port       int     `json:"port"`
	Reachable  bool    `json:"reachable"`
	Error      string  `json:"error,omitempty"`
	Machine    string  `json:"machine,omitempty"`
	Agents     int     `json:"agents"`
	Projects   int     `json:"projects"`
	Running    int     `json:"running"`
	Paused     int     `json:"paused"`
	NeedsYou   int     `json:"needs_you"`
	CPU        float64 `json:"cpu,omitempty"`
	MemFreeMB  int64   `json:"mem_free_mb,omitempty"`
	DiskFreeGB float64 `json:"disk_free_gb,omitempty"`
	HeadName   string  `json:"head_name,omitempty"`
	HeadState  string  `json:"head_state,omitempty"`
	HeadLine   string  `json:"head_line,omitempty"` // latest head activity, clamped plain text
}

func (s *Server) fleetHosts(w http.ResponseWriter, r *http.Request) {
	c, err := config.Load(s.f.Paths())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]HostSummary, len(c.Hosts))
	var wg sync.WaitGroup
	for i, h := range c.Hosts {
		wg.Add(1)
		go func(i int, h config.Host) {
			defer wg.Done()
			out[i] = s.fetchHost(r.Context(), h)
		}(i, h)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"hosts": out})
}

func (s *Server) fetchHost(ctx context.Context, h config.Host) HostSummary {
	out := HostSummary{Name: h.Name, Port: h.Port}
	if !config.ValidHostName(h.Name) {
		out.Error = "invalid host name"
		return out
	}
	if !config.ValidHostPort(h.Port) || h.Port == s.port {
		out.Error = "invalid or disallowed port"
		return out
	}
	tok, err := config.ReadSecret(s.f.Paths().HostTokenFile(h.Name))
	if err != nil || tok == "" {
		out.Error = "no token configured for this host"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	base := configuredHostBase(h.Port)

	var snap model.Snapshot
	if err := getHostJSON(ctx, base+"/api/snapshot", tok, &snap); err != nil {
		out.Error = clampText(err.Error(), 200)
		return out
	}
	out.Reachable = true
	out.Machine = clampText(snap.Machine, 80)
	out.Projects = len(snap.Projects)
	out.Agents = len(snap.Agents)
	for _, a := range snap.Agents {
		switch a.State {
		case model.StateWorking, model.StateWaiting, model.StateStarting:
			out.Running++
		case model.StatePaused:
			out.Paused++
		}
	}
	out.NeedsYou = len(snap.Approvals)
	if snap.Head != nil {
		out.HeadName = clampText(snap.Head.Name, 80)
		out.HeadState = string(snap.Head.State)
		var tl struct {
			Entries []model.TimelineEntry `json:"entries"`
		}
		tlURL := base + "/api/agents/" + url.PathEscape(snap.Head.ID) + "/timeline?limit=1"
		if err := getHostJSON(ctx, tlURL, tok, &tl); err == nil && len(tl.Entries) > 0 {
			out.HeadLine = clampText(tl.Entries[len(tl.Entries)-1].Text, 200)
		}
	}

	var res struct {
		Sample struct {
			Machine struct {
				CPU      float64 `json:"cpu"`
				MemFree  int64   `json:"mem_free"`
				DiskFree int64   `json:"disk_free"`
			} `json:"machine"`
		} `json:"sample"`
	}
	if err := getHostJSON(ctx, base+"/api/resources", tok, &res); err == nil {
		out.CPU = res.Sample.Machine.CPU
		out.MemFreeMB = res.Sample.Machine.MemFree / (1 << 20)
		out.DiskFreeGB = float64(res.Sample.Machine.DiskFree) / (1 << 30)
	}
	return out
}

func configuredHostBase(port int) string { return fmt.Sprintf("http://127.0.0.1:%d", port) }

func getHostJSON(ctx context.Context, rawURL, token string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hostClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// clampText makes a remote string safe to show as plain text: control characters (including
// newlines) are dropped and it is cut to n runes. The UI never renders this as HTML.
func clampText(s string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= n {
			b.WriteString("…")
			break
		}
		if r == '\n' || r == '\t' {
			r = ' '
		} else if r < 0x20 {
			continue
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
}

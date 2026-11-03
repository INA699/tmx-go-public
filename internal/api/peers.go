// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// The Python fleet interop (ARCHITECTURE.md "Python fleet interop"): the fleet's other machines,
// still on the Python tmx daemon, read this daemon's agents and hand over mail through the
// routes below, exactly as they do with one another. They have their own mux and their own
// authentication: a peer's pair token (X-TMX-Token + X-TMX-From), never the owner's bearer
// token, and nothing else in the API is reachable with it. The guard's network rule applies
// first: a peer arrives over the tailnet through tailscale serve (a trusted https host), or
// from this machine.

// peerPaths are the routes served to peers.
var peerPaths = map[string]bool{"/api/sessions": true, "/api/messages/incoming": true, "/api/peers/pair-token": true, "/api/gate/pin/record": true}

var wireIDRx = regexp.MustCompile(`^[0-9a-f]{12,32}$`)

type peerCtxKey struct{}

// peerCaller is an authenticated peer machine (always by its pair token).
type peerCaller struct {
	name string
}

func peerOf(r *http.Request) peerCaller {
	p, _ := r.Context().Value(peerCtxKey{}).(peerCaller)
	return p
}

func peerRoute(r *http.Request) bool { return peerPaths[r.URL.Path] }

func (s *Server) peerRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions", s.peerSessions)
	mux.HandleFunc("POST /api/messages/incoming", s.peerIncoming)
	mux.HandleFunc("POST /api/peers/pair-token", s.peerPairToken)
	mux.HandleFunc("GET /api/gate/pin/record", s.peerPinRecord) // ownerpin.go
	mux.HandleFunc("POST /api/gate/pin/record", s.peerPinAdopt) // ownerpin.go
	return mux
}

// servePeer authenticates a peer machine and serves its routes. A browser never gets here
// (no Origin), an agent or device relayed by a peer is refused (on-behalf "daemon" only).
func (s *Server) servePeer(w http.ResponseWriter, r *http.Request) {
	if s.peers == nil {
		writeError(w, http.StatusNotFound, "this daemon has no fleet peers (production profile, config.json \"peers\")")
		return
	}
	if len(r.Header.Values("Origin")) > 0 {
		writeError(w, http.StatusForbidden, "peers only")
		return
	}
	if ob := r.Header.Get(peers.HeaderOnBehalf); ob != "" && ob != "daemon" {
		writeError(w, http.StatusForbidden, "only another machine of this fleet may call this route")
		return
	}
	from := strings.ToLower(strings.TrimSpace(r.Header.Get(peers.HeaderFrom)))
	tok := strings.TrimSpace(r.Header.Get(peers.HeaderToken))
	if tok == "" {
		tok, _ = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		tok = strings.TrimSpace(tok)
	}
	if tok == "" || !s.peers.Known(from) {
		writeError(w, http.StatusUnauthorized, "missing or invalid peer token")
		return
	}
	// Pair tokens only: the fleet token was retired fleet-wide on 1 Nov 2026 (the Python daemons
	// refuse it too), so a machine joins through its own pair token (tmx peers import-python).
	owner, ok := s.peers.Tokens().Identify(tok)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing or invalid peer token")
		return
	}
	if owner != from {
		writeError(w, http.StatusUnauthorized, "this token belongs to another machine")
		return
	}
	c := peerCaller{name: from}
	s.peerMux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerCtxKey{}, c)))
}

// pythonState maps an agent state to the Python daemon's detector states where they differ.
func pythonState(st model.AgentState) string {
	switch st {
	case model.StateWaiting:
		return "dialog"
	case model.StateStopped, model.StateError:
		return "exited"
	}
	return string(st)
}

// buildCommit is the VCS revision this binary was built from ("" when unknown).
func buildCommit() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, kv := range bi.Settings {
			if kv.Key == "vcs.revision" {
				return kv.Value
			}
		}
	}
	return ""
}

// peerSessions is GET /api/sessions in the Python daemon's shape: this machine's agents.
func (s *Server) peerSessions(w http.ResponseWriter, r *http.Request) {
	snap, err := s.f.Snapshot()
	if err != nil {
		writeErr(w, err)
		return
	}
	host := s.peers.Machine()
	agents := snap.Agents
	if snap.Head != nil {
		agents = append([]model.Agent{*snap.Head}, agents...)
	}
	seen := map[string]bool{}
	rows := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		rows = append(rows, map[string]any{
			"name": a.Name, "host": host, "id": a.ID, "adapter": string(a.Harness), "state": pythonState(a.State),
			"title": a.Role, "role": a.Role, "queued_messages": a.Queued, "managed": true,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "commit": buildCommit(), "version": s.f.Version(),
		"sessions": rows, "supervisor": nil, "system": map[string]any{}})
}

// peerIncoming is POST /api/messages/incoming: a peer hands over mail for an agent here.
// The peer vouches only for its own agents (sender_host must be the peer); the body must match
// its sha256; the same ID again is the same message (idempotent).
func (s *Server) peerIncoming(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID         string  `json:"id"`
		Sender     string  `json:"sender"`
		SenderHost string  `json:"sender_host"`
		To         string  `json:"to"`
		Body       string  `json:"body"`
		ReplyTo    *string `json:"reply_to"`
		Urgent     bool    `json:"urgent"`
		SHA256     string  `json:"sha256"`
	}
	if !decodeMax(w, r, &b, fleet.MaxBodyBytes*2+4096) {
		return
	}
	p := peerOf(r)
	if b.Sender == "" || b.SenderHost == "" || b.To == "" || b.Body == "" {
		writeError(w, http.StatusBadRequest, "incoming message without sender, sender_host, to or body")
		return
	}
	if !wireIDRx.MatchString(b.ID) {
		writeError(w, http.StatusBadRequest, "id must be 12-32 lowercase hex characters")
		return
	}
	if b.SHA256 != "" {
		sum := sha256.Sum256([]byte(b.Body))
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(b.SHA256)), []byte(hex.EncodeToString(sum[:]))) != 1 {
			writeError(w, http.StatusConflict, "message damaged in transit: nothing was delivered, send it again")
			return
		}
	}
	if !strings.EqualFold(b.SenderHost, p.name) {
		writeError(w, http.StatusForbidden, "sender_host must be the machine that hands the message over")
		return
	}
	in := fleet.PeerInbound{Peer: p.name, ID: b.ID, Sender: strings.ToLower(b.Sender), SenderHost: p.name,
		To: b.To, Body: b.Body, Urgent: b.Urgent}
	if b.ReplyTo != nil {
		in.ReplyTo = *b.ReplyTo
	}
	m, _, err := s.f.DeliverPeer(in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": b.ID, "state": string(m.State)})
}

// peerPairToken is POST /api/peers/pair-token: a ROTATION of the pair token, sent over the
// current one by the peer whose name sorts first (the Python rule: the generator rotates). A
// first token never arrives here: the fleet token is retired, so a machine joins through
// tmx peers import-python. The answer is the new token's fingerprint, never the token.
func (s *Server) peerPairToken(w http.ResponseWriter, r *http.Request) {
	p := peerOf(r)
	var b struct {
		Token string `json:"token"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &b) {
		return
	}
	if !(p.name < s.peers.Machine()) {
		writeError(w, http.StatusForbidden, "this machine generates the pair token it shares with "+p.name+", not the other way round")
		return
	}
	if b.Role != "" && b.Role != "full" {
		writeError(w, http.StatusBadRequest, "role: full")
		return
	}
	fp, err := s.peers.Tokens().Store(p.name, b.Token, p.name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("peer token rotated", "peer", p.name, "fingerprint", fp)
	writeJSON(w, http.StatusOK, map[string]string{"fingerprint": fp})
}

// peerStatus is GET /api/peers (the owner): what this daemon last saw of each peer.
func (s *Server) peerStatus(w http.ResponseWriter, r *http.Request) {
	if s.peers == nil {
		writeJSON(w, http.StatusOK, map[string]any{"machine": s.f.Machine(), "peers": []peers.Status{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"machine": s.peers.Machine(), "peers": s.peers.Statuses()})
}

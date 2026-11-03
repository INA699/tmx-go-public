// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/peers"
)

// retiredFleetToken stands for the fleet token retired on 1 Nov 2026: never accepted.
const retiredFleetToken = "fleet-token-0123456789-0123456789-0123456789"

// pyPeer plays the Python daemon on "hosta" (it sorts before "hostb", so it hands us the
// pair token) and records the mail this daemon posts to it.
type pyPeer struct {
	mu   sync.Mutex
	mail []fleet.PeerOutbound
	pins []map[string]any // owner PIN records pushed to it
}

func (p *pyPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/sessions":
		_ = json.NewEncoder(w).Encode(map[string]any{"host": "hosta", "commit": "abc", "sessions": []map[string]any{{"name": "tmx-lead", "state": "idle"}}})
	case "/api/messages/incoming":
		var m fleet.PeerOutbound
		_ = json.NewDecoder(r.Body).Decode(&m)
		p.mu.Lock()
		p.mail = append(p.mail, m)
		p.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": m.ID, "state": "queued"})
	case "/api/gate/pin/record":
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		p.mu.Lock()
		p.pins = append(p.pins, b)
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]bool{"adopted": true})
	default:
		w.WriteHeader(404)
	}
}

func (p *pyPeer) got() []fleet.PeerOutbound {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]fleet.PeerOutbound(nil), p.mail...)
}

type peerSetup struct {
	d    *apitest.Daemon
	url  string // the daemon, with peer routes
	py   *pyPeer
	pair string // the hosta pair token, as tmx peers import-python brought it
}

func newPeerSetup(t *testing.T) *peerSetup {
	t.Helper()
	d := apitest.New(t)
	py := &pyPeer{}
	pySrv := httptest.NewTLSServer(py)
	t.Cleanup(pySrv.Close)
	paths := config.Paths{Home: d.Home}
	cfg := config.Config{Profile: config.ProfileProduction, Machine: "hostb",
		Peers: []config.Peer{{Name: "hosta", URL: pySrv.URL}, {Name: "hostc", URL: "https://hostc.example.ts.net"}}}
	pc := peers.New(cfg, paths, nil)
	pc.SetTransport(pySrv.Client().Transport)
	pair := peers.NewPairToken()
	if _, err := pc.Tokens().Store("hosta", pair, "python-import"); err != nil {
		t.Fatal(err)
	}
	return &peerSetup{d: d, url: d.WithPeers(t, pc), py: py, pair: pair}
}

// peerDo calls the daemon as a peer machine.
func peerDo(t *testing.T, base, method, path, from, token string, hdr map[string]string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	if token != "" {
		req.Header.Set(peers.HeaderToken, token)
	}
	if from != "" {
		req.Header.Set(peers.HeaderFrom, from)
	}
	req.Header.Set(peers.HeaderOnBehalf, "daemon")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func sum(body string) string {
	s := sha256.Sum256([]byte(body))
	return hex.EncodeToString(s[:])
}

func incoming(id, body, to string) map[string]any {
	return map[string]any{"id": id, "sender": "tmx-lead", "sender_host": "hosta", "to": to, "body": body,
		"reply_to": nil, "urgent": false, "sent": 1.0, "sha256": sum(body)}
}

func TestPeerAuthBoundaries(t *testing.T) {
	s := newPeerSetup(t)
	msg := incoming("0123456789ab", "hello from hosta", "head")
	cases := []struct {
		name, method, path, from, token string
		hdr                             map[string]string
		body                            any
		want                            int
	}{
		{"no token", "GET", "/api/sessions", "hosta", "", nil, nil, 401},
		{"unknown machine", "GET", "/api/sessions", "mallory", s.pair, nil, nil, 401},
		{"owner token is not a peer token", "GET", "/api/sessions", "hosta", s.d.OwnerToken, nil, nil, 401},
		{"a browser", "GET", "/api/sessions", "hosta", s.pair, map[string]string{"Origin": "https://evil.example"}, nil, 403},
		{"an agent relayed by a peer", "GET", "/api/sessions", "hosta", s.pair, map[string]string{peers.HeaderOnBehalf: "agent:x@hosta"}, nil, 403},
		// The fleet token is retired fleet-wide: refused everywhere, also for an unpaired peer.
		{"retired fleet token reads", "GET", "/api/sessions", "hosta", retiredFleetToken, nil, nil, 401},
		{"retired fleet token, unpaired peer", "GET", "/api/sessions", "hostc", retiredFleetToken, nil, nil, 401},
		{"retired fleet token mails", "POST", "/api/messages/incoming", "hosta", retiredFleetToken, nil, msg, 401},
		{"retired fleet token pairs", "POST", "/api/peers/pair-token", "hostc", retiredFleetToken, nil, map[string]string{"token": peers.NewPairToken()}, 401},
		{"the pair token reads", "GET", "/api/sessions", "hosta", s.pair, nil, nil, 200},
	}
	for _, tc := range cases {
		if got := peerDo(t, s.url, tc.method, tc.path, tc.from, tc.token, tc.hdr, tc.body, nil); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	// The pair token opens nothing else, and is only good for its own machine.
	if got := peerDo(t, s.url, "GET", "/api/sessions", "hostc", s.pair, nil, nil, nil); got != 401 {
		t.Errorf("pair token claimed by another machine: %d", got)
	}
	req, _ := http.NewRequest("GET", s.url+"/api/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+s.pair)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 401 {
		t.Errorf("pair token reached an owner route: %v %v", res.StatusCode, err)
	}
	// A remote Host without the trusted https proxy is refused before any peer check.
	req, _ = http.NewRequest("GET", s.url+"/api/sessions", nil)
	req.Host = "hostb.example.ts.net"
	req.Header.Set(peers.HeaderToken, s.pair)
	req.Header.Set(peers.HeaderFrom, "hosta")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 403 {
		t.Errorf("remote host: %v %v", res.StatusCode, err)
	}
}

func TestPeerSessionsAndHealth(t *testing.T) {
	s := newPeerSetup(t)
	var out struct {
		Host     string           `json:"host"`
		Commit   *string          `json:"commit"`
		Sessions []map[string]any `json:"sessions"`
	}
	if code := peerDo(t, s.url, "GET", "/api/sessions", "hosta", s.pair, nil, nil, &out); code != 200 || out.Host != "hostb" || out.Commit == nil {
		t.Fatalf("%d %+v", code, out)
	}
	found := false
	for _, row := range out.Sessions {
		if row["name"] == "head" && row["host"] == "hostb" {
			found = true
		}
	}
	if !found {
		t.Fatalf("head not listed: %+v", out.Sessions)
	}
	var health map[string]any
	if code := peerDo(t, s.url, "GET", "/api/health", "", "", nil, nil, &health); code != 200 || health["host"] != "hostb" {
		t.Fatalf("health %d %v", code, health)
	}
}

func TestPeerMailBothWays(t *testing.T) {
	s := newPeerSetup(t)
	owner := client{t: t, base: s.url, token: s.d.OwnerToken}

	// Spoofing another machine's agent, a damaged body and a bad ID are refused.
	spoof := incoming("0123456789ab", "hi", "head")
	spoof["sender_host"] = "hostc"
	damaged := incoming("0123456789ab", "hi", "head")
	damaged["sha256"] = sum("other")
	badID := incoming("NOT-HEX", "hi", "head")
	for name, tc := range map[string]struct {
		body map[string]any
		want int
	}{"spoof": {spoof, 403}, "damaged": {damaged, 409}, "bad id": {badID, 400}, "no agent": {incoming("0123456789ac", "hi", "nobody"), 404}} {
		if got := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", s.pair, nil, tc.body, nil); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}

	// Delivered with the wire ID echoed; the same ID again is the same message.
	in := incoming("0123456789ab", "please review PR 104", "head")
	var res map[string]string
	if code := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", s.pair, nil, in, &res); code != 201 || res["id"] != "0123456789ab" {
		t.Fatalf("incoming %d %v", code, res)
	}
	if code := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", s.pair, nil, in, &res); code != 201 || res["id"] != "0123456789ab" {
		t.Fatalf("retry %d %v", code, res)
	}
	changed := incoming("0123456789ab", "a different body", "head")
	if code := peerDo(t, s.url, "POST", "/api/messages/incoming", "hosta", s.pair, nil, changed, nil); code != 409 {
		t.Fatalf("same id, other body: %d", code)
	}
	var msgs []model.Message
	if code := owner.do("GET", "/api/messages?agent=head", nil, &msgs); code != 200 {
		t.Fatal(code)
	}
	var local model.Message
	n := 0
	for _, m := range msgs {
		if m.From == fleet.PeerParty("tmx-lead", "hosta") {
			local, n = m, n+1
		}
	}
	if n != 1 || local.Body == "" {
		t.Fatalf("peer mail stored %d times: %+v", n, msgs)
	}

	// The owner answers it: out to hosta with the original wire ID as reply_to.
	var sent model.Message
	if code := owner.do("POST", "/api/messages", map[string]string{"body": "done", "reply_to": local.ID}, &sent); code != 200 && code != 201 {
		t.Fatalf("reply %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.py.got()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got := s.py.got()
	if len(got) != 1 {
		t.Fatalf("hosta got %d messages", len(got))
	}
	m := got[0]
	if m.To != "tmx-lead" || m.SenderHost != "hostb" || m.Body != "done" || m.ReplyTo == nil || *m.ReplyTo != "0123456789ab" || m.SHA256 != sum("done") {
		t.Fatalf("wire message: %+v", m)
	}
	if strings.ContainsAny(m.ID, "ghijklmnopqrstuvwxyz") || len(m.ID) < 12 {
		t.Fatalf("wire id %q is not hex", m.ID)
	}

	// The owner's view of the peers.
	var st struct {
		Machine string         `json:"machine"`
		Peers   []peers.Status `json:"peers"`
	}
	if code := owner.do("GET", "/api/peers", nil, &st); code != 200 || st.Machine != "hostb" || len(st.Peers) != 2 || st.Peers[0].Name != "hosta" || !st.Peers[0].Paired || st.Peers[1].Paired {
		t.Fatalf("peers %d %+v", code, st)
	}
}

// A rotation comes over the current pair token from the machine that generated it, the one whose
// name sorts first (the Python rule): hosta may rotate, hostc may not (it sorts after hostb).
func TestPeerPairTokenRotation(t *testing.T) {
	s := newPeerSetup(t)
	next := peers.NewPairToken()
	var fp map[string]string
	if code := peerDo(t, s.url, "POST", "/api/peers/pair-token", "hosta", s.pair, nil, map[string]string{"token": next, "role": "full"}, &fp); code != 200 || fp["fingerprint"] != peers.Fingerprint(next) {
		t.Fatalf("rotation %d %v", code, fp)
	}
	for _, tok := range []string{next, s.pair} { // the old one still works during the overlap
		if code := peerDo(t, s.url, "GET", "/api/sessions", "hosta", tok, nil, nil, nil); code != 200 {
			t.Fatalf("after rotation: %d", code)
		}
	}
	vt := peers.NewPairToken()
	paths := config.Paths{Home: s.d.Home}
	if _, err := peers.NewTokens(paths.PeersFile()).Store("hostc", vt, "python-import"); err != nil {
		t.Fatal(err)
	}
	if code := peerDo(t, s.url, "POST", "/api/peers/pair-token", "hostc", vt, nil, map[string]string{"token": peers.NewPairToken()}, nil); code != 403 {
		t.Fatalf("hostc rotated the token hostb generates: %d", code)
	}
}

// The owner PIN between machines (Python's Ship 1 protocol): a peer pushes its newer record,
// reads ours; nobody else reaches the record; a PIN set here is pushed to the peers.
func TestPeerOwnerPINSync(t *testing.T) {
	s := newPeerSetup(t)
	var got struct {
		Record map[string]any `json:"record"`
	}
	if code := peerDo(t, s.url, "GET", "/api/gate/pin/record", "hosta", s.pair, nil, nil, &got); code != 200 || got.Record != nil {
		t.Fatalf("empty record: %d %v", code, got.Record)
	}
	// Vector 1 (confirmed against Python): PIN 123456.
	rec := map[string]any{"salt": "000102030405060708090a0b0c0d0e0f", "hash": "53e59f1f9f56c2317d99f7a1e76fd1d43677ccb19c2c125e054f9a81281e0aec", "set": float64(time.Now().Unix())}
	for _, tc := range []struct {
		name, from, token string
		hdr               map[string]string
		body              any
		want              int
	}{
		{"no token", "hosta", "", nil, map[string]any{"record": rec}, 401},
		{"owner token", "hosta", s.d.OwnerToken, nil, map[string]any{"record": rec}, 401},
		{"a browser", "hosta", s.pair, map[string]string{"Origin": "https://evil.example"}, map[string]any{"record": rec}, 403},
		{"an agent relayed by a peer", "hosta", s.pair, map[string]string{peers.HeaderOnBehalf: "agent:x@hosta"}, map[string]any{"record": rec}, 403},
		{"malformed", "hosta", s.pair, nil, map[string]any{"record": map[string]any{"salt": "00", "hash": "11", "set": 1}}, 400},
		{"the peer", "hosta", s.pair, nil, map[string]any{"record": rec}, 200},
	} {
		if code := peerDo(t, s.url, "POST", "/api/gate/pin/record", tc.from, tc.token, tc.hdr, tc.body, nil); code != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, code, tc.want)
		}
	}
	if code := peerDo(t, s.url, "GET", "/api/gate/pin/record", "hosta", s.pair, nil, nil, &got); code != 200 || got.Record["hash"] != rec["hash"] {
		t.Fatalf("adopted record: %v", got.Record)
	}
	// An older record never replaces a newer one.
	old := map[string]any{"salt": "ffeeddccbbaa99887766554433221100", "hash": "b2f4376c5db628fa24d3e8a0e087a2e838653543d225e400a76b78608611502c", "set": 1.0}
	var res map[string]bool
	peerDo(t, s.url, "POST", "/api/gate/pin/record", "hosta", s.pair, nil, map[string]any{"record": old}, &res)
	if res["adopted"] {
		t.Fatal("older record adopted")
	}
	// The owner at the machine sets a new PIN (the old one is not needed there): pushed to hosta.
	b, _ := json.Marshal(map[string]string{"pin": "24681357"})
	req, _ := http.NewRequest("POST", s.url+"/api/gate/pin", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+s.d.OwnerToken)
	req.Header.Set("X-Tmx-UI-Session", strings.Repeat("b", 64))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("set at the machine: %v %v", res.StatusCode, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.py.mu.Lock()
		n := len(s.py.pins)
		s.py.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PIN not pushed to the peer")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.py.mu.Lock()
	pushed, _ := s.py.pins[0]["record"].(map[string]any)
	s.py.mu.Unlock()
	if len(pushed) != 3 || len(pushed["salt"].(string)) != 32 || len(pushed["hash"].(string)) != 64 || pushed["set"].(float64) <= rec["set"].(float64)-1 {
		t.Fatalf("pushed %v", pushed)
	}
}

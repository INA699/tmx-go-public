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
	"github.com/blackcode-ch/tmx-go/internal/summarize"
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
	pc   *peers.Client
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
	return &peerSetup{d: d, url: d.WithPeers(t, pc), py: py, pair: pair, pc: pc}
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
		// on-behalf names whom the peer acts for on ITS OWN machine; it never makes the caller
		// the owner - Hosta's real traffic relays on-behalf of its own agents too
		// (agent:<name>), not just daemon/operator, and that must keep working.
		{"a peer relaying for one of its own agents", "GET", "/api/sessions", "hosta", s.pair, map[string]string{peers.HeaderOnBehalf: "agent:x@hosta"}, nil, 200},
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

// A peer forwarding a read for its OWN local operator (Python's relay-invites/autonomy
// polling: on_behalf "operator") reaches the ordinary mux like any other peer call, and gets a
// clean 404 for a route Go does not implement yet - not an on-behalf 403 (tmx-lead's #103:
// "answer 404, not 401, or work").
//
// Hard condition: on-behalf (whom the peer acts for on ITS OWN machine) never makes the caller
// the owner - it is always a fleet.PeerCaller, whatever on-behalf says (an agent's name only
// names the peer party its mail comes from). So (a) the owner driving Hostb's agents from Hosta's
// dashboard (relayed with on_behalf=operator) gets agent operations only (launch/screen work),
// never owner-only settings (gate/PIN/devices still 403); (b) Hosta relaying its OWN agents'
// or devices' reads (on_behalf=agent:<name> or device:<id>, tmx-lead's 3 Nov P1: this broke
// /api/health, /api/sessions, relay-invites polling) keeps working the same way.
func TestPeerOnBehalfOperatorReachesMuxAgentRelayStillRefused(t *testing.T) {
	s := newPeerSetup(t)
	operator := map[string]string{peers.HeaderOnBehalf: "operator"}

	if code := peerDo(t, s.url, "GET", "/api/pending", "hosta", s.pair, operator, nil, nil); code != 404 {
		t.Errorf("on-behalf operator, unimplemented route /api/pending: %d, want 404", code)
	}
	if code := peerDo(t, s.url, "GET", "/api/autonomy", "hosta", s.pair, operator, nil, nil); code != 404 {
		t.Errorf("on-behalf operator, unimplemented route /api/autonomy: %d, want 404", code)
	}

	// on_behalf=operator never becomes Owner: owner-only routes still 403.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/gate/pin"}, {"GET", "/api/devices"}, {"PUT", "/api/safety/guard"}, {"GET", "/api/config"},
	} {
		if code := peerDo(t, s.url, tc.method, tc.path, "hosta", s.pair, operator, nil, nil); code != 403 {
			t.Errorf("on-behalf operator on owner-only %s %s: %d, want 403", tc.method, tc.path, code)
		}
	}

	// on_behalf=operator still gets agent operations, like any full peer.
	var launched model.Agent
	if code := peerDo(t, s.url, "POST", "/api/agents", "hosta", s.pair, operator,
		map[string]string{"name": "via-operator", "mode": "terminal"}, &launched); code != 201 {
		t.Fatalf("on-behalf operator launch: %d", code)
	}
	if p := s.d.Term.Last(launched.ID); p != nil {
		p.SetReplay([]byte("hello from the terminal"))
	}
	if code := peerDo(t, s.url, "GET", "/api/agents/"+launched.ID+"/screen", "hosta", s.pair, operator, nil, nil); code != 200 {
		t.Errorf("on-behalf operator screen: %d, want 200", code)
	}

	// on_behalf=agent:<name> and device:<id> are peer calls too (tmx-lead's P1): 404 on an
	// unimplemented route, never the on-behalf 403 that broke Hosta's relayed reads.
	if code := peerDo(t, s.url, "GET", "/api/pending", "hosta", s.pair,
		map[string]string{peers.HeaderOnBehalf: "agent:x@hosta"}, nil, nil); code != 404 {
		t.Errorf("on-behalf agent relay: %d, want 404", code)
	}
	if code := peerDo(t, s.url, "GET", "/api/pending", "hosta", s.pair,
		map[string]string{peers.HeaderOnBehalf: "device:x"}, nil, nil); code != 404 {
		t.Errorf("on-behalf device relay: %d, want 404", code)
	}

	// Never Owner, whatever on-behalf says: still 403 on an owner-only route.
	if code := peerDo(t, s.url, "GET", "/api/gate/pin", "hosta", s.pair,
		map[string]string{peers.HeaderOnBehalf: "agent:x@hosta"}, nil, nil); code != 403 {
		t.Errorf("on-behalf agent relay on owner-only route: %d, want 403", code)
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
	// Owner material (go-reviewer-o/tmx-lead, 3 Nov): rotating the pair token is the peer
	// machine's own act, never a relayed agent, device or operator.
	for _, ob := range []string{"agent:x@hosta", "device:x", "operator"} {
		if code := peerDo(t, s.url, "POST", "/api/peers/pair-token", "hosta", s.pair,
			map[string]string{peers.HeaderOnBehalf: ob}, map[string]string{"token": peers.NewPairToken()}, nil); code != 403 {
			t.Errorf("pair-token rotation on-behalf %q: %d, want 403", ob, code)
		}
	}
}

// The owner PIN record (salt + scrypt hash) never leaves the machine: no peer reads it or
// pushes one, whatever on-behalf says, and a PIN set here is not pushed to the peers.
func TestPeerNeverReachesThePINRecord(t *testing.T) {
	s := newPeerSetup(t)
	rec := map[string]any{"salt": "000102030405060708090a0b0c0d0e0f", "hash": "53e59f1f9f56c2317d99f7a1e76fd1d43677ccb19c2c125e054f9a81281e0aec", "set": float64(time.Now().Unix())}
	for _, ob := range []string{"", "daemon", "operator", "agent:x@hosta"} {
		hdr := map[string]string{peers.HeaderOnBehalf: ob}
		var got map[string]any
		if code := peerDo(t, s.url, "GET", "/api/gate/pin/record", "hosta", s.pair, hdr, nil, &got); code == 200 || got["record"] != nil {
			t.Errorf("GET pin/record on-behalf %q: %d %v", ob, code, got)
		}
		if code := peerDo(t, s.url, "POST", "/api/gate/pin/record", "hosta", s.pair, hdr, map[string]any{"record": rec}, nil); code == 200 {
			t.Errorf("POST pin/record on-behalf %q: %d", ob, code)
		}
	}
	greq, _ := http.NewRequest("GET", s.url+"/api/gate/pin", nil)
	greq.Header.Set("Authorization", "Bearer "+s.d.OwnerToken)
	var st struct {
		Set bool `json:"set"`
	}
	if res, err := http.DefaultClient.Do(greq); err != nil || res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&st) != nil || st.Set {
		t.Fatalf("a peer's record was adopted: %+v %v", st, err)
	}
	b, _ := json.Marshal(map[string]string{"pin": "24681357"})
	req, _ := http.NewRequest("POST", s.url+"/api/gate/pin", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+s.d.OwnerToken)
	req.Header.Set("X-Tmx-UI-Session", strings.Repeat("b", 64))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 200 {
		t.Fatalf("set at the machine: %v %v", res.StatusCode, err)
	}
	time.Sleep(200 * time.Millisecond)
	s.py.mu.Lock()
	n := len(s.py.pins)
	s.py.mu.Unlock()
	if n != 0 {
		t.Fatal("the PIN record was pushed to a peer")
	}
}

// The summary route between our own machines (summaries.via_peer / serve_peers). Serving: only with the peer's pair
// token, only the peers this daemon serves, and the generate goes to its own loopback Ollama. Asking: a peer still
// on the Python daemon has no such route, and the reason says so.
func TestPeerSummarizeLocal(t *testing.T) {
	s := newPeerSetup(t)
	var gens int
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gens++
		_, _ = w.Write([]byte(`{"response":"{\"summary\":\"ok\"}"}`))
	}))
	t.Cleanup(ollama.Close)
	in := summarize.PeerGenerate{Model: summarize.DefaultLocalModel, Prompt: "Transcript: hi"}
	path := "/api/peer/summarize-local"

	s.d.SetSummaries(config.SummaryConfig{LocalURL: ollama.URL})
	if c := peerDo(t, s.url, "POST", path, "hosta", s.pair, nil, in, nil); c != 403 {
		t.Fatalf("a peer this daemon does not serve: %d", c)
	}
	s.d.SetSummaries(config.SummaryConfig{LocalURL: ollama.URL, ServePeers: []string{"hosta"}})
	if c := peerDo(t, s.url, "POST", path, "hosta", "", nil, in, nil); c != 401 {
		t.Fatalf("without the pair token: %d", c)
	}
	if c := peerDo(t, s.url, "POST", path, "hosta", s.d.OwnerToken, nil, in, nil); c != 401 {
		t.Fatalf("the owner token is not a peer token: %d", c)
	}
	if c := peerDo(t, s.url, "POST", path, "hosta", s.pair, nil, summarize.PeerGenerate{Model: "llama3:70b", Prompt: "p"}, nil); c != 400 {
		t.Fatalf("another model: %d", c)
	}
	if gens != 0 {
		t.Fatalf("a refused request reached Ollama")
	}
	var out summarize.PeerAnswer
	if c := peerDo(t, s.url, "POST", path, "hosta", s.pair, nil, in, &out); c != 200 || !strings.Contains(out.Response, "ok") || gens != 1 {
		t.Fatalf("served: %d %+v (%d generates)", c, out, gens)
	}

	// asking: hosta is still the Python daemon (pyPeer answers 404 to anything it does not know)
	if _, err := s.pc.SummarizeLocal(t.Context(), "hosta", in); err == nil || !strings.Contains(err.Error(), "no summary route yet") {
		t.Fatalf("a Python peer: %v", err)
	}
}

// A full peer reaches the agent-operation routes (launch/screen/restart, type/keys the way a
// head agent does) and mail, never as the owner: every other route answers 403 to it.
func TestPeerReachesAgentOpsButNeverOwnerOnlyRoutes(t *testing.T) {
	s := newPeerSetup(t)

	var launched model.Agent
	if code := peerDo(t, s.url, "POST", "/api/agents", "hosta", s.pair, nil,
		map[string]string{"name": "peer-launched", "mode": "terminal"}, &launched); code != 201 {
		t.Fatalf("peer launch: %d", code)
	}
	if p := s.d.Term.Last(launched.ID); p != nil {
		p.SetReplay([]byte("hello from the terminal"))
	}
	if code := peerDo(t, s.url, "GET", "/api/agents/"+launched.ID+"/screen", "hosta", s.pair, nil, nil, nil); code != 200 {
		t.Fatalf("peer screen: %d", code)
	}
	if code := peerDo(t, s.url, "POST", "/api/agents/"+launched.ID+"/restart", "hosta", s.pair, nil, nil, nil); code != 200 {
		t.Fatalf("peer restart: %d", code)
	}

	// Owner-only routes: gate/PIN, devices, pairing/invites, safety, secrets/accounts, update,
	// config. A full peer gets 403 on every one of them, and on everything else that is not an
	// agent operation or mail (peerOps), whatever it says it acts for.
	ownerOnly := []struct{ method, path string }{
		{"GET", "/api/gate/pin"},
		{"POST", "/api/gate/pin"},
		{"POST", "/api/gate/pin/clear"},
		{"PUT", "/api/gate/hello"},
		{"GET", "/api/devices"},
		{"POST", "/api/devices/approve"},
		{"GET", "/api/safety"},
		{"PUT", "/api/safety/guard"},
		{"GET", "/api/gate"},
		{"PUT", "/api/gate"},
		{"DELETE", "/api/agents/" + launched.ID},
		{"GET", "/api/config"},
		{"PUT", "/api/config"},
		{"GET", "/api/p2p"},
		{"GET", "/api/system/tools"},
		{"GET", "/api/update"},
		{"PUT", "/api/update"},
		{"PUT", "/api/update/keys"},
		{"PUT", "/api/update/token"},
		{"GET", "/api/accounts"},
		{"GET", "/api/whatsapp"},
		{"GET", "/api/whatsapp/chats"},
		{"PUT", "/api/policy"},
		{"GET", "/api/snapshot"},
		{"GET", "/api/messages"},
		{"GET", "/api/events"},
		{"POST", "/api/projects"},
		{"GET", "/api/agents/" + launched.ID + "/timeline"},
		{"GET", "/api/agents/" + launched.ID + "/terminal"},
		{"POST", "/api/agents/" + launched.ID + "/send"},
		{"POST", "/api/approvals"},
		{"GET", "/api/chats"},
		{"GET", "/api/matrix/mine"},
		{"GET", "/api/jobs"},
	}
	for _, ob := range []string{"daemon", "operator", "agent:tmx-lead@hosta"} {
		for _, tc := range ownerOnly {
			if code := peerDo(t, s.url, tc.method, tc.path, "hosta", s.pair, map[string]string{peers.HeaderOnBehalf: ob}, nil, nil); code != 403 {
				t.Errorf("peer (%s) on %s %s: %d, want 403", ob, tc.method, tc.path, code)
			}
		}
	}
}

// Mail a peer posts to /api/messages is the other machine's, never the owner's: from
// "daemon@<peer>", or from the peer's own agent it acts for.
func TestPeerMailIsNeverTheOwners(t *testing.T) {
	s := newPeerSetup(t)
	for _, tc := range []struct{ ob, from string }{
		{"daemon", "fleet:daemon@hosta"},
		{"operator", "fleet:daemon@hosta"},
		{"agent:builder@hosta", "fleet:builder@hosta"},
		{"agent:builder", "fleet:builder@hosta"},
		{"agent:builder@hostc", "fleet:daemon@hosta"}, // not the peer's own agent
	} {
		var m model.Message
		if code := peerDo(t, s.url, "POST", "/api/messages", "hosta", s.pair, map[string]string{peers.HeaderOnBehalf: tc.ob},
			map[string]string{"to": "head", "body": "hello from " + tc.ob}, &m); code != 201 {
			t.Fatalf("%s: %d", tc.ob, code)
		}
		if m.From != tc.from || m.From == model.Owner {
			t.Errorf("on-behalf %s: mail from %q, want %q", tc.ob, m.From, tc.from)
		}
	}
}

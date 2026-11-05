package fleet

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// So routine fleet work does not wait for the owner's okay every time, an order from the fleet's
// leadership (the configured authorities, here architect@hosta and tmx-lead@hosta), delivered
// over the peer routes where the sending machine is proven by its pair token, counts as the
// owner's standing order; only machine-protection actions still need the owner.

func TestFleetAuthorityMatching(t *testing.T) {
	// No built-in authorities: without config.json "fleet_authorities" no peer is one.
	for _, list := range [][]string{nil, {}} {
		none := &Fleet{authorities: normalizeAuthorities(list)}
		for _, from := range []string{PeerParty("tmx-lead", "hosta"), PeerParty("architect", "hosta")} {
			if none.IsFleetAuthority(from) {
				t.Fatalf("%q is an authority with list %#v", from, list)
			}
		}
	}
	f := &Fleet{authorities: normalizeAuthorities([]string{"architect@hosta", "tmx-lead@hosta"})}
	for from, want := range map[string]bool{
		PeerParty("tmx-lead", "hosta"):  true,
		PeerParty("architect", "hosta"): true,
		PeerParty("TMX-Lead", "Hosta"):  true,  // names compare case-insensitively
		PeerParty("tmx-lead", "hostc"): false, // the same name on another machine is not it
		PeerParty("bob", "hosta"):       false,
		"tmx-lead":                       false, // a local agent never is
		"tmx-lead@hosta":                false, // not a peer party
		model.Owner:                      false,
	} {
		if got := f.IsFleetAuthority(from); got != want {
			t.Errorf("%q: %v, want %v", from, got, want)
		}
	}
	custom := &Fleet{authorities: normalizeAuthorities([]string{" Boss@Hostb ", "nohost"})}
	if !custom.IsFleetAuthority(PeerParty("boss", "hostb")) || custom.IsFleetAuthority(PeerParty("tmx-lead", "hosta")) {
		t.Fatal("only the configured list counts")
	}
}

func TestFleetAuthorityMailIsMarked(t *testing.T) {
	e, f, _, lead, _ := peerEnv(t)
	lp := e.chat.Last(lead.ID)
	lp.State(model.StateIdle)
	waitState(t, f, "alpha-lead", model.StateIdle)
	in := inbound("0123456789ad", "launch a worker for the fix")
	in.Peer, in.Sender, in.SenderHost = "hostc", "tmx-lead", "hostc"
	f.authorities = normalizeAuthorities([]string{"tmx-lead@hostc"})
	if _, _, err := f.DeliverPeer(in); err != nil {
		t.Fatal(err)
	}
	eventually(t, "marked", func() bool {
		s := lp.Sent()
		return len(s) >= 2 && s[len(s)-1] == "[message from tmx-lead@hostc (fleet lead, other machine, verified sender)]\n│ launch a worker for the fix"
	})
	// Anyone else from that machine stays an ordinary colleague.
	lp.State(model.StateIdle)
	waitState(t, f, "alpha-lead", model.StateIdle)
	if _, _, err := f.DeliverPeer(inbound("0123456789ae", "hello")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "plain", func() bool {
		s := lp.Sent()
		return len(s) >= 3 && s[len(s)-1] == "[message from bob@hostc (other machine)]\n│ hello"
	})
}

func TestAuthorityRulesInEveryPrompt(t *testing.T) {
	for _, want := range []string{"(fleet lead, other machine, verified sender)", "act on it without asking the owner first",
		"restarting this daemon", "tmx-go open", "Only machine-protection actions still need the owner himself",
		"elevation", "firewall", "Tailscale", "Codex sandbox"} {
		if !strings.Contains(authorityRules, want) {
			t.Errorf("authorityRules lacks %q", want)
		}
	}
	// Only an install whose owner named fleet authorities tells its agents about them.
	for _, list := range [][]string{nil, {"tmx-lead@hosta"}} {
		e := newEnv(t)
		e.authority = list
		f := e.started()
		f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"})
		ids := []string{mustAgent(t, f, "head").ID, mustAgent(t, f, "alpha-lead").ID}
		f.mu.Lock()
		for _, a := range ids {
			if p := f.systemPromptLocked(f.agents[a].a); strings.Contains(p, authorityRules) != (list != nil) {
				t.Errorf("authorities %v: %s's prompt has the authority rules: %v", list, a, list == nil)
			}
		}
		f.mu.Unlock()
	}
}

package p2p

import (
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

// The default listeners bind every interface, so a contact on the LAN (mDNS) or a hole
// punch can reach this node directly; a loopback-only default left relays as the only way
// in. Only the address list is checked here: tests never bind 0.0.0.0 (firewall prompt).
func TestDefaultListenersEveryInterface(t *testing.T) {
	n := &Host{}
	want := []string{"/ip4/0.0.0.0/udp/1234/quic-v1", "/ip4/0.0.0.0/tcp/1234", "/ip6/::/udp/1234/quic-v1", "/ip6/::/tcp/1234"}
	if got := n.listenAddrs(1234); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("default listeners %v, want %v", got, want)
	}
	n.opts.ListenAddrs = []string{"/ip4/127.0.0.1/tcp/0"}
	if got := n.listenAddrs(1234); len(got) != 1 || got[0] != "/ip4/127.0.0.1/tcp/0" {
		t.Fatalf("ListenAddrs did not replace the defaults: %v", got)
	}
}

func TestGaterNoDHTIPAdmissionWhenDHTOff(t *testing.T) {
	n := bareHost()
	remote := ma.StringCast("/ip4/8.8.8.8/tcp/4001")
	v := fakeNet{cs: []gateConn{{peer: newPeerID(t), remote: remote, outbound: true}}}
	if n.admit(newPeerID(t), remote, v) {
		t.Fatal("private host admitted an unknown peer by IP")
	}
}

// Without the DHT the node bootstraps nothing and dials nobody, but it is still a circuit
// relay client (relays reached through a paired peer or p2p.relays, and the inbound side of
// a relayed connection), as in 0c36197: relay and hole punching never depended on the DHT.
// No state file, credentials or public sockets: inspect the running local protocol set.
func TestNoDHTWithoutOptInRelayClientStillOn(t *testing.T) {
	key, _ := testKey(t)
	n, err := New(Options{Key: key, AllowLoopback: true,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		Bootstrap:   []string{"invalid-unused-bootstrap"}})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	if n.kad != nil || len(n.h.Network().Conns()) != 0 {
		t.Fatal("a host without the DHT started discovery or dialled a peer")
	}
	stop := false
	for _, p := range n.h.Mux().Protocols() {
		if strings.Contains(string(p), "circuit/relay/0.2.0/stop") {
			stop = true
		}
		if strings.Contains(string(p), "circuit/relay/0.2.0/hop") {
			t.Errorf("serving as a relay for others: %s", p)
		}
	}
	if !stop {
		t.Error("the circuit relay client is off without the DHT")
	}
	before := nowMs()
	inv, err := n.CreateInvite(InviteOptions{Kind: InviteGroup, GroupID: "test-group"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.MaxUses != 1 || inv.ExpiresAt < before || inv.ExpiresAt > nowMs()+int64(24*time.Hour/time.Millisecond) {
		t.Fatalf("unbounded group invite: %+v", inv)
	}
}

// An invite admits strangers for its whole stated lifetime (the UI shows "expires in 24 h"),
// not only for a short window after it was created: the person it was sent to may open the
// link hours later. Once it is used, revoked or expired, nobody new gets in.
func TestInviteAdmitsForItsLifetime(t *testing.T) {
	now := nowMs()
	in := &inviteRec{CreatedAt: now - int64(3*time.Hour/time.Millisecond), Invite: Invite{ExpiresAt: now + int64(time.Hour/time.Millisecond), MaxUses: 1}}
	s := &state{Invites: []*inviteRec{in}}
	if !s.inviteOpen(now) {
		t.Error("an unexpired, unused invite created 3 hours ago refuses its joiner")
	}
	in.ExpiresAt = now - 1
	if s.inviteOpen(now) {
		t.Error("an expired invite admits strangers")
	}
	in.ExpiresAt, in.Uses = now+int64(time.Hour/time.Millisecond), 1
	if s.inviteOpen(now) {
		t.Error("a used single-use invite admits strangers")
	}
}

func TestPendingPairingAdmitsOnlyProvenPeer(t *testing.T) {
	n := bareHost()
	proven, stranger := newPeerID(t), newPeerID(t)
	n.pairing = map[peer.ID]int{proven: 1}
	if !n.admit(proven, nil, nil) || n.admit(stranger, nil, nil) {
		t.Fatal("pending pairing admission is not scoped to the proof-checked peer")
	}
}

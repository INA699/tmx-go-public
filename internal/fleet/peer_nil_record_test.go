package fleet

import "testing"

// A peer machine has no agent record here and is never the owner: these must refuse it, never
// panic on a nil record (callerRecLocked refuses a peer too).

func TestAskOwnerRefusesPeer(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	if _, err := f.AskOwner(PeerCaller("hosta"), AskRequest{Question: "from a peer?"}); KindOf(err) != KindForbidden {
		t.Fatalf("peer ask: %v", err)
	}
}

func TestRequestProcessRefusesPeer(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	if _, err := f.RequestProcess(PeerCaller("hosta"), ProcessRequest{Purpose: "look around", Command: "ls"}); KindOf(err) != KindForbidden {
		t.Fatalf("peer process request: %v", err)
	}
}

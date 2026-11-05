package fleet

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A peer machine launches what the head could, only inside this machine's workspace or the
// project's folder, and its first instruction is labelled as the other machine's.
func TestPeerLaunchIsCheckedLikeTheHead(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	p, err := f.CreateProject(OwnerCaller, ProjectRequest{Name: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	peer := PeerCallerAs("hosta", "builder")

	w, err := f.LaunchAgent(peer, LaunchRequest{Name: "pw", ProjectID: p.ID, Prompt: "run the tests"})
	if err != nil {
		t.Fatalf("peer launch: %v", err)
	}
	if s := e.chat.Last(w.ID).Sent(); len(s) != 1 || s[0] != "[message from builder@hosta (other machine)]\n│ run the tests" {
		t.Fatalf("peer prompt: %q", s)
	}
	if _, err := f.LaunchAgent(peer, LaunchRequest{Name: "pw2", ProjectID: p.ID, Cwd: "sub/dir"}); err != nil {
		t.Fatalf("relative cwd inside the project: %v", err)
	}
	for _, cwd := range []string{"/", filepath.Dir(e.workspace), "../..", filepath.Join(p.Folder, "..", "..", "elsewhere")} {
		if _, err := f.LaunchAgent(peer, LaunchRequest{Name: "pw3", ProjectID: p.ID, Cwd: cwd}); KindOf(err) != KindForbidden {
			t.Fatalf("peer launched in %q: %v", cwd, err)
		}
	}
	// The owner still may launch anywhere the admission allows.
	if _, err := f.LaunchAgent(OwnerCaller, LaunchRequest{Name: "ow", ProjectID: p.ID, Cwd: filepath.Dir(e.workspace)}); KindOf(err) == KindForbidden && strings.Contains(err.Error(), "another machine") {
		t.Fatalf("owner refused: %v", err)
	}
	// A profile the head can't launch is refused for a peer.
	if _, err := f.CreateProfile(OwnerCaller, model.Profile{Slug: "special", Name: "Special", Level: model.LevelWorker,
		Model: "sonnet", AllowedModels: []string{"sonnet"}, PermissionMode: "default"}); err != nil {
		t.Fatalf("profile: %v", err)
	}
	if _, err := f.LaunchAgent(peer, LaunchRequest{Name: "pw4", ProjectID: p.ID, Profile: "special"}); KindOf(err) != KindForbidden {
		t.Fatalf("peer launched a profile the head can't: %v", err)
	}
	if _, err := f.LaunchAgent(peer, LaunchRequest{Name: "pw5", ProjectID: p.ID, CodexFullAccess: true, Harness: model.HarnessCodex}); KindOf(err) != KindForbidden {
		t.Fatalf("peer opted into Codex full access: %v", err)
	}
}

// A peer's mail is from the other machine, never the owner; it reads no mailbox here, opens
// no project or thread, and is no relay to a third machine.
func TestPeerMailIsLabelledAndNothingElseIsOpen(t *testing.T) {
	e := newEnv(t)
	f := e.started()
	peer := PeerCaller("hosta")
	m, err := f.SendMessage(peer, SendRequest{To: HeadName, Body: "hello"})
	if err != nil {
		t.Fatalf("peer mail: %v", err)
	}
	if m.From != "fleet:daemon@hosta" || m.From == model.Owner {
		t.Fatalf("peer mail from %q", m.From)
	}
	f.mu.Lock()
	line := f.mailLineLocked(m)
	f.mu.Unlock()
	if line != "[message from daemon@hosta (other machine)]\n│ hello" {
		t.Fatalf("delivered as %q", line)
	}
	if _, err := f.Messages(peer, "", 0); KindOf(err) != KindForbidden {
		t.Fatalf("peer read the mailbox: %v", err)
	}
	if _, err := f.CreateProject(peer, ProjectRequest{Name: "beta"}); KindOf(err) != KindForbidden {
		t.Fatalf("peer created a project: %v", err)
	}
	if _, err := f.NewThread(peer, NewThreadRequest{Title: "x"}); KindOf(err) != KindForbidden {
		t.Fatalf("peer opened a thread: %v", err)
	}
	if _, err := f.SendMessage(peer, SendRequest{To: "someone@hostc", Body: "relay"}); KindOf(err) != KindForbidden && KindOf(err) != KindNotFound {
		t.Fatalf("peer relayed mail: %v", err)
	}
}

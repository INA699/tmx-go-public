package processes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
	"github.com/blackcode-ch/tmx-go/internal/runner/host/hosttest"
)

// slowSpawn makes host.Spawn wait for release; then it runs real (nil) or fails (fail).
func slowSpawn(t *testing.T, fail error) (entered, release chan struct{}) {
	t.Helper()
	old := spawnHost
	t.Cleanup(func() { spawnHost = old })
	entered, release = make(chan struct{}, 1), make(chan struct{})
	spawnHost = func(ctx context.Context, exe, dir string, spec host.Spec) (*host.Conn, error) {
		entered <- struct{}{}
		<-release
		if fail != nil {
			return nil, fail
		}
		return host.Spawn(ctx, exe, dir, spec)
	}
	return entered, release
}

func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked while a tool started", what)
	}
}

// A host that is slow to come up holds up nothing else: List, Promote, other starts and their
// stops go on; the starting tool is listed once it started. A stop of its owner meanwhile
// waits for it and ends it; a failed start leaves nothing behind.
func TestSlowStartBlocksNothing(t *testing.T) {
	entered, release := slowSpawn(t, errors.New("no host"))
	r := New()
	t.Cleanup(r.Close)
	r.EnableHosts("unused", t.TempDir())
	started := make(chan error, 1)
	go func() {
		_, err := r.Start(Spec{OwnerID: "agent-1", Purpose: "preview", Command: "x"})
		started <- err
	}()
	<-entered

	within(t, 2*time.Second, "List", func() {
		if l := r.List(); len(l) != 0 {
			t.Errorf("a starting tool is listed: %+v", l)
		}
	})
	var sys model.ProcessInfo
	within(t, 10*time.Second, "another start", func() { sys = helper(t, r, "system", true) })
	within(t, 10*time.Second, "another stop", func() { r.Stop(sys.ID) })

	stopped := make(chan struct{})
	go func() { r.StopOwner("agent-1"); close(stopped) }()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("the owner's stop did not wait for its starting tool")
	default:
	}
	close(release)
	if err := <-started; err == nil {
		t.Fatal("a failed start succeeded")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the owner's stop never returned")
	}
	for _, p := range r.List() {
		if p.ID != sys.ID {
			t.Fatalf("a failed start is listed: %+v", p)
		}
	}
}

// A stop that comes while a tool's host comes up ends the tool once it is up; a Detach that
// comes meanwhile leaves it running for the next daemon, as it does any started hosted tool.
func TestStopOrDetachWhileStarting(t *testing.T) {
	exe := hosttest.Executable(t)
	for _, detach := range []bool{false, true} {
		name := "stop"
		if detach {
			name = "detach"
		}
		t.Run(name, func(t *testing.T) {
			tick := filepath.Join(t.TempDir(), "tick")
			t.Setenv("PROCTEST_TICK_FILE", tick)
			dir := filepath.Join(t.TempDir(), "processes")
			entered, release := slowSpawn(t, nil)
			r := New()
			r.EnableHosts(exe, dir)
			started := make(chan error, 1)
			go func() {
				_, err := r.Start(Spec{OwnerID: "agent-1", Purpose: "watch", Command: os.Args[0], Cwd: t.TempDir()})
				started <- err
			}()
			<-entered
			finished := make(chan struct{})
			go func() {
				if detach {
					r.Detach()
				} else {
					r.StopOwner("agent-1")
				}
				close(finished)
			}()
			time.Sleep(50 * time.Millisecond)
			close(release)
			if err := <-started; err == nil {
				t.Fatal("Start reported a tool stopped while it started as started")
			}
			select {
			case <-finished:
			case <-time.After(20 * time.Second):
				t.Fatal("the stop never returned")
			}
			if !detach {
				if l := r.List(); len(l) != 1 || l[0].State != "stopped" || l[0].PID != 0 {
					t.Fatalf("after the stop: %+v", l)
				}
				n := ticks(t, tick)
				time.Sleep(600 * time.Millisecond)
				if after := ticks(t, tick); after > n+1 {
					t.Fatalf("the tool still runs after a stop: %d -> %d", n, after)
				}
				return
			}
			waitTicking(t, tick, ticks(t, tick), "after the detach")
			r2 := New()
			r2.EnableHosts(exe, dir)
			if got := r2.Adopt(); len(got) != 1 || got[0].State != "running" {
				t.Fatalf("adopted %+v", got)
			}
			r2.StopOwner("agent-1")
		})
	}
}

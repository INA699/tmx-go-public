//go:build darwin

package sysinfo

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// processStopped: the process is stopped (SIGSTOP), from sysctl kern.proc.pid.
func processStopped(pid int) bool {
	_, stat, err := procInfo(pid)
	return err == nil && stat == procStatStopped
}

// startSleeper starts a disposable child of this test: `sleep 30`, or a shell that execs it.
func startSleeper(t *testing.T, script string) (*exec.Cmd, Proc) {
	t.Helper()
	cmd := hiddenexec.Command("/bin/sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	p, _, err := procInfo(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, p
}

func TestDarwinIdentityRefusesAnotherProcess(t *testing.T) {
	_, p := startSleeper(t, "sleep 30")
	for name, want := range map[string]Proc{
		"start time": {PID: p.PID, Start: p.Start + 1, Name: p.Name},
		"name":       {PID: p.PID, Start: p.Start, Name: "not-" + p.Name},
		"no start":   {PID: p.PID, Name: p.Name},
	} {
		if h, err := openIdentity(want); !errors.Is(err, ErrNotChild) {
			if h != nil {
				h.close()
			}
			t.Errorf("%s: err = %v, want ErrNotChild", name, err)
		}
	}
	if _, err := openResume(Proc{PID: p.PID, Start: p.Start - 1}); !errors.Is(err, ErrNotChild) {
		t.Errorf("resume with another start time: err = %v, want ErrNotChild", err)
	}
	if _, err := openControlled(Proc{PID: p.PID, Start: p.Start, Name: "ssh"}); !errors.Is(err, ErrProtected) {
		t.Errorf("protected name: err = %v, want ErrProtected", err)
	}
}

func TestDarwinSuspendResumeKill(t *testing.T) {
	cmd, p := startSleeper(t, "exec sleep 30")
	time.Sleep(100 * time.Millisecond) // the exec
	p, _, _ = procInfo(p.PID)
	h, err := openControlled(p)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if !h.alive() {
		t.Fatal("a running process is not alive")
	}
	if err := h.suspend(); err != nil {
		t.Fatal(err)
	}
	if !processStopped(p.PID) {
		t.Fatal("suspend did not stop the process")
	}
	if err := h.resume(); err != nil {
		t.Fatal(err)
	}
	if processStopped(p.PID) {
		t.Fatal("resume did not continue the process")
	}
	if err := h.kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if h.alive() {
		t.Fatal("a killed process is still alive")
	}
	// Ended and reaped: its PID may now belong to anyone; nothing is sent to it.
	if err := h.suspend(); err == nil {
		t.Fatal("suspend signalled an ended process")
	}
	if err := h.kill(); err != nil {
		t.Fatalf("kill of an ended process: %v", err)
	}
}

// An exec keeps the process (the watch follows it), so its new name is accepted; a name change
// the watch did not report is not.
func TestDarwinExecInPinnedProcess(t *testing.T) {
	_, p := startSleeper(t, "sleep 0.3; exec sleep 30")
	h, err := openControlled(p)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		now, _, err := procInfo(p.PID)
		if err == nil && now.Name == "sleep" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell did not exec")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := h.suspend(); err != nil {
		t.Fatalf("suspend after exec: %v", err)
	}
	if err := h.resume(); err != nil {
		t.Fatalf("resume after exec: %v", err)
	}

	d := h.(*darwinProcess)
	d.mu.Lock()
	d.name = "something-else" // as if the PID now held another program, without an exec
	d.mu.Unlock()
	if err := h.suspend(); !errors.Is(err, ErrNotChild) {
		t.Fatalf("suspend of a renamed process: err = %v, want ErrNotChild", err)
	}
	if processStopped(p.PID) {
		t.Fatal("a refused suspend stopped the process")
	}
}

func TestDarwinClosedHandleSendsNothing(t *testing.T) {
	_, p := startSleeper(t, "sleep 30")
	h, err := openControlled(p)
	if err != nil {
		t.Fatal(err)
	}
	h.close()
	h.close()
	if err := h.suspend(); !errors.Is(err, ErrNotChild) {
		t.Fatalf("closed handle: err = %v", err)
	}
	if processStopped(p.PID) {
		t.Fatal("a closed handle stopped the process")
	}
	if err := unix.Kill(p.PID, 0); err != nil {
		t.Fatal("the process ended")
	}
}

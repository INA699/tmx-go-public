//go:build darwin

package sysinfo

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// macOS has no pidfd and no process handle a signal can be sent through, so a darwinProcess
// pins a process as tightly as the system allows:
//
//   - A kqueue watches the process (EVFILT_PROC, NOTE_EXIT|NOTE_EXEC) from the moment it is
//     opened. The kernel attaches that watch to the process itself, not to its number: if it
//     ends, the exit is reported even when the PID is later reused.
//   - Right after the watch is set, and again immediately before every signal, its start time
//     (sysctl kern.proc.pid, microseconds, as Procs records it) and image name must still be
//     the ones it was opened with. An exec in that same process (reported by the watch) may
//     change the name; a protected image is then refused.
//
// Only kill(2) by number remains: between the last check and the signal the process would
// have to exit, be reaped by its parent and have its PID handed to a new process, all within
// microseconds. macOS assigns PIDs in increasing order up to 99999 before it wraps, so that
// takes a full cycle of PIDs inside that window; the start-time check rules out any slower
// reuse. A process that already exited is never signalled.
type darwinProcess struct {
	mu         sync.Mutex
	kq         int // -1 once closed
	pid        int
	start      int64
	name       string
	controlled bool // refuse a protected image (openControlled), also after an exec
	exited     bool
	execed     bool
}

// procStatZombie and procStatStopped are p_stat values (sys/proc.h).
const (
	procStatStopped = 4 // SSTOP
	procStatZombie  = 5 // SZOMB
)

func openIdentity(p Proc) (controlledProcess, error) { return openDarwin(p, false) }

func openControlled(p Proc) (controlledProcess, error) {
	if ProtectedProcess(p.Name) {
		return nil, ErrProtected
	}
	return openDarwin(p, true)
}

// openResume is restart recovery: the ledger's start time must match; the name is read from
// the process, then checked like any controlled one.
func openResume(want Proc) (controlledProcess, error) {
	if want.PID <= 0 || want.Start <= 0 {
		return nil, ErrNotChild
	}
	now, _, err := procInfo(want.PID)
	if err != nil || now.Start != want.Start {
		return nil, ErrNotChild
	}
	return openControlled(now)
}

func openDarwin(p Proc, controlled bool) (controlledProcess, error) {
	if p.PID <= 0 || p.Start <= 0 {
		return nil, ErrNotChild
	}
	// Close-on-exec under ForkLock: the watch must not leak into an agent the daemon starts.
	syscall.ForkLock.RLock()
	kq, err := unix.Kqueue()
	if err == nil {
		unix.CloseOnExec(kq)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	var ev unix.Kevent_t
	unix.SetKevent(&ev, p.PID, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ENABLE|unix.EV_CLEAR)
	ev.Fflags = unix.NOTE_EXIT | unix.NOTE_EXEC
	if _, err := keventRetry(kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		unix.Close(kq)
		if errors.Is(err, unix.ESRCH) {
			return nil, ErrNotChild
		}
		return nil, err
	}
	h := &darwinProcess{kq: kq, pid: p.PID, start: p.Start, name: p.Name, controlled: controlled}
	// The watch is set: the process checked now is the one it follows (or its exit is queued).
	now, _, err := procInfo(p.PID)
	if err != nil || now.Start != p.Start || (p.Name != "" && now.Name != p.Name) {
		h.close()
		return nil, ErrNotChild
	}
	h.name = now.Name
	return h, nil
}

// procInfo reads one process's identity, as Procs reports it, and its state (p_stat).
func procInfo(pid int) (Proc, int8, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Proc{}, 0, err
	}
	if int(k.Proc.P_pid) != pid {
		return Proc{}, 0, unix.ESRCH
	}
	st := k.Proc.P_starttime
	return Proc{
		PID: pid, PPID: int(k.Eproc.Ppid), PGID: int(k.Eproc.Pgid),
		Start: int64(st.Sec)*1_000_000 + int64(st.Usec),
		Name:  unix.ByteSliceToString(k.Proc.P_comm[:]),
	}, k.Proc.P_stat, nil
}

func keventRetry(kq int, changes, events []unix.Kevent_t, timeout *unix.Timespec) (int, error) {
	for {
		n, err := unix.Kevent(kq, changes, events, timeout)
		if err != unix.EINTR {
			return n, err
		}
	}
}

// pollLocked collects what the watch reported, without waiting.
func (p *darwinProcess) pollLocked() {
	if p.kq < 0 || p.exited {
		return
	}
	var evs [4]unix.Kevent_t
	for {
		n, err := keventRetry(p.kq, nil, evs[:], &unix.Timespec{})
		if err != nil || n == 0 {
			return
		}
		for _, ev := range evs[:n] {
			if ev.Fflags&unix.NOTE_EXIT != 0 || ev.Flags&unix.EV_EOF != 0 {
				p.exited = true
			}
			if ev.Fflags&unix.NOTE_EXEC != 0 {
				p.execed = true
			}
		}
	}
}

// signal sends sig only if the process is still the one opened: not exited (the watch), same
// start time and image name (sysctl, just before the signal).
func (p *darwinProcess) signal(sig unix.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kq < 0 {
		return ErrNotChild
	}
	p.pollLocked()
	if p.exited {
		return unix.ESRCH
	}
	now, stat, err := procInfo(p.pid)
	if err != nil || now.Start != p.start {
		return ErrNotChild
	}
	if stat == procStatZombie {
		return unix.ESRCH
	}
	if now.Name != p.name {
		// A new image is fine only in the very process the watch follows.
		p.pollLocked()
		if !p.execed || p.exited {
			return ErrNotChild
		}
		if p.controlled && ProtectedProcess(now.Name) {
			return ErrProtected
		}
		p.name, p.execed = now.Name, false
	}
	return unix.Kill(p.pid, sig)
}

func (p *darwinProcess) suspend() error { return p.signal(unix.SIGSTOP) }
func (p *darwinProcess) resume() error  { return p.signal(unix.SIGCONT) }

// kill: a process that already ended needs no kill.
func (p *darwinProcess) kill() error {
	if err := p.signal(unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	return nil
}

func (p *darwinProcess) alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kq < 0 {
		return false
	}
	p.pollLocked()
	return !p.exited
}

func (p *darwinProcess) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.kq >= 0 {
		unix.Close(p.kq)
		p.kq = -1
	}
}

// watchdogCommand: the resume watchdog gets no environment and a session of its own, so a
// signal to the daemon's process group or terminal does not end it before it resumes.
func watchdogCommand(cmd *exec.Cmd) {
	cmd.Env = []string{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

const pinnedControlAvailable = true

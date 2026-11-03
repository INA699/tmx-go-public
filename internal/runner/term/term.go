// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Package term runs an agent in a pseudo-terminal (terminal mode): the user's shell, or a
// harness's interactive TUI (Claude Code, Codex, opencode, pi). Output is streamed raw and
// kept in a replay ring buffer. For a harness, a virtual screen (package screen) is fed the
// same output and read for the agent's state and its menus, which become approvals.
package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	pty "github.com/aymanbagabas/go-pty"

	"github.com/blackcode-ch/tmx-go/internal/codexguard"
	"github.com/blackcode-ch/tmx-go/internal/harness"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/claude"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

const (
	// EventBuffer is the capacity of Process.Events; the consumer must drain it, or reading
	// the terminal (and so the program in it) stalls.
	EventBuffer = 1024
	// ReplaySize is how much recent output Replay keeps.
	ReplaySize = 1 << 20

	defaultCols, defaultRows = 120, 32
	quietAfter               = 2 * time.Second // a shell without output this long = idle
	exitGrace                = 2 * time.Second // after "exit" / SIGTERM, before escalating
	killGrace                = 3 * time.Second
)

type termRunner struct{}

// New returns the terminal-mode runner.
func New() runner.Runner { return termRunner{} }

// launch is what Start runs.
type launch struct {
	path string
	args []string
	env  []string
	info *harness.Info // nil for a shell
	cmd  harness.Command
}

// command picks the program for spec: a harness's TUI, or a shell.
func command(spec runner.Spec) (launch, error) {
	if spec.Harness == model.HarnessShell || spec.Harness == "" {
		return launch{path: shellPath(spec), args: spec.ExtraArgs}, nil
	}
	info, ok := harness.Get(spec.Harness)
	if !ok {
		return launch{}, fmt.Errorf("terminal mode does not know harness %q", spec.Harness)
	}
	c, err := harness.TerminalCommand(spec)
	if err != nil {
		return launch{}, err
	}
	return launch{path: c.Path, args: c.Args, env: c.Env, info: &info, cmd: c}, nil
}

// envFor is the process's final environment. The owner's own shell (HarnessShell: only the
// owner may launch one, fleet.LaunchAgent) is the owner, not an agent, so it keeps their
// usual gh/ssh/account environment unchanged; every other harness gets the owner-credential
// denylist (agentpolicy.FilterEnv, via claude.BaseEnv/MergeEnv).
func envFor(spec runner.Spec, l launch) []string {
	env := append(append(append([]string{}, spec.Env...), l.env...), "TERM=xterm-256color")
	if spec.Harness == model.HarnessShell {
		return claude.MergeEnvUnfiltered(claude.BaseEnvUnfiltered(), env...)
	}
	return claude.MergeEnv(claude.BaseEnv(), env...)
}

func shellPath(spec runner.Spec) string {
	if spec.Command != "" {
		return spec.Command
	}
	if runtime.GOOS == "windows" {
		if c := os.Getenv("COMSPEC"); c != "" {
			return c
		}
		return "powershell.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// Start opens the pty and launches the program. ctx only bounds the start itself.
func (termRunner) Start(ctx context.Context, spec runner.Spec) (runner.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := runner.RefuseElevated(spec); err != nil {
		return nil, err
	}
	if spec.Harness == model.HarnessCodex {
		if err := harness.ValidateCodexLaunch(spec); err != nil {
			return nil, err
		}
	}
	var guardLease *codexguard.Lease
	var guardErr error
	if spec.Harness == model.HarnessCodex {
		spec, guardLease, guardErr = harness.PrepareCodexGuard(spec)
		if guardErr != nil {
			return nil, guardErr
		}
	}
	guardStarted := false
	defer func() {
		if guardLease != nil && !guardStarted {
			guardLease.Close()
		}
	}()
	l, err := command(spec)
	if err != nil {
		return nil, err
	}
	if guardLease != nil {
		if err := codexguard.ProbeEffective(ctx, spec.Command, harness.CodexAppServerArgs(spec), envFor(spec, l), spec.Cwd, spec.CodexGuardExecutable); err != nil {
			return nil, err
		}
	}
	// One claude start at a time per CLAUDE_CONFIG_DIR (claude.BeginStart also checks it).
	var ticket *claude.StartTicket
	if l.info != nil && l.info.Name == model.HarnessClaude {
		if ticket, err = claude.BeginStart(ctx, envFor(spec, l), claude.HolderAgent, spec.OnStartWait); err != nil {
			return nil, err
		}
	}
	started := false
	defer func() {
		switch {
		case ticket == nil:
		case started:
			ticket.Settled()
		default:
			ticket.Release()
		}
	}()
	cols, rows := spec.Cols, spec.Rows
	if cols <= 0 || rows <= 0 {
		cols, rows = defaultCols, defaultRows
	}
	pt, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	if err := pt.Resize(cols, rows); err != nil {
		pt.Close()
		return nil, fmt.Errorf("size pty: %w", err)
	}
	argv0, args := l.path, l.args
	if spec.LowPriority {
		argv0, args = sysinfo.LowPriorityCommand(argv0, args) // macOS: low disk I/O policy
	}
	cmd := pt.Command(argv0, args...)
	cmd.Dir = spec.Cwd
	prepareCommand(cmd)
	cmd.Env = envFor(spec, l)
	if err := cmd.Start(); err != nil {
		pt.Close()
		return nil, fmt.Errorf("start %s: %w", l.path, err)
	}
	started = true
	guardStarted = true
	sysinfo.RegisterChild(cmd.Process.Pid, l.info == nil)
	if spec.LowPriority {
		_ = sysinfo.LowerPriority(cmd.Process.Pid) // best effort; children inherit it
	}
	afterStart(pt)

	p := &process{
		guardLease: guardLease,
		pty:        pt,
		cmd:        cmd,
		shell:      l.info == nil,
		events:     make(chan runner.Event, EventBuffer),
		ring:       newRing(ReplaySize),
		done:       make(chan struct{}),
	}
	p.setState(model.StateStarting)
	if l.info == nil {
		p.quiet = time.AfterFunc(quietAfter, func() { p.setState(model.StateIdle) })
	} else {
		p.w = newWatcher(p, *l.info, spec, l.cmd, cols, rows)
	}
	readDone := make(chan struct{})
	go p.read(readDone)
	go p.wait(readDone)
	if p.w != nil {
		go p.w.run()
		go p.w.watchSession()
	}
	return p, nil
}

type process struct {
	guardLease *codexguard.Lease
	pty        pty.Pty
	cmd        *pty.Cmd
	shell      bool
	ring       *ring
	w          *watcher // nil for a shell

	wmu sync.Mutex // serialises writes to the pty

	mu     sync.Mutex // guards state, closed and sending on events; never held across a pty write
	state  model.AgentState
	closed bool
	events chan runner.Event
	quiet  *time.Timer // shells only
	turnAt time.Time   // when the current turn began (working after idle)

	done chan struct{}
}

func (p *process) Events() <-chan runner.Event { return p.events }
func (p *process) PID() int                    { return p.cmd.Process.Pid }
func (p *process) Replay() []byte              { return p.ring.Bytes() }

// Write sends a viewer's input (owner or, absent a pending approval, a head/lead typing into
// its worker). Answers to terminal queries are dropped: the runner's own screen answers them,
// and a second answer would arrive as typed garbage.
//
// Lock order: fleet.mu is released by the caller (fleet.procForWriteAct) before Write is ever
// called, so Write only ever has to take p.mu itself here - never the reverse (Write must not
// be called with fleet.mu held). A non-owner's check happens under p.mu, the same lock the
// approval detector (watcher.evaluate -> menuLocked, runner/term/watch.go) holds while it flips
// w.pending/p.state to StateWaiting: either the detector's section runs first and the write
// sees pending and is refused, or the check runs first and the detector's transition waits
// until the key was written (watch.go's guardedWrite).
//
// Nothing holds p.mu across a pty write (Write, the detector's own typeText/press/pick, the
// screen's query answers): a pty write blocks while the program is not reading its input, and
// the output reader needs p.mu to drain what the program prints meanwhile.
//
// A shell has no watcher and nothing between its input and the commands it runs: only the
// owner may write into it (runner.ErrOwnerOnly otherwise).
func (p *process) Write(b []byte, owner bool) error {
	if p.w != nil {
		if b = stripQueryReplies(b); len(b) == 0 {
			return nil
		}
		return p.w.guardedWrite(b, owner)
	}
	if !owner {
		return runner.ErrOwnerOnly
	}
	return p.write(b)
}

func (p *process) write(b []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	n, err := p.pty.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

// Send types text and presses Enter. A harness gets it as a bracketed paste once its input
// box is ready (earlier sends wait for that), Enter after the harness's submit delay.
func (p *process) Send(text string) error {
	if p.w != nil {
		return p.w.send(text)
	}
	return p.write([]byte(text + "\r"))
}

func (p *process) SendMail(text string, ids []string) error {
	if p.w != nil {
		return p.w.sendMail(mailInput{text: text, ids: append([]string(nil), ids...)})
	}
	err := p.Send(text)
	p.mu.Lock()
	stage := "submitted"
	if err != nil {
		stage = "unconfirmed"
	}
	p.emitLocked(runner.Event{Kind: runner.EvMailDelivery, MessageIDs: ids, Delivery: stage})
	p.mu.Unlock()
	// A partial terminal write is uncertain; it must never trigger a blind replay.
	return nil
}

func (p *process) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return errors.New("terminal size must be positive")
	}
	if p.w != nil {
		p.w.scr.Resize(cols, rows)
	}
	return p.pty.Resize(cols, rows)
}

func (p *process) Answer(id string, d runner.Decision) error {
	if p.w == nil {
		return errors.New("a shell has no menus to answer; answer in the terminal")
	}
	return p.w.answer(id, d)
}

// Interrupt: Esc for a harness ("esc to interrupt"), Ctrl-C for a shell.
func (p *process) Interrupt() error {
	if p.w != nil {
		return p.write([]byte{0x1b})
	}
	return p.write([]byte{0x03})
}

// Stop asks politely ("exit" for a shell, SIGTERM for a harness); after exitGrace it signals
// the whole session, and once ctx has expired and killGrace passed, it kills it.
func (p *process) Stop(ctx context.Context) error {
	if p.shell {
		go func() { _ = p.write([]byte("exit\r")) }() // not waited on: a shell not reading its input is signalled below all the same
	} else {
		terminate(p.cmd)
	}
	if p.waitDone(ctx, exitGrace) {
		return nil
	}
	terminate(p.cmd)
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
	}
	if p.waitDone(context.Background(), killGrace) {
		return nil
	}
	kill(p.cmd)
	<-p.done
	return nil
}

// waitDone reports whether the process ended within d (or before ctx expired).
func (p *process) waitDone(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.done:
		return true
	case <-ctx.Done():
		select {
		case <-p.done:
			return true
		default:
			return false
		}
	case <-t.C:
		return false
	}
}

func (p *process) emitLocked(ev runner.Event) {
	if !p.closed {
		p.events <- ev
	}
}

func (p *process) setStateLocked(s model.AgentState) {
	if p.state != s {
		if s == model.StateWorking && p.state != model.StateWaiting {
			p.turnAt = time.Now()
		}
		p.state = s
		p.emitLocked(runner.Event{Kind: runner.EvState, State: s})
	}
}

func (p *process) setState(s model.AgentState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setStateLocked(s)
}

func (p *process) read(done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, err := p.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			p.ring.Write(chunk)
			if p.w != nil {
				p.w.output(chunk)
			}
			p.mu.Lock()
			if p.w == nil {
				p.setStateLocked(model.StateWorking)
			}
			p.emitLocked(runner.Event{Kind: runner.EvOutput, Output: chunk})
			p.mu.Unlock()
			if p.quiet != nil {
				p.quiet.Reset(quietAfter)
			}
		}
		if err != nil {
			return
		}
	}
}

// wait reaps the process, lets the reader drain, closes the pty and emits EvExit.
func (p *process) wait(readDone <-chan struct{}) {
	if p.guardLease != nil {
		defer p.guardLease.Close()
	}
	err := p.cmd.Wait()
	select {
	case <-readDone:
	case <-time.After(500 * time.Millisecond): // a background job may still hold the tty
	}
	p.pty.Close()
	<-readDone
	if p.quiet != nil {
		p.quiet.Stop()
	}

	ev := runner.Event{Kind: runner.EvExit, ExitCode: -1}
	if st := p.cmd.ProcessState; st != nil {
		ev.ExitCode = st.ExitCode()
	}
	if ev.ExitCode != 0 {
		ev.Err = err
		if p.w != nil { // what the harness said last, like a chat runner's stderr tail
			if last := p.w.lastWords(); last != "" {
				ev.Err = fmt.Errorf("%v: %s", err, last)
			}
		}
	}
	p.mu.Lock()
	p.emitLocked(ev)
	p.closed = true
	close(p.events)
	p.mu.Unlock()
	close(p.done)
	if p.w != nil {
		p.w.scr.Close()
	}
}

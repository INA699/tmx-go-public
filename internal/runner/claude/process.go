// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Package claude runs Claude Code in chat mode: `claude -p` speaking stream-json on
// stdin/stdout, mapped to runner events. See ARCHITECTURE.md "Claude stream-json protocol".
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

// EventBuffer is the capacity of Process.Events. The consumer must drain the channel
// continuously: when it is full, reading claude's stdout (and so claude) stalls.
const EventBuffer = 1024

const (
	stderrTail = 8 << 10
	killGrace  = 3 * time.Second
)

type chatRunner struct{ log *slog.Logger }

// New returns the chat-mode runner.
func New() runner.Runner { return chatRunner{log: slog.Default()} }

// Start launches claude. ctx only bounds the start itself: the process lives until Stop or
// until it exits.
func (r chatRunner) Start(ctx context.Context, spec runner.Spec) (runner.Process, error) {
	if spec.Harness != "" && spec.Harness != model.HarnessClaude {
		return nil, fmt.Errorf("chat mode supports only the claude harness, not %q", spec.Harness)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := runner.RefuseElevated(spec); err != nil {
		return nil, err
	}
	path, err := LookPath(spec)
	if err != nil {
		return nil, err
	}
	argv0, args := path, ChatArgs(spec)
	if spec.LowPriority {
		argv0, args = sysinfo.LowPriorityCommand(path, args) // macOS: low disk I/O policy
	}
	cmd := hiddenexec.Command(argv0, args...)
	cmd.Dir = spec.Cwd
	cmd.Env = MergeEnv(BaseEnv(), spec.Env...)
	// One start at a time per CLAUDE_CONFIG_DIR (startqueue.go); BeginStart also checks it.
	ticket, err := BeginStart(ctx, cmd.Env, HolderAgent, spec.OnStartWait)
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if started {
			ticket.Settled()
		} else {
			ticket.Release()
		}
	}()
	cmd.WaitDelay = 2 * time.Second // grandchildren holding stderr must not block Wait
	setProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &process{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan runner.Event, EventBuffer),
		stderr: &tailBuffer{max: stderrTail},
		done:   make(chan struct{}),
		log:    r.log.With("agent", spec.Name),
	}
	cmd.Stderr = p.stderr
	p.parser = newParser(p.nextID(), p.log)
	p.parser.sessionID = spec.SessionID
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	started = true
	sysinfo.RegisterChild(cmd.Process.Pid, false)
	if spec.LowPriority {
		_ = sysinfo.LowerPriority(cmd.Process.Pid) // best effort; children inherit it
	}
	var o out
	p.parser.setState(&o, model.StateStarting)
	p.emitAll(o.events)
	go p.run(stdout)
	if err := p.write(controlRequest(p.parser.initID, "initialize")); err != nil {
		p.log.Warn("claude: initialize not sent", "err", err)
	}
	return p, nil
}

type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	wmu   sync.Mutex // serialises stdin writes
	seq   atomic.Int64

	mu     sync.Mutex // guards parser and emission order
	parser *parser
	events chan runner.Event
	closed bool

	stderr   *tailBuffer
	done     chan struct{} // closed after EvExit was emitted and events closed
	stopOnce sync.Once
	log      *slog.Logger
}

func (p *process) Events() <-chan runner.Event { return p.events }
func (p *process) PID() int                    { return p.cmd.Process.Pid }
func (p *process) Replay() []byte              { return nil }
func (p *process) Resize(int, int) error       { return nil }

func (p *process) Write([]byte, bool) error {
	return errors.New("raw input is only for terminal mode")
}

func (p *process) nextID() string { return fmt.Sprintf("tmx-%d", p.seq.Add(1)) }

// emit sends one event; the caller holds p.mu or is the only goroutine left.
func (p *process) emit(ev runner.Event) {
	if !p.closed {
		p.events <- ev
	}
}

func (p *process) write(line []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.stdin == nil {
		return errors.New("claude: input closed")
	}
	_, err := p.stdin.Write(line)
	return err
}

// Send starts a user turn. Sending while a turn runs is allowed: claude queues it.
func (p *process) Send(text string) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.stdin == nil {
		return errors.New("claude: input closed")
	}
	p.mu.Lock()
	p.parser.busy = true
	sid := p.parser.sessionID
	p.emit(runner.Event{Kind: runner.EvTimeline, Entry: &model.TimelineEntry{Kind: model.EntryUser, Text: text}})
	if p.parser.state != model.StateWaiting {
		var o out
		p.parser.setState(&o, model.StateWorking)
		p.emitAll(o.events)
	}
	p.mu.Unlock()
	_, err := p.stdin.Write(userMessage(text, sid))
	return err
}

func (p *process) Answer(requestID string, d runner.Decision) error {
	p.mu.Lock()
	input, ok := p.parser.pending[requestID]
	if ok {
		delete(p.parser.pending, requestID)
		if len(p.parser.pending) == 0 {
			var o out
			p.parser.setState(&o, model.StateWorking)
			p.emitAll(o.events)
		}
	}
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending permission request %q", requestID)
	}
	if !d.Allow {
		return p.write(permissionDeny(requestID, d.Message))
	}
	if d.UpdatedInput != nil {
		if !json.Valid(d.UpdatedInput) {
			return errors.New("updated input is not valid JSON")
		}
		input = d.UpdatedInput
	}
	return p.write(permissionAllow(requestID, input))
}

func (p *process) Interrupt() error {
	return p.write(controlRequest(p.nextID(), "interrupt"))
}

// Stop closes stdin (claude exits after the current turn), then on ctx expiry terminates the
// process group and kills it after killGrace.
func (p *process) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.wmu.Lock()
		if p.stdin != nil {
			_ = p.stdin.Close()
			p.stdin = nil
		}
		p.wmu.Unlock()
	})
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
	}
	terminate(p.cmd)
	select {
	case <-p.done:
		return nil
	case <-time.After(killGrace):
	}
	kill(p.cmd)
	<-p.done
	return nil
}

func (p *process) emitAll(evs []runner.Event) {
	for _, ev := range evs {
		p.emit(ev)
	}
}

// run reads stdout until EOF, then reaps the process and emits EvExit.
func (p *process) run(stdout io.Reader) {
	br := bufio.NewReaderSize(stdout, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			p.handle(line)
		}
		if err != nil {
			break
		}
	}
	waitErr := p.cmd.Wait()
	reapGroup(p.cmd) // whatever the agent left running in its process group

	ev := runner.Event{Kind: runner.EvExit, ExitCode: -1}
	if st := p.cmd.ProcessState; st != nil {
		ev.ExitCode = st.ExitCode()
	}
	if ev.ExitCode != 0 {
		msg := strings.TrimSpace(p.stderr.String())
		switch {
		case msg != "" && waitErr != nil:
			ev.Err = fmt.Errorf("%v: %s", waitErr, msg)
		case msg != "":
			ev.Err = errors.New(msg)
		default:
			ev.Err = waitErr
		}
	}
	p.mu.Lock()
	p.emit(ev)
	p.closed = true
	close(p.events)
	p.mu.Unlock()
	close(p.done)
}

func (p *process) handle(line []byte) {
	p.mu.Lock()
	o := p.parser.line(line)
	p.emitAll(o.events)
	p.mu.Unlock()
	for _, r := range o.replies {
		if err := p.write(r); err != nil {
			p.log.Debug("claude: reply not sent", "err", err)
		}
	}
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

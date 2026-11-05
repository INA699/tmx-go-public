// Package processes owns explicitly launched tools and their supervision.
package processes

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner/host"
	"github.com/blackcode-ch/tmx-go/internal/sysinfo"
)

type Spec struct {
	OwnerID, ProjectID, Purpose, Command, Cwd string
	Args                                      []string
	System                                    bool // trusted daemon code only
}

type entry struct {
	info     model.ProcessInfo
	spec     Spec
	cmd      *exec.Cmd  // a tool of the daemon's own (system tools, or no hosts)
	conn     *host.Conn // a tool in a session host (hosted.go)
	dir      string     // its host folder, for a hosted tool
	exited   chan struct{}
	done     chan struct{}
	cancel   chan struct{}
	stopping bool
	detached bool // Detach: it runs on in its host; its end here is not an exit
	unknown  bool // adopted, but its host could not be checked (hosted.go)
	starting bool // reserved by Start, its program not yet published: not listed
	identity int64
}

type Registry struct {
	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	// hostExe and dir: agents' tools run in session hosts that outlive the daemon
	// (EnableHosts); "" = the daemon's own children.
	hostExe, dir string
}

func New() *Registry { return &Registry{entries: make(map[string]*entry)} }

// Start reserves the tool's entry, starts its program without holding mu (a hosted tool's host
// takes seconds to come up, and List, Stop and every other start must not wait for it), then
// publishes it. A stop or Detach that came meanwhile acts on it once it is published.
func (r *Registry) Start(s Spec) (model.ProcessInfo, error) {
	if s.OwnerID == "" || s.Purpose == "" || s.Command == "" {
		return model.ProcessInfo{}, errors.New("owner, purpose and command are required")
	}
	s.Args = append([]string(nil), s.Args...)
	e := &entry{spec: s, done: make(chan struct{}), cancel: make(chan struct{}), starting: true}
	e.info = model.ProcessInfo{ID: model.NewID(), Category: model.CategoryAgentTool, OwnerID: s.OwnerID, ProjectID: s.ProjectID, Purpose: s.Purpose, Command: filepath.Base(s.Command)}
	e.info.CreatorID = s.OwnerID
	if s.System {
		e.info.Category = model.CategorySystemTool
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return model.ProcessInfo{}, errors.New("process registry closed")
	}
	how := r.howToStart(e)
	r.entries[e.info.ID] = e // reserved: not listed while it starts
	r.mu.Unlock()

	l, err := how.launch()

	r.mu.Lock()
	e.starting = false
	if err != nil && !s.System {
		delete(r.entries, e.info.ID)
		r.mu.Unlock()
		close(e.done) // releases a stop that came while it started
		return model.ProcessInfo{}, err
	}
	r.publish(e, l, err)
	info, stopped := e.info, e.stopping
	r.mu.Unlock()
	go r.supervise(e)
	if stopped {
		return model.ProcessInfo{}, errors.New("the tool was stopped while it started")
	}
	return info, nil
}

// startPlan is what launch needs, copied under mu: launch itself runs without it.
type startPlan struct {
	spec    Spec
	hostExe string // "" = the daemon's own child
	toolDir string // a hosted tool's folder
}

// launched is a started program, not yet published to its entry.
type launched struct {
	cmd      *exec.Cmd
	identity int64
	conn     *host.Conn
}

// howToStart plans e's next start. Called with r.mu held.
func (r *Registry) howToStart(e *entry) startPlan {
	p := startPlan{spec: e.spec}
	if r.hosted(e) {
		p.hostExe, p.toolDir = r.hostExe, r.toolDir(e.info.ID)
		e.dir = filepath.Join(p.toolDir, "host")
	}
	return p
}

// launch starts the program. Called without r.mu.
func (p startPlan) launch() (launched, error) {
	if p.hostExe != "" {
		c, err := launchHosted(p) // hosted.go
		return launched{conn: c}, err
	}
	cmd := hiddenexec.Command(p.spec.Command, p.spec.Args...)
	cmd.Dir = p.spec.Cwd
	// Do not delegate the daemon's bearer credentials or harness identity to tools.
	cmd.Env = toolEnv(os.Environ())
	if err := cmd.Start(); err != nil {
		return launched{}, err
	}
	sysinfo.RegisterChild(cmd.Process.Pid, false)
	l := launched{cmd: cmd}
	if tree, err := sysinfo.RegisteredTree(cmd.Process.Pid); err == nil && len(tree) > 0 {
		l.identity = tree[0].Start
	}
	return l, nil
}

// publish makes a launch the entry's program (or records that it failed). Called with r.mu
// held; Wait alone owns cmd.Wait. A Detach that came while it started leaves a hosted tool
// running in its host, as it does for one that had started.
func (r *Registry) publish(e *entry, l launched, err error) {
	if err != nil {
		e.cmd, e.conn = nil, nil
		e.info.PID = 0
		e.info.State = "retrying"
		return
	}
	e.info.StartedAt = model.Now()
	if l.conn != nil {
		r.attach(e, l.conn)
		r.saveMeta(e)
		if e.detached {
			_ = l.conn.Close()
		}
		return
	}
	e.cmd = l.cmd
	e.identity = l.identity
	e.exited = make(chan struct{})
	e.info.PID = l.cmd.Process.Pid
	e.info.State = "running"
	go func(cmd *exec.Cmd, done chan struct{}) { _ = cmd.Wait(); close(done) }(l.cmd, e.exited)
}

func (r *Registry) supervise(e *entry) {
	defer func() {
		r.mu.Lock()
		if e.stopping && !e.detached {
			e.info.PID = 0
			e.info.State = "stopped"
		}
		r.mu.Unlock()
		close(e.done)
	}()
	for {
		r.mu.Lock()
		exited := e.exited
		running := e.cmd != nil || e.conn != nil
		r.mu.Unlock()
		if running {
			select {
			case <-exited:
			case <-e.cancel:
				r.mu.Lock()
				detached := e.detached
				r.mu.Unlock()
				if !detached {
					r.terminate(e)
				}
				return
			}
		}
		r.mu.Lock()
		if e.detached { // the daemon left it running in its host
			r.mu.Unlock()
			return
		}
		e.info.PID = 0
		e.info.State = "exited"
		if e.conn != nil {
			r.forgetHosted(e) // hosted.go: its host has ended with it
		}
		if e.stopping || !e.spec.System {
			r.mu.Unlock()
			return
		}
		e.info.State = "retrying"
		r.mu.Unlock()
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-e.cancel:
			timer.Stop()
			return
		case <-timer.C:
		}
		r.mu.Lock()
		if e.stopping {
			r.mu.Unlock()
			return
		}
		e.info.Restarts++
		how := r.howToStart(e)
		r.mu.Unlock()
		l, err := how.launch()
		r.mu.Lock()
		r.publish(e, l, err) // a stop meanwhile ends it at the top of the loop
		r.mu.Unlock()
	}
}

func (r *Registry) terminate(e *entry) {
	r.mu.Lock()
	cmd, conn, exited, identity := e.cmd, e.conn, e.exited, e.identity
	e.info.State = "stopping"
	r.mu.Unlock()
	switch {
	case conn != nil:
		r.terminateHosted(e, conn, exited) // hosted.go
	case cmd != nil:
		// Pin before graceful exit can reparent descendants; PID reuse cannot
		// redirect this cleanup to a replacement process.
		if cleanup, err := sysinfo.PinStopTree(cmd.Process.Pid, identity); err == nil {
			defer cleanup()
		}
		// Unix SIGTERM; Windows cannot signal a hidden console gracefully. Give
		// already-exiting children a grace period before the verified handle kill.
		graceful(cmd.Process)
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-exited:
			timer.Stop()
		case <-timer.C:
			// Original os.Process handle only; descendants use the pinned snapshot.
			_ = cmd.Process.Kill()
			<-exited
		}
	}
	r.mu.Lock()
	e.info.PID = 0
	e.info.State = "stopped"
	r.mu.Unlock()
}

func (r *Registry) List() []model.ProcessInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.ProcessInfo, 0, len(r.entries))
	for _, e := range r.entries {
		if !e.starting {
			out = append(out, e.info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Promote(id, owner, project string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[id]
	if e == nil || e.stopping || e.info.State != "running" || e.spec.System {
		return errors.New("no running agent tool")
	}
	e.info.OwnerID = owner
	e.info.ProjectID = project
	e.info.Promoted = true
	if e.conn != nil {
		r.saveMeta(e) // hosted.go: the next daemon adopts it with its new owner
	}
	return nil
}

func (r *Registry) stopMatching(match func(model.ProcessInfo) bool) {
	r.mu.Lock()
	var wait []chan struct{}
	for _, e := range r.entries {
		if match(e.info) {
			if !e.stopping {
				e.stopping = true
				close(e.cancel)
			}
			wait = append(wait, e.done)
		}
	}
	r.mu.Unlock()
	for _, done := range wait {
		<-done
	}
}
func (r *Registry) Stop(id string) {
	r.stopMatching(func(p model.ProcessInfo) bool { return p.ID == id })
}
func (r *Registry) StopOwner(owner string) {
	r.stopMatching(func(p model.ProcessInfo) bool { return p.OwnerID == owner && p.Category != model.CategorySystemTool })
}

func (r *Registry) StopProject(project string) {
	r.stopMatching(func(p model.ProcessInfo) bool {
		return p.ProjectID == project && p.Category != model.CategorySystemTool
	})
}

// Close stops every tool. A daemon that is restarting uses Detach instead.
func (r *Registry) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.stopMatching(func(model.ProcessInfo) bool { return true })
}

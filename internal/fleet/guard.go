// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package fleet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/guard"
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
)

// The dangerous-command guard's daemon side (ARCHITECTURE.md "Safety"): chat-mode Claude agents get
// a PreToolUse hook (`tmx hook pretooluse`) through --settings; what it holds becomes an
// Approval of kind guard that the hook waits on, what it blocks becomes an error row in the
// agent's timeline.

// HoldRequest is POST /api/guard/holds and POST /api/guard/blocks (from the agent's hook).
type HoldRequest struct {
	Tool      string          `json:"tool"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Command   string          `json:"command"`
	Rule      string          `json:"rule"`
	Detail    string          `json:"detail,omitempty"`
	Match     string          `json:"match,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
}

// hookTimeout is Claude's limit for one hook run: longer than the hook waits for the owner.
const hookTimeout = guard.HoldWait + 5*time.Minute

type safetyState struct {
	settings guard.Settings
	holds    map[string]chan struct{} // pending guard approval ID -> closed when it is resolved
}

func (f *Fleet) safetyLocked() *safetyState {
	if f.safety == nil {
		s := guard.DefaultSettings()
		if f.opts.Guard != nil {
			s = *f.opts.Guard
		}
		f.safety = &safetyState{settings: s, holds: map[string]chan struct{}{}}
	}
	return f.safety
}

// GuardSettings returns the guard's on/off settings.
func (f *Fleet) GuardSettings() guard.Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.safetyLocked().settings
	s.OffFor = slices.Clone(s.OffFor)
	if s.OffFor == nil {
		s.OffFor = []string{}
	}
	return s
}

// SetGuardSettings changes them (owner only; the API asks the owner gate first when the change
// protects fewer agents). Running agents keep their hook until they restart.
func (f *Fleet) SetGuardSettings(c Caller, s guard.Settings) (guard.Settings, error) {
	if !c.Owner {
		return guard.Settings{}, errf(KindForbidden, "only the owner changes the guard")
	}
	off := []string{}
	for _, k := range s.OffFor {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || len(k) > 64 {
			return guard.Settings{}, errf(KindInvalid, "%q is not a kind of agent", k)
		}
		if !slices.Contains(off, k) {
			off = append(off, k)
		}
	}
	s.OffFor = off
	if f.opts.SaveGuard != nil {
		if err := f.opts.SaveGuard(s); err != nil {
			return guard.Settings{}, err
		}
	}
	f.mu.Lock()
	f.safetyLocked().settings = s
	f.mu.Unlock()
	f.log.Info("guard settings changed", "enabled", s.Enabled, "off_for", s.OffFor)
	return f.GuardSettings(), nil
}

// ApprovalByID returns one approval.
func (f *Fleet) ApprovalByID(id string) (model.Approval, error) {
	ap, err := f.st.GetApproval(id)
	if err != nil {
		return model.Approval{}, errf(KindNotFound, "no approval %q", id)
	}
	return ap, nil
}

func (f *Fleet) guardInfo(req HoldRequest, outcome guard.Outcome) (model.GuardHold, error) {
	r, ok := guard.RuleByID(req.Rule)
	if !ok || r.Outcome != outcome {
		return model.GuardHold{}, errf(KindInvalid, "%q is not a %s rule of the guard", req.Rule, outcome)
	}
	if len(req.Command) > MaxBodyBytes || len(req.ToolInput) > 4*MaxBodyBytes {
		return model.GuardHold{}, errf(KindInvalid, "the held call is too large")
	}
	in := req.ToolInput
	if len(in) > 0 && !json.Valid(in) {
		in = nil
	}
	return model.GuardHold{Outcome: string(outcome), Rule: r.ID, RuleName: r.Name, Why: r.Why,
		Detail: req.Detail, Match: req.Match, Command: req.Command, Tool: req.Tool, ToolInput: in, Cwd: req.Cwd}, nil
}

// Hold records a tool call the guard held (agent only): a pending Approval of kind guard in
// Needs you, and the agent waits for it.
func (f *Fleet) Hold(c Caller, req HoldRequest) (model.Approval, error) {
	if c.Owner {
		return model.Approval{}, errf(KindForbidden, "only an agent's guard hook holds calls")
	}
	info, err := f.guardInfo(req, guard.Hold)
	if err != nil {
		return model.Approval{}, err
	}
	raw, _ := json.Marshal(info)
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.callerRecLocked(c)
	if err != nil {
		return model.Approval{}, err
	}
	ap := model.Approval{ID: model.NewID(), AgentID: r.a.ID, Kind: model.ApprovalGuard, Tool: req.Tool,
		Input: raw, Description: info.Detail, Options: []string{}, State: model.ApprovalPending, CreatedAt: model.Now()}
	if err := f.st.PutApproval(ap); err != nil {
		return model.Approval{}, err
	}
	f.safetyLocked().holds[ap.ID] = make(chan struct{})
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Tool: req.Tool,
		Text: "The guard held this for you: " + info.RuleName,
		Meta: map[string]any{"guard": info, "approval_id": ap.ID}})
	r.waiting++
	r.a.State = model.StateWaiting
	f.saveAgentLocked(r)
	f.bus.Publish(model.BusEvent{Type: model.EvApprovalCreated, AgentID: r.a.ID, ProjectID: r.a.ProjectID, Data: ap})
	return ap, nil
}

// WaitHold waits up to wait for the owner's decision on one of the caller's holds and returns
// the approval as it then is.
func (f *Fleet) WaitHold(ctx context.Context, c Caller, id string, wait time.Duration) (model.Approval, error) {
	f.mu.Lock()
	ap, err := f.ownHoldLocked(c, id)
	done := f.safetyLocked().holds[id]
	f.mu.Unlock()
	if err != nil {
		return model.Approval{}, err
	}
	if ap.State == model.ApprovalPending && done != nil {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
		case <-ctx.Done():
		case <-f.ctx.Done():
		}
	}
	return f.ApprovalByID(id)
}

// CancelHold expires one of the caller's holds (its hook gave up waiting).
func (f *Fleet) CancelHold(c Caller, id string) (model.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ap, err := f.ownHoldLocked(c, id)
	if err != nil {
		return model.Approval{}, err
	}
	if ap.State == model.ApprovalPending {
		f.resolveHoldLocked(&ap, model.ApprovalExpired, "", "No answer in time; not run: ")
	}
	return ap, nil
}

func (f *Fleet) ownHoldLocked(c Caller, id string) (model.Approval, error) {
	ap, err := f.st.GetApproval(id)
	if err != nil || ap.Kind != model.ApprovalGuard {
		return model.Approval{}, errf(KindNotFound, "no guard hold %q", id)
	}
	if !c.Owner && ap.AgentID != c.AgentID {
		return model.Approval{}, errf(KindForbidden, "this hold belongs to another agent")
	}
	return ap, nil
}

// resolveHoldLocked finishes a guard approval, wakes its hook and writes the timeline row.
func (f *Fleet) resolveHoldLocked(ap *model.Approval, state model.ApprovalState, answer, text string) {
	f.finishApprovalLocked(ap, state, answer)
	st := f.safetyLocked()
	if ch, ok := st.holds[ap.ID]; ok {
		close(ch)
		delete(st.holds, ap.ID)
	}
	r, ok := f.agents[ap.AgentID]
	if !ok {
		return
	}
	var info model.GuardHold
	_ = json.Unmarshal(ap.Input, &info)
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntrySystem, Tool: ap.Tool, Text: text + info.RuleName,
		Meta: map[string]any{"guard": info, "approval_id": ap.ID, "decision": string(state)}})
	f.releaseWaitLocked(r)
}

func (f *Fleet) releaseWaitLocked(r *agentRec) {
	if r.waiting > 0 {
		r.waiting--
	}
	if r.waiting == 0 && r.a.State == model.StateWaiting {
		r.a.State = model.StateWorking
	}
	f.saveAgentLocked(r)
}

// decideGuard resolves a guard approval (owner only; the API has asked the owner gate).
func (f *Fleet) decideGuard(ap model.Approval, d DecideRequest) (model.Approval, error) {
	var state model.ApprovalState
	var text string
	switch d.Decision {
	case "allow":
		state, text = model.ApprovalAllowed, "You allowed it once: "
	case "deny":
		state, text = model.ApprovalDenied, "You denied it: "
	default:
		return model.Approval{}, errf(KindInvalid, "decision must be allow or deny for a guard hold")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, waiting := f.safetyLocked().holds[ap.ID]; !waiting {
		f.finishApprovalLocked(&ap, model.ApprovalExpired, "")
		return ap, errf(KindConflict, "the agent is no longer waiting for this; the hold expired")
	}
	f.resolveHoldLocked(&ap, state, d.Message, text)
	return ap, nil
}

// ReportBlock writes a blocked call into the agent's timeline as an error row (agent only).
func (f *Fleet) ReportBlock(c Caller, req HoldRequest) error {
	if c.Owner {
		return errf(KindForbidden, "only an agent's guard hook reports blocks")
	}
	info, err := f.guardInfo(req, guard.Block)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.callerRecLocked(c)
	if err != nil {
		return err
	}
	text := "Blocked by the guard: " + info.RuleName
	if info.Detail != "" {
		text += " (" + info.Detail + ")"
	}
	f.appendLocked(r, model.TimelineEntry{Kind: model.EntryError, Tool: req.Tool, Text: text, IsError: true,
		Meta: map[string]any{"guard": info}})
	f.log.Warn("guard blocked a tool call", "agent", r.a.Name, "rule", info.Rule, "detail", info.Detail)
	return nil
}

// expireHoldsLocked expires the open holds of an agent whose process ended.
func (f *Fleet) expireHoldsLocked(agentID string) {
	st := f.safetyLocked()
	for id, ch := range st.holds {
		ap, err := f.st.GetApproval(id)
		if err != nil || ap.AgentID != agentID {
			continue
		}
		close(ch)
		delete(st.holds, id)
		if ap.State == model.ApprovalPending {
			f.finishApprovalLocked(&ap, model.ApprovalExpired, "")
		}
	}
}

// guardSpecLocked installs the enforced adapter or refuses the launch. Claude
// supports PreToolUse in both modes; other harnesses cannot satisfy an enabled
// guard yet. An explicit owner guard opt-out remains subject to launch policy.
func (f *Fleet) guardSpecLocked(r *agentRec, spec *runner.Spec) error {
	a := r.a
	if a.Harness == model.HarnessShell {
		return nil // owner terminal; agent authorization denies launching or typing into it
	}
	if err := f.guardAdmissionLocked(a); err != nil {
		return err
	}
	if !f.safetyLocked().settings.On(string(a.Level), a.Profile) { // off_for may name a level or a profile
		return nil
	}
	if f.codexParityEligibleLocked(a) {
		spec.CodexGuardHome = filepath.Join(f.opts.Paths.AgentDir(a.ID), "codex-guard")
		spec.CodexGuardExecutable = f.opts.Executable
		spec.Env = append(spec.Env, guard.EnvFolder+"="+a.Cwd, guard.EnvWorkspace+"="+f.opts.Workspace, guard.EnvState+"="+f.opts.Paths.Home)
		return nil
	}
	cfg := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{
		"matcher": guard.Matcher,
		"hooks": []any{map[string]any{"type": "command", "command": HookCommand(f.opts.Executable),
			"timeout": int(hookTimeout / time.Second)}},
	}}}}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := f.opts.Paths.AgentDir(a.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	if err := config.WriteFileAtomic(path, b, 0o600); err != nil {
		return err
	}
	spec.SettingsPath = path
	spec.Env = append(spec.Env, guard.EnvFolder+"="+a.Cwd, guard.EnvWorkspace+"="+f.opts.Workspace,
		guard.EnvState+"="+f.opts.Paths.Home)
	return nil
}

// HookCommand is the hook's command line, quoted for the shell Claude Code runs hooks with.
func HookCommand(exe string) string {
	if runtime.GOOS == "windows" {
		return `"` + exe + `" hook pretooluse`
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' hook pretooluse"
}

// hooksDisabledIn names a Claude settings file that switches every hook off, if any.
func hooksDisabledIn(cwd string) string {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	files = append(files, filepath.Join(cwd, ".claude", "settings.json"), filepath.Join(cwd, ".claude", "settings.local.json"))
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s struct {
			DisableAllHooks bool `json:"disableAllHooks"`
		}
		if json.Unmarshal(b, &s) == nil && s.DisableAllHooks {
			return p
		}
	}
	return ""
}

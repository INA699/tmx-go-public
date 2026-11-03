// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Package guard classifies an agent's tool call (a shell command, a file write) as allowed,
// held for the owner, or blocked. It never runs anything and never touches the network; the
// only files it reads are a repository's .git/HEAD (for the git rules). `tmx hook pretooluse` applies it to chat-mode Claude
// agents through a Claude Code PreToolUse hook, which runs in every permission mode, including
// bypassPermissions. See ARCHITECTURE.md "Safety".
//
// It is a seatbelt against accidents and casual prompt injection, not a sandbox: the agent runs
// as the owner's OS user, and a determined program can always hide what it does.
package guard

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/agentpolicy"
)

// Outcome is what happens to a tool call.
type Outcome string

const (
	Allow Outcome = "allow" // runs as usual (Claude's own permission mode still applies)
	Hold  Outcome = "hold"  // waits for the owner's decision in Needs you
	Block Outcome = "block" // never runs; the agent is told why
)

func (o Outcome) rank() int {
	switch o {
	case Block:
		return 2
	case Hold:
		return 1
	}
	return 0
}

// Verdict is the result of a check.
type Verdict struct {
	Outcome  Outcome `json:"outcome"`
	Rule     string  `json:"rule,omitempty"`      // Rule.ID
	RuleName string  `json:"rule_name,omitempty"` // Rule.Name
	Why      string  `json:"why,omitempty"`       // Rule.Why
	Detail   string  `json:"detail,omitempty"`    // what exactly matched, in plain words
	Match    string  `json:"match,omitempty"`     // the part of the command that matched
}

// Message is the sentence shown to the agent for a block or a hold.
func (v Verdict) Message() string {
	var b strings.Builder
	b.WriteString("tmx guard (" + v.RuleName + "): ")
	if v.Detail != "" {
		b.WriteString(v.Detail + ". ")
	}
	b.WriteString(v.Why)
	return b.String()
}

// Call is one tool call as Claude Code's PreToolUse hook reports it.
type Call struct {
	Tool  string          `json:"tool_name"`
	Input json.RawMessage `json:"tool_input"`
	Cwd   string          `json:"cwd"`
}

// Context is where the agent stands. Paths are absolute; empty ones are ignored.
type Context struct {
	AllowBrowserAutomation bool   // trusted host setting; never read from agent environment
	OS                     string // runtime.GOOS of the machine ("darwin", "linux", "windows")
	Cwd                    string // the agent's current folder (the hook's cwd; else Own)
	Own                    string // the agent's own folder: recursive deletes inside it are fine
	Workspace              string // project folders: writes inside it are fine
	StateDir               string // tmx's state directory: never written, secrets never read
	Home                   string
	Temp                   []string            // scratch folders (TMPDIR, /tmp, %TEMP%): writes and deletes are fine
	Env                    func(string) string // environment lookup for $VAR / %VAR%; nil = none
	// TmxNames are extra names of the tmx executable (basenames or full paths) besides "tmx".
	TmxNames []string
	// Git answers "which branch is checked out in dir, and which is the remote's default?";
	// nil = read .git (only HEAD files; nothing is run).
	Git func(dir string) (branch, remoteDefault string)
}

// Settings is the owner's guard configuration (config.json "guard").
type Settings struct {
	AllowBrowserAutomation bool `json:"allow_browser_automation"`
	Enabled                bool `json:"enabled"`
	// OffFor names the kinds of agents the guard is off for: levels today ("head", "lead",
	// "worker"), agent profiles once they exist.
	OffFor []string `json:"off_for"`
}

// DefaultSettings: the guard is on for everyone.
func DefaultSettings() Settings { return Settings{Enabled: true, OffFor: []string{}} }

// On reports whether the guard applies to an agent described by keys (its level, later its
// profile): globally on and off for none of them.
func (s Settings) On(keys ...string) bool {
	if !s.Enabled {
		return false
	}
	for _, k := range keys {
		for _, off := range s.OffFor {
			if k != "" && strings.EqualFold(k, off) {
				return false
			}
		}
	}
	return true
}

// Weaker reports whether next protects fewer agents than s (switching it off globally or for
// one more kind of agent): that change needs the owner's confirmation.
func (s Settings) Weaker(next Settings) bool {
	if !s.AllowBrowserAutomation && next.AllowBrowserAutomation {
		return true
	}
	if s.Enabled && !next.Enabled {
		return true
	}
	if !next.Enabled {
		return false
	}
	for _, k := range next.OffFor {
		if s.On(k) {
			return true
		}
	}
	return false
}

// Environment the daemon gives a guarded agent (inherited by its hook).
const (
	EnvFolder    = "TMXG_GUARD_FOLDER"    // the agent's own folder
	EnvWorkspace = "TMXG_GUARD_WORKSPACE" // the workspace
	EnvState     = "TMXG_GUARD_STATE"     // tmx's state directory
)

// HoldWait is how long a held call waits for the owner before it is denied.
const HoldWait = 10 * time.Minute

// Matcher is the Claude Code hook matcher: the tools the guard looks at.
const Matcher = ".*"

// Check classifies one tool call.
func Check(ctx Context, call Call) Verdict {
	if v := checkBrowser(ctx, call.Tool); v.Outcome == Block {
		return v
	}
	if call.Cwd != "" {
		ctx.Cwd = call.Cwd
	}
	if ctx.Cwd == "" {
		ctx.Cwd = ctx.Own
	}
	var in map[string]any
	_ = json.Unmarshal(call.Input, &in)
	str := func(k string) string {
		s, _ := in[k].(string)
		return s
	}
	switch call.Tool {
	case "tmx_exec":
		var exec struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal(call.Input, &exec); err != nil || strings.TrimSpace(exec.Command) == "" {
			return Verdict{Outcome: Block, Rule: RuleTmxState, Detail: "invalid guarded executable request"}
		}
		return CheckExec(ctx, exec.Command, exec.Args)
	case "Bash":
		return CheckCommand(ctx, ShellPOSIX, str("command"))
	case "PowerShell":
		return CheckCommand(ctx, ShellPowerShell, str("command"))
	case "Write", "Edit", "MultiEdit":
		return checkFile(ctx, str("file_path"), true)
	case "NotebookEdit":
		return checkFile(ctx, str("notebook_path"), true)
	case "Read":
		return checkFile(ctx, str("file_path"), false)
	case "Grep":
		return checkFile(ctx, str("path"), false)
	}
	return Verdict{Outcome: Allow}
}

// Shell is the syntax a command is written in.
type Shell int

const (
	ShellPOSIX      Shell = iota // sh, bash, zsh (also Git Bash on Windows)
	ShellPowerShell              // Windows PowerShell and pwsh
	ShellCmd                     // cmd.exe (inside cmd /c ...)
)

// CheckExec classifies an executable and literal arguments without inventing
// shell expansion. Interpreter arguments are analyzed by the existing handlers.
func CheckExec(ctx Context, executable string, argv []string) Verdict {
	a := newAnalyzer(ctx)
	values := append([]string{executable}, argv...)
	args := make([]arg, len(values))
	for i, value := range values {
		args[i] = arg{s: value, raw: value}
	}
	raw := strings.Join(values, " ")
	sh := ShellPOSIX
	if ctx.OS == "windows" {
		sh = ShellPowerShell
	}
	a.dispatch(args, raw, sh, nil)
	v := a.verdict()
	if browser := checkBrowser(ctx, raw); browser.Outcome.rank() > v.Outcome.rank() {
		return browser
	}
	return v
}

// CheckCommand classifies a shell command line.
func CheckCommand(ctx Context, sh Shell, command string) Verdict {
	if strings.TrimSpace(command) == "" {
		return Verdict{Outcome: Allow}
	}
	a := newAnalyzer(ctx)
	a.script(command, sh)
	v := a.verdict()
	if v.Outcome == Block {
		return v
	}
	if browser := checkBrowser(ctx, command); browser.Outcome.rank() > v.Outcome.rank() {
		return browser
	}
	return v
}

// checkBrowser applies the host browser policy: an actual launch or target is a Block, a mere
// mention (an argument, a file name, a commit message) is a Hold the owner can approve.
func checkBrowser(ctx Context, command string) Verdict {
	switch agentpolicy.ClassifyBrowserCommand(ctx.OS, ctx.AllowBrowserAutomation, command) {
	case agentpolicy.BrowserCommand:
		r := ruleByID[RuleBrowser]
		return Verdict{Outcome: Block, Rule: r.ID, RuleName: r.Name, Why: r.Why}
	case agentpolicy.BrowserMention:
		r := ruleByID[RuleBrowserMention]
		return Verdict{Outcome: Hold, Rule: r.ID, RuleName: r.Name, Why: r.Why}
	}
	return Verdict{Outcome: Allow}
}

func checkFile(ctx Context, p string, write bool) Verdict {
	if p == "" {
		return Verdict{Outcome: Allow}
	}
	a := newAnalyzer(ctx)
	t := a.resolve(p)
	match := p
	if write {
		a.write(t, match)
	} else {
		a.read(t, match)
	}
	return a.verdict()
}

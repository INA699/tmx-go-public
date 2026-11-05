// Package model holds the types shared by the daemon, the API, the CLI and (mirrored in
// ui/src/lib/types.ts) the UI. JSON names are the contract; see DESIGN.md.
package model

import (
	"crypto/rand"
	"encoding/json"
	"regexp"
	"time"
)

type Level string

const (
	LevelHead   Level = "head"
	LevelLead   Level = "lead"
	LevelWorker Level = "worker"
)

type Harness string

const (
	HarnessClaude   Harness = "claude"
	HarnessShell    Harness = "shell"
	HarnessCodex    Harness = "codex"
	HarnessOpenCode Harness = "opencode"
	HarnessPi       Harness = "pi"
	HarnessGemini   Harness = "gemini"
)

type Mode string

const (
	ModeChat     Mode = "chat"
	ModeTerminal Mode = "terminal"
)

type AgentState string

const (
	StateStarting AgentState = "starting"
	StateWorking  AgentState = "working"
	StateIdle     AgentState = "idle"
	StateWaiting  AgentState = "waiting" // a permission request or question is pending
	StateStopped  AgentState = "stopped"
	StateExited   AgentState = "exited"
	StateError    AgentState = "error"
	// Resource control (DESIGN.md "Resources"):
	StateQueued AgentState = "queued" // waits for capacity (max agents, free RAM); starts in order
	StatePaused AgentState = "paused" // its process tree is suspended by a brake; PausedReason says why
)

// Owner is the From/To value for the human.
const Owner = "owner"

// CompanionParty is the From of mail from Companion, the owner's desktop agent app (a message
// it sent through `tmx mcp --companion`, or the outcome of a task tmx handed to it).
const CompanionParty = "companion"

type Agent struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ProjectID string  `json:"project_id,omitempty"`
	ParentID  string  `json:"parent_id,omitempty"`
	Level     Level   `json:"level"`
	Profile   string  `json:"profile"` // profile slug; "" in old rows = the built-in of the level
	Harness   Harness `json:"harness"`
	// Account is the harness account the agent runs as (internal/accounts; "" = the
	// harness's usual home, as before there were several).
	Account        string     `json:"account,omitempty"`
	Mode           Mode       `json:"mode"`
	Model          string     `json:"model"`
	Cwd            string     `json:"cwd"`
	Role           string     `json:"role"`
	Brief          string     `json:"brief"`
	State          AgentState `json:"state"`
	Desired        string     `json:"desired"` // running | stopped
	SessionID      string     `json:"session_id"`
	PID            int        `json:"pid"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
	LastActivityAt int64      `json:"last_activity_at"`
	ShelvedAt      int64      `json:"shelved_at,omitempty"`
	DoneAt         int64      `json:"done_at,omitempty"`
	Protected      bool       `json:"protected,omitempty"`
	CostUSD        float64    `json:"cost_usd"`
	Turns          int        `json:"turns"`
	Error          string     `json:"error"`
	Queued         int        `json:"queued"`
	// Resource control; kept in memory (not in the store), rebuilt after a restart.
	QueuePos     int    `json:"queue_pos,omitempty"`     // state queued: 1 = next to start
	QueueReason  string `json:"queue_reason,omitempty"`  // state queued: what it waits for
	PausedReason string `json:"paused_reason,omitempty"` // state paused: write_budget | memory | disk
	PausedDetail string `json:"paused_detail,omitempty"` // state paused: a sentence for the owner
	PausedAt     int64  `json:"paused_at,omitempty"`
	// Live mode switch (DESIGN.md "Switching live"); kept in memory, not in the store, so a
	// daemon restart brings the agent back in the mode it was launched in.
	HomeMode    Mode `json:"home_mode,omitempty"`    // set while switched: the mode it was launched in
	PendingMode Mode `json:"pending_mode,omitempty"` // it switches to this mode when its current turn ends

	CodexFullAccess bool `json:"codex_full_access,omitempty"`
	AgyFullAccess   bool `json:"agy_full_access,omitempty"` // owner opt-in; runtime only, reset on daemon restart
}

type ProjectStatus string

const (
	ProjectActive   ProjectStatus = "active"
	ProjectPaused   ProjectStatus = "paused"
	ProjectArchived ProjectStatus = "archived"
)

type Project struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Slug      string        `json:"slug"`
	Folder    string        `json:"folder"`
	Brief     string        `json:"brief"`
	LeadID    string        `json:"lead_id"`
	Status    ProjectStatus `json:"status"`
	CreatedAt int64         `json:"created_at"`
	UpdatedAt int64         `json:"updated_at"`
}

type MessageState string

const (
	MsgQueued    MessageState = "queued"
	MsgDelivered MessageState = "delivered"
	MsgRead      MessageState = "read"
	MsgFailed    MessageState = "failed"
)

type Message struct {
	RetryID     string       `json:"retry_id,omitempty"`
	RetryHash   string       `json:"-"`
	Receipt     string       `json:"receipt,omitempty"` // queued, dispatching, accepted, submitted, unconfirmed, read, owner
	ReceiptAt   int64        `json:"receipt_at,omitempty"`
	ID          string       `json:"id"`
	From        string       `json:"from"` // agent ID or "owner"
	To          string       `json:"to"`   // agent ID or "owner"
	Body        string       `json:"body"`
	State       MessageState `json:"state"`
	ReplyTo     string       `json:"reply_to,omitempty"`
	CreatedAt   int64        `json:"created_at"`
	DeliveredAt int64        `json:"delivered_at,omitempty"`
	// ThreadID: the board topic of mail to or from the head ("" = none given).
	ThreadID string `json:"thread_id,omitempty"`
}

type ApprovalKind string

const (
	ApprovalPermission ApprovalKind = "permission"
	ApprovalQuestion   ApprovalKind = "question"
	ApprovalGuard      ApprovalKind = "guard" // a tool call the dangerous-command guard held; Input is a GuardHold
	// ApprovalBrake: an agent was paused by a resource brake; decisions resume | raise | deny.
	ApprovalBrake ApprovalKind = "brake"
	// ApprovalCompanion: about a task for Companion (DESIGN.md "Companion"). Tool
	// ToolCompanionTask: an agent wants to hand a task over (allow | deny; Input is a
	// CompanionApprovalInput with the exact text). Tool ToolCompanionMessage: the head's
	// message to Companion reads like it asks Companion to act (allow | deny; the exact text,
	// as for a task). Tool ToolCompanionQuestion: Companion asks about a task (answer | deny =
	// cancel the task).
	ApprovalCompanion ApprovalKind = "companion"
)

// Tools of companion approvals.
const (
	ToolCompanionTask     = "companion_task"
	ToolCompanionMessage  = "companion_message"
	ToolCompanionQuestion = "companion_question"
)

// GuardHold is the Input of an Approval of kind guard, and meta["guard"] of the timeline
// entries the guard writes (held, decided, blocked).
type GuardHold struct {
	Outcome   string          `json:"outcome"` // hold | block
	Rule      string          `json:"rule"`
	RuleName  string          `json:"rule_name"`
	Why       string          `json:"why"`
	Detail    string          `json:"detail"`
	Match     string          `json:"match"`   // the part of the command that matched
	Command   string          `json:"command"` // the whole command, or the file path for file tools
	Tool      string          `json:"tool"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
}

type ApprovalState string

const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalAllowed  ApprovalState = "allowed"
	ApprovalDenied   ApprovalState = "denied"
	ApprovalAnswered ApprovalState = "answered"
	ApprovalExpired  ApprovalState = "expired"
)

// ToolTerminalMenu is Approval.Tool for a menu read off a terminal agent's screen (a
// permission prompt, question or dialog of a harness TUI). Question holds the dialog text,
// Options its choices; the answer is typed as keys.
const ToolTerminalMenu = "terminal_menu"

type Approval struct {
	Deferred    bool            `json:"deferred,omitempty"`
	AnswerID    string          `json:"answer_id,omitempty"`
	Version     int64           `json:"version"`
	Card        *OwnerCard      `json:"card,omitempty"`
	ID          string          `json:"id"`
	AgentID     string          `json:"agent_id"`
	Kind        ApprovalKind    `json:"kind"`
	Tool        string          `json:"tool"`
	Input       json.RawMessage `json:"input,omitempty"`
	Description string          `json:"description"`
	Question    string          `json:"question"`
	Options     []string        `json:"options"`
	State       ApprovalState   `json:"state"`
	Answer      string          `json:"answer"`
	CreatedAt   int64           `json:"created_at"`
	DecidedAt   int64           `json:"decided_at,omitempty"`
	// ThreadID: the board topic a head's question was asked in (its answer goes there).
	ThreadID string `json:"thread_id,omitempty"`
}

type EntryKind string

const (
	EntryUser       EntryKind = "user"
	EntryAssistant  EntryKind = "assistant"
	EntryThinking   EntryKind = "thinking"
	EntryToolUse    EntryKind = "tool_use"
	EntryToolResult EntryKind = "tool_result"
	EntrySystem     EntryKind = "system"
	EntryResult     EntryKind = "result"
	EntryMessageIn  EntryKind = "message_in"
	EntryError      EntryKind = "error"
)

type TimelineEntry struct {
	Seq       int64           `json:"seq"`
	AgentID   string          `json:"agent_id"`
	At        int64           `json:"at"`
	Kind      EntryKind       `json:"kind"`
	Text      string          `json:"text"`
	Tool      string          `json:"tool,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Meta      map[string]any  `json:"meta,omitempty"`
	// ThreadID: the board topic (DESIGN.md "Board") of a head's entry; "" = general.
	ThreadID string `json:"thread_id,omitempty"`
}

type LevelPolicy struct {
	Model             string   `json:"model"`
	AllowedModels     []string `json:"allowed_models"`
	PermissionMode    string   `json:"permission_mode"`
	CanCreateProjects bool     `json:"can_create_projects"`
	CanLaunch         []Level  `json:"can_launch"`
}

type Policy struct {
	Levels map[Level]LevelPolicy `json:"levels"`
}

// DefaultPolicy: head on Opus, everyone below on Sonnet (owner's rule, 21 Oct 2026).
func DefaultPolicy() Policy {
	return Policy{Levels: map[Level]LevelPolicy{
		LevelHead: {Model: "claude-opus-5-5", AllowedModels: []string{"claude-opus-5-5"},
			PermissionMode: "auto", CanCreateProjects: true, CanLaunch: []Level{LevelLead, LevelWorker}},
		LevelLead: {Model: "claude-opus-5-5", AllowedModels: []string{"claude-opus-5-5", "claude-sonnet-5"},
			PermissionMode: "auto", CanLaunch: []Level{LevelWorker}},
		LevelWorker: {Model: "claude-sonnet-5", AllowedModels: []string{"claude-sonnet-5", "claude-opus-5-5", "claude-haiku-4-5-20251001"},
			PermissionMode: "auto"},
	}}
}

type Snapshot struct {
	RollbackWarning string     `json:"rollback_warning,omitempty"`
	Machine         string     `json:"machine"`
	Version         string     `json:"version"`
	Head            *Agent     `json:"head"`
	Projects        []Project  `json:"projects"`
	Agents          []Agent    `json:"agents"`
	Approvals       []Approval `json:"approvals"`
	Policy          Policy     `json:"policy"` // derived from the built-in profiles
	Profiles        []Profile  `json:"profiles"`
	Workspace       string     `json:"workspace"`
}

// Bus event types (WebSocket /api/events).
const (
	EvSnapshot         = "snapshot"
	EvAgentUpdated     = "agent.updated"
	EvAgentRemoved     = "agent.removed"
	EvProjectUpdated   = "project.updated"
	EvProjectRemoved   = "project.removed"
	EvTimelineAppended = "timeline.appended"
	EvApprovalCreated  = "approval.created"
	EvApprovalResolved = "approval.resolved"
	EvMessageCreated   = "message.created"
	EvMessageUpdated   = "message.updated"
	EvResourcesSampled = "resources.sampled" // data: resources.Sample (every few seconds)
	EvSlotsUpdated     = "slots.updated"     // data: slots.View
	EvCompanionTask    = "companion.task"    // data: CompanionTask (new or changed)
	EvThreadUpdated    = "thread.updated"    // data: Thread (new or changed)
	EvBoardChanged     = "board.changed"     // head turns moved between threads; data: {"thread_id"}
	EvThreadRemoved    = "thread.removed"    // a topic was deleted; data: {"id"}
	EvThreadEntry      = "thread.entry"      // a line was added to a thread's log; data: ThreadEntry
	EvThreadLink       = "thread.link"       // a link was attached or ended (the Matrix); data: ThreadLink
)

type BusEvent struct {
	Type      string `json:"type"`
	AgentID   string `json:"agent_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Data      any    `json:"data"`
	At        int64  `json:"at"`
}

var nameRx = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// ValidName reports whether s is a valid agent name.
func ValidName(s string) bool { return nameRx.MatchString(s) }

// Now returns the current time in Unix milliseconds.
func Now() int64 { return time.Now().UnixMilli() }

const idAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"

// NewID returns a 10-character random lowercase ID.
func NewID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

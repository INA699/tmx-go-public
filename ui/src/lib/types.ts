// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

// Mirror of internal/model/model.go. JSON names are the contract (see ARCHITECTURE.md); keep in sync.
// Times are Unix milliseconds.

export type Level = "head" | "lead" | "worker";
export type Harness = "claude" | "codex" | "opencode" | "agy" | "antigravity" | "pi" | "shell";
export type Mode = "chat" | "terminal";
export type AgentState = "starting" | "working" | "idle" | "waiting" | "stopped" | "exited" | "error" | "queued" | "paused";

/** The From/To value for the human. */
export const OWNER = "owner";

export interface Agent {
	/** Owner opt-in for this daemon run; never a profile default. */
	agy_full_access?: boolean;
  id: string;
  name: string;
  project_id?: string;
  parent_id?: string;
  level: Level;
  /** profile slug; "" only in data from before profiles */
  profile: string;
  harness: Harness;
  mode: Mode;
  model: string;
  cwd: string;
  role: string;
  brief: string;
  state: AgentState;
  desired: "running" | "stopped" | string;
  session_id: string;
  pid: number;
  created_at: number;
  updated_at: number;
  last_activity_at: number;
  shelved_at?: number;
  done_at?: number;
  protected?: boolean;
  turns: number;
  error: string;
  queued: number;
  // Resource control (ARCHITECTURE.md "Resources"): state queued / paused.
  queue_pos?: number;
  queue_reason?: string;
  paused_reason?: "write_budget" | "memory" | "disk" | string;
  paused_detail?: string;
  paused_at?: number;
  // Live mode switch (ARCHITECTURE.md "Switching live"): chat ⇄ the harness's own screen.
  /** set while switched: the mode it was launched in (a daemon restart brings it back) */
  home_mode?: Mode;
  /** it switches to this mode when its current turn ends */
  pending_mode?: Mode;
}

export type ProjectStatus = "active" | "paused" | "archived";

export interface Project {
  id: string;
  name: string;
  slug: string;
  folder: string;
  brief: string;
  lead_id: string;
  status: ProjectStatus;
  created_at: number;
  updated_at: number;
}

export type MessageState = "queued" | "delivered" | "read" | "failed";

export interface Message {
  retry_id?: string;
  receipt?: string;
  receipt_at?: number;
  id: string;
  from: string;
  to: string;
  body: string;
  state: MessageState;
  reply_to?: string;
  created_at: number;
  delivered_at?: number;
  /** mail to or from the head: its board thread */
  thread_id?: string;
}

export type ApprovalKind = "permission" | "question" | "guard" | "brake" | "companion" | "whatsapp";
export type ApprovalState = "pending" | "allowed" | "denied" | "answered" | "expired";

export interface Approval {
  deferred?: boolean;
  answer_id?: string;
  version?: number;
  card?: {
    type: "decision" | "human_action";
    context?: string;
    category?: string;
    recommendation?: string;
    action?: { goal: string; steps: string[]; account?: string; success: string; do_not_touch?: string[]; effects: "none" | "money" | "outward" | "account" };
  };
  id: string;
  agent_id: string;
  kind: ApprovalKind;
  tool: string;
  /** Raw JSON in Go; arrives here already parsed. */
  input?: unknown;
  description: string;
  question: string;
  options: string[] | null;
  state: ApprovalState;
  answer: string;
  created_at: number;
  decided_at?: number;
  /** a head's question: the board thread it was asked in */
  thread_id?: string;
}

export type EntryKind =
  | "user"
  | "assistant"
  | "thinking"
  | "tool_use"
  | "tool_result"
  | "system"
  | "result"
  | "message_in"
  | "error";

export interface TimelineEntry {
  seq: number;
  agent_id: string;
  at: number;
  kind: EntryKind;
  text: string;
  tool?: string;
  tool_use_id?: string;
  input?: unknown;
  is_error?: boolean;
  meta?: Record<string, unknown>;
  /** the head's entries: the board thread (ARCHITECTURE.md "Board") */
  thread_id?: string;
}

export interface LevelPolicy {
  model: string;
  allowed_models: string[] | null;
  permission_mode: string;
  can_create_projects: boolean;
  can_launch: Level[] | null;
}

export interface Policy {
  levels: Partial<Record<Level, LevelPolicy>>;
}

/** An agent type (ARCHITECTURE.md "Profiles"). Built-ins (head, lead, worker) keep their level and can only be reset. */
export interface Profile {
  slug: string;
  name: string;
  description: string;
  level: Level;
  /** template: {{name}} {{project}} {{folder}} … (see lib/profiles.ts TEMPLATE_VARS) */
  instructions: string;
  model: string;
  allowed_models: string[] | null;
  permission_mode: string;
  allowed_tools: string[] | null;
  disallowed_tools: string[] | null;
  read_only: boolean;
  can_create_projects: boolean;
  /** profile slugs */
  can_launch: string[] | null;
  /** may hand tasks to Companion (MCP companion_task); every hand-off still waits for the owner's OK */
  companion_tasks?: boolean;
  built_in: boolean;
  /** a built-in that differs from its default */
  modified: boolean;
  created_at: number;
  updated_at: number;
}

export interface Snapshot {
  machine: string;
  version: string;
  head: Agent | null;
  projects: Project[] | null;
  agents: Agent[] | null;
  approvals: Approval[] | null;
  policy: Policy;
  profiles?: Profile[] | null;
  workspace: string;
}

export type BusEventType =
  | "snapshot"
  | "agent.updated"
  | "agent.removed"
  | "project.updated"
  | "project.removed"
  | "timeline.appended"
  | "approval.created"
  | "approval.resolved"
  | "message.created"
  | "message.updated"
  | "profile.updated"
  | "profile.removed"
  | "resources.sampled"
  | "slots.updated"
  | "thread.updated"
  | "thread.removed"
  | "thread.entry"
  | "board.changed"
  | "recording.updated"
  | "recording.removed"
  | "summary.updated";

export interface BusEvent {
  type: BusEventType | string;
  agent_id?: string;
  project_id?: string;
  data: unknown;
  at: number;
}

export interface Health {
  ok: boolean;
  version: string;
  machine: string;
}

// Request bodies (ARCHITECTURE.md API table).
export interface CreateProjectBody {
  name: string;
  /** harness used to start the project lead; omitted defaults to Claude */
  harness?: Harness;
  folder?: string;
  brief?: string;
  /** lead profile; default "lead" */
  profile?: string;
}
export interface PatchProjectBody {
  name?: string;
  brief?: string;
  status?: ProjectStatus;
}
export interface CreateAgentBody {
  agy_full_access?: boolean;
  name: string;
  project_id?: string;
  parent_id?: string;
  level?: Level;
  profile?: string;
  harness?: Harness;
  mode?: Mode;
  model?: string;
  cwd?: string;
  role?: string;
  brief?: string;
  prompt?: string;
}
/** PATCH /api/agents/{id}: applies at the agent's next start. */
export interface PatchAgentBody {
  protected?: boolean;
  role?: string;
  brief?: string;
  /** "" = the profile's default */
  model?: string;
  profile?: string;
}
/** POST /api/agents/{id}/mode */
export interface ModeChange {
  agent: Agent;
  /** it switches when its current turn ends */
  pending: boolean;
  note: string;
}
export interface AgentUpdate {
  agent: Agent;
  changed: boolean;
  restart_needed: boolean;
  note: string;
}
export interface DecideBody {
  decision: "allow" | "deny" | "answer" | "resume" | "raise" | "defer" | "reask";
  version?: number;
  answer_id?: string;
  free_text?: boolean;
  message?: string;
  answer?: string;
}

// ---- Safety (ARCHITECTURE.md "Safety") ------------------------------------------------------------

/** Input of an Approval of kind "guard"; also meta.guard of the guard's timeline entries. */
export interface GuardHold {
  outcome: "hold" | "block";
  rule: string;
  rule_name: string;
  why: string;
  detail: string;
  /** the part of the command that matched */
  match: string;
  /** the whole command, or the file path for file tools */
  command: string;
  tool: string;
  tool_input?: unknown;
  cwd?: string;
}

export interface GuardRule {
  id: string;
  outcome: "hold" | "block";
  name: string;
  summary: string;
  why: string;
  examples: string[];
}

export interface GuardSettings {
  enabled: boolean;
  /** kinds of agents the guard is off for: levels today, agent profiles later */
  off_for: string[];
}

export interface GateStatus {
  enabled: boolean;
  available: boolean;
  method: string;
  method_label: string;
  unavailable?: string;
  changed_at?: number;
  gated: string[];
  /** the gate's state file; missing state keeps the gate on */
  file?: string;
  recovery?: string;
  /** the grace window: no prompt for this long after a proof (0 = off; absent = an older daemon) */
  grace_minutes?: number;
  /** when the open window ends (Unix ms); absent = locked. In the daemon's memory only. */
  grace_until?: number;
  /** the lengths the dial offers, in minutes */
  grace_steps?: number[];
}

export interface SafetyInfo {
  guard: GuardSettings;
  rules: GuardRule[];
  gate: GateStatus;
  levels: Level[];
}

/** GET /api/gate/pin: never the PIN or its hash. */
export interface PinStatus {
  available: boolean;
  set: boolean;
  at_machine?: boolean;
  wait_s?: number;
  error?: string;
}

export type ConfirmationState = "pending" | "confirmed" | "failed" | "used";

/** A proof of the owner's presence the daemon is collecting (428 "owner confirmation needed"). */
export interface Confirmation {
  id: string;
  reason: string;
  state: ConfirmationState;
  error?: string;
  method: string;
  method_label: string;
  created_at: number;
  expires_at?: number;
}

// ---- Harnesses, discover and adopt (ARCHITECTURE.md "Harnesses") ---------------------------------

/** GET/PUT /api/config: the part of config.json Settings edits. */
export interface ConfigView {
  head_harness: string;
}

/** GET /api/harnesses: what each harness is and whether it is installed here. */
export interface HarnessInfo {
  name: Harness;
  label: string;
  install?: string;
  /** modes it runs in; the first is its default */
  modes: Mode[];
  installed: boolean;
  path?: string;
  /** it has a chat mode (a machine-readable protocol), not only a terminal */
  structured: boolean;
}

/** Input of an Approval with tool "terminal_menu": a menu read off a terminal agent's screen. */
export interface TerminalMenu {
  harness: Harness;
  /** approval | question | trust | locked | dialog */
  screen: string;
  /** the highlighted option's n; 0 = none */
  selected: number;
  choices: { n: number; label: string }[] | null;
}

export interface Conversation {
  id: string;
  started: number;
  updated: number;
  preview: string;
}

/** GET /api/discover: an agent process running on this machine outside tmx. */
export interface DiscoveredAgent {
  pid: number;
  harness: Harness;
  cwd: string;
  command: string;
  started_at: number;
  interactive: boolean;
  adoptable: boolean;
  /** why it cannot be adopted */
  reason?: string;
  conversation_id?: string;
  conversation_source: string;
  candidates: Conversation[] | null;
}

/** POST /api/adopt: stops that pid (SIGTERM, owner gate) and resumes its conversation under tmx. */
export interface AdoptBody {
  pid: number;
  name?: string;
  project_id?: string;
  level?: Level;
  conversation_id?: string;
  confirm: boolean;
}

// ---------------------------------------------------------------------------------------------
// The board (ARCHITECTURE.md "Board"): the head's turns as cards on threads (topics).

export type ThreadKind = "general" | "project" | "topic" | "chat" | "intent";

export interface Thread {
  id: string;
  title: string;
  kind: ThreadKind;
  project_id?: string;
  chat_id?: string;
  color?: string;
  archived?: boolean;
  /** the owner's urgency order: 1 = most urgent (left); absent = not placed */
  position?: number;
  created_at: number;
  updated_at: number;
  // The Matrix (ARCHITECTURE.md "Matrix")
  parent_id?: string;
  /** absent = active */
  status?: ThreadStatus;
  /** absent = task */
  lifecycle?: "task" | "ongoing";
  /** agent ID, usually a lead */
  owner?: string;
  story?: string;
  intent_text?: string;
  intent_source?: string;
  canvas_x?: number;
  canvas_y?: number;
}

export type ThreadStatus = "active" | "paused" | "waiting_on_owner" | "blocked" | "done";

export type LinkAxis =
  | "agent"
  | "shell"
  | "tool"
  | "memory"
  | "doc"
  | "file"
  | "path"
  | "key"
  | "secret"
  | "rule"
  | "person"
  | "story"
  | "project"
  | "machine";

/** Something attached to a thread; a key or secret is only ever its name. */
export interface ThreadLink {
  id: string;
  thread_id: string;
  axis: LinkAxis;
  ref: string;
  role?: string;
  label?: string;
  why?: string;
  by: string;
  how: "auto" | "hand";
  at: number;
  /** set once detached; the link stays as history */
  ended_at?: number;
}

/** One head turn, summarised; id is the seq of its first entry. */
export interface BoardCard {
  id: number;
  thread_id: string;
  /** seq of its last entry */
  to: number;
  at: number;
  updated: number;
  kind: "owner" | "mail" | "chat" | "start";
  /** "owner", the agent's name, or (chat) who wrote in the chat */
  sender: string;
  prompt: string;
  reply?: string;
  notes?: string[];
  asked?: string[];
  launched?: string[];
  projects?: string[];
  sent_to?: string[];
  tools: number;
  done: boolean;
  error?: boolean;
}

export type LogKind = "decision" | "action" | "info" | "link" | EventKind;

/** Events agents append to a thread (the Matrix); threads only, chat topics keep the four above. */
export type EventKind = "result" | "blocker" | "handoff" | "ask" | "start" | "pause" | "done";

/** A line in a thread's log: append-only; a correction names the entry it replaces. */
export interface ThreadEntry {
  id: string;
  thread_id: string;
  kind: LogKind;
  text: string;
  link?: string;
  /** the card (head turn) it came from */
  card?: number;
  /** "owner" or the agent's name */
  by: string;
  by_id?: string;
  at: number;
  corrects?: string;
}

/** Threads come in board order (the owner's urgency order when `ordered`). */
export interface Board {
  head_id: string;
  threads: Thread[];
  ordered: boolean;
  cards: BoardCard[];
  entries: ThreadEntry[];
}

// ---------------------------------------------------------------------------------------------
// Voice transcripts (ARCHITECTURE.md "Voice"): what whisper made of a recording's audio, on this machine.

export type TranscriptState = "queued" | "running" | "done" | "failed";

/** One stretch of speech, in milliseconds from the start of the audio. */
export interface TranscriptSegment {
  from_ms: number;
  to_ms: number;
  text: string;
}

/** GET /api/transcripts/{id}; the "transcript.updated" event carries one. */
export interface Transcript {
  id: string;
  /** what was transcribed, e.g. "recording:<id>/<segment>" */
  source: string;
  state: TranscriptState;
  model: string;
  language: string;
  text: string;
  segments: TranscriptSegment[];
  error: string;
  created_at: number;
  updated_at: number;
}

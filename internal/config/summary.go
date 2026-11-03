// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package config

// SummaryConfig is config.json "summaries": Claude's summaries of finished recordings (ARCHITECTURE.md
// "Summaries"). Every field is optional; internal/summarize fills the defaults.
type SummaryConfig struct {
	// Auto summarizes every finished recording by itself; nil = on.
	Auto *bool `json:"auto,omitempty"`
	// Model is the Claude model that writes them ("" = the cheapest Haiku tmx knows).
	Model string `json:"model,omitempty"`
	// Backend: "" or "local" = the local model on this machine (recordings are client content: never a cloud
	// API, the owner 3 Nov); "claude" = Claude Code.
	Backend string `json:"backend,omitempty"`
	// LocalModel is the Ollama model ("" = gemma4:e4b); LocalURL its loopback address ("" = http://127.0.0.1:11434).
	LocalModel string `json:"local_model,omitempty"`
	LocalURL   string `json:"local_url,omitempty"`
}

// AutoOn reports config summaries.auto (default on).
func (c SummaryConfig) AutoOn() bool { return c.Auto == nil || *c.Auto }

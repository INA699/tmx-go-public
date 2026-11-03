// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package harness

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/runner"
	"github.com/blackcode-ch/tmx-go/internal/runner/term/screen"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAgyLaunchAndAlias(t *testing.T) {
	for _, h := range []string{"agy", "antigravity"} {
		info, ok := Get(model.Harness(h))
		if !ok || info.Name != "agy" {
			t.Fatal(h)
		}
		c, err := TerminalCommand(runner.Spec{Harness: model.Harness(h), Command: "/test/agy", InitialPrompt: "[tmx instructions]\nhello", Model: "gemini", SessionID: "existing"})
		want := []string{"--mode", "auto", "--model", "gemini", "-i", "[tmx instructions]\nhello"}
		if err != nil || !slices.Equal(c.Args, want) {
			t.Fatalf("%v %v", c.Args, err)
		}
	}
	c, err := TerminalCommand(runner.Spec{Harness: "agy", Command: "/test/agy", AgyFullAccess: true})
	if err != nil || !slices.Equal(c.Args, []string{"--dangerously-skip-permissions"}) {
		t.Fatalf("owner opt-in: %v %v", c.Args, err)
	}
	if _, err := TerminalCommand(runner.Spec{Harness: "agy", Command: "/test/agy", PermissionMode: "plan", AgyFullAccess: true}); err == nil {
		t.Fatal("read-only accepted")
	}
}

func TestAgyScreensAndNumericAnswers(t *testing.T) {
	info, _ := Get("agy")
	for _, tc := range []struct {
		text  string
		state screen.State
	}{
		{"Thought for 12s\n╭────────╮\n│ >      │\n╰────────╯\n? for shortcuts", screen.Idle},
		{"╰────────╯\nUsing read_file\n? for shortcuts", screen.Working},
		{"> earlier prompt\nThought for 12s\nUsing read_file", screen.Working},
		{">", screen.Working},
		{"? for shortcuts\nAllow creation of this file?\n> 1. Yes, allow access\n  2. No, deny access", screen.Approval},
		{"Allow creation of this file?\n> 1. Yes, allow access\n  2. No, deny access", screen.Approval},
	} {
		if d := screen.Classify(strings.Split(tc.text, "\n"), info.Patterns, time.Minute); d.State != tc.state {
			t.Fatalf("%q: %s", tc.text, d.State)
		}
	}
	d := screen.Classify(strings.Split("Allow creation of this file?\n> 1. Yes, allow access\n  2. No, deny access", "\n"), info.Patterns, 0)
	for choice, want := range map[string]string{"approve": "1 enter", "deny": "2 enter"} {
		got, err := screen.PlanAnswer(d, choice, info.Synonyms)
		if err != nil || got != want {
			t.Fatalf("%s: %q %v", choice, got, err)
		}
	}
}

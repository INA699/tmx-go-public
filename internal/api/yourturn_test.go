// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package api_test

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"github.com/blackcode-ch/tmx-go/internal/model"
)

// A real HTTP server and fresh database; never uses a live daemon or owner state.
func TestYourTurnThrowawayCardJourney(t *testing.T) {
	d := apitest.New(t)
	owner := client{t: t, base: d.URL, token: d.OwnerToken}
	asker := client{t: t, base: d.URL, token: d.AgentToken(t, "head")}
	var card model.Approval
	if s := asker.do("POST", "/api/approvals", map[string]any{"question": "Throwaway: choose a plan"}, &card); s != 201 {
		t.Fatalf("create: %d", s)
	}
	var listed []model.Approval
	if s := owner.do("GET", "/api/approvals", nil, &listed); s != 200 || len(listed) != 1 || listed[0].ID != card.ID {
		t.Fatalf("list: %d %+v", s, listed)
	}
	t.Log("throwaway card created and listed")
	words := "Keep my typed answer\nand dictated second line"
	draft := map[string]any{"decision": "answer", "answer": words, "free_text": true, "version": card.Version, "answer_id": "draft"}
	if s := asker.do("POST", "/api/approvals", map[string]any{"id": card.ID, "question": "Throwaway: changed plan"}, &card); s != 201 {
		t.Fatalf("change: %d", s)
	}
	if s := owner.do("POST", "/api/approvals/"+card.ID, draft, nil); s != 409 {
		t.Fatalf("stale answer: %d", s)
	}
	draft["version"], draft["answer_id"] = card.Version, "reviewed"
	for range 2 {
		var result model.Approval
		if s := owner.do("POST", "/api/approvals/"+card.ID, draft, &result); s != 200 || result.Answer != words {
			t.Fatalf("answer/retry: %d %+v", s, result)
		}
	}
	t.Log("changed card rejected stale decision; reviewed words answered and retried intact")
	draft["answer"], draft["answer_id"] = "Late dictation after closure", "late"
	for range 2 {
		var result model.Approval
		if s := owner.do("POST", "/api/approvals/"+card.ID, draft, &result); s != 200 || result.Answer != words {
			t.Fatalf("late correspondence: %d %+v", s, result)
		}
	}
	if s := asker.do("POST", "/api/approvals/"+card.ID, draft, nil); s != 403 {
		t.Fatalf("agent answer: %d", s)
	}
	var mail []model.Message
	if s := asker.do("GET", "/api/messages", nil, &mail); s != 200 {
		t.Fatalf("mail: %d", s)
	}
	answers, late := 0, 0
	for _, m := range mail {
		if strings.Contains(m.Body, words) {
			answers++
		}
		if strings.Contains(m.Body, "Late dictation after closure") && strings.Contains(m.Body, "not a new decision") {
			late++
		}
	}
	if answers != 1 || late != 1 {
		t.Fatalf("lost or duplicate words: answers=%d late=%d", answers, late)
	}
	t.Log("late dictated words delivered once as correspondence; original decision intact; agent forbidden")
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package store

import (
	"github.com/blackcode-ch/tmx-go/internal/model"
	"path/filepath"
	"testing"
)

func TestCardAnswerRollback(t *testing.T) {
	s := openTest(t)
	a := model.Approval{ID: "card", AgentID: "asker", Kind: model.ApprovalQuestion, State: model.ApprovalPending, Version: 1}
	if err := s.PutApproval(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_card_receipt BEFORE INSERT ON card_answers BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	answer := a
	answer.State = model.ApprovalAnswered
	answer.AnswerID = "request"
	m := model.Message{ID: "mail", From: model.Owner, To: "asker", Body: "answer", State: model.MsgQueued}
	if err := s.CommitCardAnswer(a, answer, "request", "{}", &m); err == nil {
		t.Fatal("expected failure")
	}
	got, err := s.GetApproval(a.ID)
	if err != nil || got.State != model.ApprovalPending || got.AnswerID != "" {
		t.Fatalf("closed without receipt: %+v %v", got, err)
	}
	if _, err := s.GetMessage(m.ID); err != ErrNotFound {
		t.Fatalf("mail escaped rollback: %v", err)
	}
}

func TestCardAnswerReceiptSurvivesDatabaseReopen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMXG_HOME", home)
	path := filepath.Join(home, "cards.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Approval{ID: "card", AgentID: "asker", Kind: model.ApprovalQuestion, State: model.ApprovalPending, Version: 2}
	if err := s.PutApproval(a); err != nil {
		t.Fatal(err)
	}
	answer := a
	answer.State, answer.Answer, answer.AnswerID = model.ApprovalAnswered, "yes", "stable-request"
	m := model.Message{ID: "queued-answer", From: model.Owner, To: "asker", Body: "yes", State: model.MsgQueued}
	if err := s.CommitCardAnswer(a, answer, answer.AnswerID, "request-body", &m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	request, result, messageID, err := s.CardAnswer(a.ID, answer.AnswerID)
	if err != nil || request != "request-body" || result.AnswerID != answer.AnswerID || messageID != m.ID {
		t.Fatalf("lost receipt: %s %+v %s %v", request, result, messageID, err)
	}
	mail, err := s.GetMessage(messageID)
	if err != nil || mail.State != model.MsgQueued {
		t.Fatalf("lost queued answer: %+v %v", mail, err)
	}
}

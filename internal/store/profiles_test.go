// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

func TestProfilesCRUD(t *testing.T) {
	s := openTest(t)
	p := model.DefaultProfiles()[3]
	p.CreatedAt, p.UpdatedAt, p.BuiltIn = 5, 6, true
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	p.Name, p.UpdatedAt = "Code reviewer", 7
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListProfiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	got := list[0]
	if !got.SameSettings(p) || got.CreatedAt != 5 || got.UpdatedAt != 7 || got.BuiltIn {
		t.Fatalf("got %+v", got)
	}
	if err := s.DeleteProfile(p.Slug); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListProfiles(); len(list) != 0 {
		t.Fatal("not deleted")
	}
}

// A database from before profiles (schema 1) gets the column, and its agents the built-in
// profile of their level.
func TestMigrationGivesAgentsTheirLevelProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmx.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		migrations[0],
		`INSERT INTO schema_version(version) VALUES (1)`,
		`INSERT INTO agents (id, name, level, harness, mode, state, desired, created_at, updated_at)
			VALUES ('a1', 'head', 'head', 'claude', 'chat', 'idle', 'running', 1, 1),
			       ('a2', 'w', 'worker', 'claude', 'chat', 'idle', 'running', 2, 2)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, err := s.ListAgents()
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if list[0].Profile != "head" || list[1].Profile != "worker" {
		t.Fatalf("profiles %q %q", list[0].Profile, list[1].Profile)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package store

import (
	"encoding/json"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

// Profiles are stored as JSON (the settings change more often than the schema should).

// ListProfiles returns every stored profile, in slug order.
func (s *Store) ListProfiles() ([]model.Profile, error) {
	rows, err := s.db.Query(`SELECT data, created_at, updated_at FROM profiles ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Profile
	for rows.Next() {
		var data string
		var p model.Profile
		var created, updated int64
		if err := rows.Scan(&data, &created, &updated); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = created, updated
		out = append(out, p)
	}
	return out, rows.Err()
}

// PutProfile inserts or replaces a profile (by slug).
func (s *Store) PutProfile(p model.Profile) error {
	p.BuiltIn, p.Modified = false, false
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO profiles (slug, data, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		p.Slug, string(b), p.CreatedAt, p.UpdatedAt)
	return err
}

// DeleteProfile removes a profile.
func (s *Store) DeleteProfile(slug string) error {
	_, err := s.db.Exec(`DELETE FROM profiles WHERE slug = ?`, slug)
	return err
}

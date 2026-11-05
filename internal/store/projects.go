package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/blackcode-ch/tmx-go/internal/model"
)

const projectCols = `id, name, slug, folder, brief, lead_id, status, created_at, updated_at`

func scanProject(r scanner) (model.Project, error) {
	var p model.Project
	err := r.Scan(&p.ID, &p.Name, &p.Slug, &p.Folder, &p.Brief, &p.LeadID, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// PutProject writes a project's fields. The progress_* columns and progress_samples (from a
// removed checklist-progress feature; the migration that added them stays) keep their defaults.
func (s *Store) PutProject(p model.Project) error {
	_, err := s.db.Exec(upsert("projects", projectCols),
		p.ID, p.Name, p.Slug, p.Folder, p.Brief, p.LeadID, p.Status, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) GetProject(id string) (model.Project, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProject(id string) error {
	_, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (s *Store) PutProjectContribution(c model.ProjectContribution) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO project_contributions(project_id,agent_id,data) VALUES (?,?,?)
		ON CONFLICT(project_id,agent_id) DO UPDATE SET data=excluded.data`, c.ProjectID, c.AgentID, string(data))
	return err
}

func (s *Store) ProjectContributions(project string) ([]model.ProjectContribution, error) {
	rows, err := s.db.Query(`SELECT data FROM project_contributions WHERE project_id=? ORDER BY agent_id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.ProjectContribution{}
	for rows.Next() {
		var data string
		var c model.ProjectContribution
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

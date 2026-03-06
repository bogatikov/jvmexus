package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"rootPath"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Module struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Dependency struct {
	ModuleName     string  `json:"moduleName"`
	GroupID        string  `json:"groupId"`
	ArtifactID     string  `json:"artifactId"`
	Version        string  `json:"version,omitempty"`
	Scope          string  `json:"scope"`
	Type           string  `json:"type"`
	BinaryJarPath  string  `json:"binaryJarPath,omitempty"`
	SourceJarPath  string  `json:"sourceJarPath,omitempty"`
	SourceStatus   string  `json:"sourceStatus"`
	ResolutionType string  `json:"resolutionType"`
	Confidence     float64 `json:"confidence"`
}

func (s *Store) UpsertProject(ctx context.Context, name, rootPath string) (Project, error) {
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO projects(name, root_path)
		VALUES(?, ?)
		ON CONFLICT(root_path) DO UPDATE SET
		  name = excluded.name,
		  updated_at = CURRENT_TIMESTAMP
	`, name, rootPath); err != nil {
		return Project{}, fmt.Errorf("upsert project: %w", err)
	}

	var p Project
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, created_at, updated_at
		FROM projects
		WHERE root_path = ?
	`, rootPath).Scan(&p.ID, &p.Name, &p.RootPath, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("load project: %w", err)
	}
	return p, nil
}

func (s *Store) ReplaceModules(ctx context.Context, projectID int64, modules []Module) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace modules: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM modules WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete modules: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO modules(project_id, name, path) VALUES(?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert module: %w", err)
	}
	defer stmt.Close()

	for _, m := range modules {
		if _, err := stmt.ExecContext(ctx, projectID, m.Name, m.Path); err != nil {
			return fmt.Errorf("insert module %s: %w", m.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit modules: %w", err)
	}
	return nil
}

func (s *Store) ReplaceDependencies(ctx context.Context, projectID int64, dependencies []Dependency) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace deps: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM dependencies WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete deps: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO dependencies(
			project_id, module_name, group_id, artifact_id, version, scope, dep_type,
			binary_jar_path, source_jar_path, source_status, resolution_type, confidence
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert dep: %w", err)
	}
	defer stmt.Close()

	for _, dep := range dependencies {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			dep.ModuleName,
			dep.GroupID,
			dep.ArtifactID,
			dep.Version,
			dep.Scope,
			dep.Type,
			nullable(dep.BinaryJarPath),
			nullable(dep.SourceJarPath),
			dep.SourceStatus,
			dep.ResolutionType,
			dep.Confidence,
		); err != nil {
			return fmt.Errorf("insert dep %s:%s: %w", dep.GroupID, dep.ArtifactID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit deps: %w", err)
	}
	return nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, root_path, created_at, updated_at
		FROM projects
		ORDER BY updated_at DESC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.RootPath, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (s *Store) FindProject(ctx context.Context, nameOrPath string) (Project, error) {
	var p Project
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, created_at, updated_at
		FROM projects
		WHERE name = ? OR root_path = ?
		ORDER BY updated_at DESC
		LIMIT 1
	`, nameOrPath, nameOrPath).Scan(&p.ID, &p.Name, &p.RootPath, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return Project{}, fmt.Errorf("project not found: %s", nameOrPath)
		}
		return Project{}, fmt.Errorf("find project: %w", err)
	}
	return p, nil
}

func (s *Store) ListDependenciesByProjectID(ctx context.Context, projectID int64) ([]Dependency, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT module_name, group_id, artifact_id, ifnull(version,''), scope, dep_type,
		       ifnull(binary_jar_path,''), ifnull(source_jar_path,''), source_status, resolution_type, confidence
		FROM dependencies
		WHERE project_id = ?
		ORDER BY module_name ASC, group_id ASC, artifact_id ASC, ifnull(version,'') ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query dependencies: %w", err)
	}
	defer rows.Close()

	var dependencies []Dependency
	for rows.Next() {
		var dep Dependency
		if err := rows.Scan(
			&dep.ModuleName,
			&dep.GroupID,
			&dep.ArtifactID,
			&dep.Version,
			&dep.Scope,
			&dep.Type,
			&dep.BinaryJarPath,
			&dep.SourceJarPath,
			&dep.SourceStatus,
			&dep.ResolutionType,
			&dep.Confidence,
		); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		dependencies = append(dependencies, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dependencies: %w", err)
	}
	return dependencies, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

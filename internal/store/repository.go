package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
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
	Kind           string  `json:"kind"`
	BinaryJarPath  string  `json:"binaryJarPath,omitempty"`
	SourceJarPath  string  `json:"sourceJarPath,omitempty"`
	SourceStatus   string  `json:"sourceStatus"`
	ResolutionType string  `json:"resolutionType"`
	MetadataJSON   string  `json:"metadataJson,omitempty"`
	Confidence     float64 `json:"confidence"`
}

type Symbol struct {
	ID        int64  `json:"id"`
	FilePath  string `json:"filePath"`
	Language  string `json:"language"`
	Name      string `json:"name"`
	FQName    string `json:"fqName"`
	Kind      string `json:"kind"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Signature string `json:"signature,omitempty"`
}

type SymbolReference struct {
	FromName   string  `json:"fromName"`
	FromFile   string  `json:"fromFile"`
	ToName     string  `json:"toName"`
	ToFQName   string  `json:"toFqName,omitempty"`
	RefType    string  `json:"refType"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence,omitempty"`
}

type Chunk struct {
	ID         int64   `json:"id"`
	FilePath   string  `json:"filePath"`
	Language   string  `json:"language"`
	ChunkType  string  `json:"chunkType"`
	ChunkIndex int     `json:"chunkIndex"`
	SymbolName string  `json:"symbolName,omitempty"`
	Text       string  `json:"text"`
	TokenCount int     `json:"tokenCount"`
	Score      float64 `json:"score,omitempty"`
}

type IndexedFile struct {
	FilePath  string `json:"filePath"`
	FileKind  string `json:"fileKind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
	MtimeUnix int64  `json:"mtimeUnix"`
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

func (s *Store) ListModulesByProjectID(ctx context.Context, projectID int64) ([]Module, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, path
		FROM modules
		WHERE project_id = ?
		ORDER BY CASE WHEN name = ':' THEN 0 ELSE 1 END, name ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query modules: %w", err)
	}
	defer rows.Close()

	modules := make([]Module, 0, 16)
	for rows.Next() {
		var module Module
		if err := rows.Scan(&module.Name, &module.Path); err != nil {
			return nil, fmt.Errorf("scan module: %w", err)
		}
		modules = append(modules, module)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate modules: %w", err)
	}
	return modules, nil
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
			project_id, module_name, group_id, artifact_id, version, scope, dep_type, dep_kind,
			binary_jar_path, source_jar_path, source_status, resolution_type, metadata_json, confidence
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			dep.Kind,
			nullable(dep.BinaryJarPath),
			nullable(dep.SourceJarPath),
			dep.SourceStatus,
			dep.ResolutionType,
			nullable(dep.MetadataJSON),
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
	return s.ListDependenciesByProjectIDWithMode(ctx, projectID, false)
}

func (s *Store) ListDependenciesByProjectIDWithMode(ctx context.Context, projectID int64, includeTransitive bool) ([]Dependency, error) {
	query := `
		SELECT module_name, group_id, artifact_id, ifnull(version,''), scope, dep_type, ifnull(dep_kind,'direct'),
		       ifnull(binary_jar_path,''), ifnull(source_jar_path,''), source_status, resolution_type, ifnull(metadata_json,''), confidence
		FROM dependencies
		WHERE project_id = ?`
	args := []any{projectID}
	if !includeTransitive {
		query += ` AND ifnull(dep_kind,'direct') = 'direct'`
	}
	query += ` ORDER BY module_name ASC, scope ASC, group_id ASC, artifact_id ASC, ifnull(version,'') ASC`

	rows, err := s.db.QueryContext(ctx, query, args...)
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
			&dep.Kind,
			&dep.BinaryJarPath,
			&dep.SourceJarPath,
			&dep.SourceStatus,
			&dep.ResolutionType,
			&dep.MetadataJSON,
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

func (s *Store) ReplaceChunks(ctx context.Context, projectID int64, chunks []Chunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace chunks: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chunks(project_id, file_path, language, chunk_type, chunk_index, symbol_name, text, token_count)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert chunks: %w", err)
	}
	defer stmt.Close()

	for _, chunk := range chunks {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			chunk.FilePath,
			chunk.Language,
			chunk.ChunkType,
			chunk.ChunkIndex,
			nullable(chunk.SymbolName),
			chunk.Text,
			chunk.TokenCount,
		); err != nil {
			return fmt.Errorf("insert chunk %s#%d: %w", chunk.FilePath, chunk.ChunkIndex, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chunks: %w", err)
	}
	return nil
}

func (s *Store) InsertChunks(ctx context.Context, projectID int64, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO chunks(project_id, file_path, language, chunk_type, chunk_index, symbol_name, text, token_count)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert chunks: %w", err)
	}
	defer stmt.Close()

	for _, chunk := range chunks {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			chunk.FilePath,
			chunk.Language,
			chunk.ChunkType,
			chunk.ChunkIndex,
			nullable(chunk.SymbolName),
			chunk.Text,
			chunk.TokenCount,
		); err != nil {
			return fmt.Errorf("insert chunk %s#%d: %w", chunk.FilePath, chunk.ChunkIndex, err)
		}
	}
	return nil
}

func (s *Store) DeleteChunksByFilePaths(ctx context.Context, projectID int64, filePaths []string) error {
	return s.deleteByFilePaths(ctx, "chunks", "file_path", projectID, filePaths)
}

func (s *Store) SearchChunks(ctx context.Context, projectID int64, query string, limit int) ([]Chunk, error) {
	if limit <= 0 {
		limit = 10
	}
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.file_path, c.language, c.chunk_type, c.chunk_index, ifnull(c.symbol_name,''), c.text, c.token_count, bm25(chunks_fts) AS score
		FROM chunks_fts
		JOIN chunks c ON c.id = chunks_fts.rowid
		WHERE chunks_fts MATCH ? AND c.project_id = ?
		ORDER BY score ASC
		LIMIT ?
	`, ftsQuery, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("query chunks fts: %w", err)
	}
	defer rows.Close()

	results := make([]Chunk, 0, limit)
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.FilePath, &c.Language, &c.ChunkType, &c.ChunkIndex, &c.SymbolName, &c.Text, &c.TokenCount, &c.Score); err != nil {
			return nil, fmt.Errorf("scan chunk search result: %w", err)
		}
		results = append(results, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chunk search results: %w", err)
	}
	return results, nil
}

var nonTokenRE = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func buildFTSQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	tokens := strings.Fields(raw)
	clean := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = nonTokenRE.ReplaceAllString(token, "")
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		clean = append(clean, token+"*")
	}
	if len(clean) == 0 {
		return ""
	}
	return strings.Join(clean, " OR ")
}

func (s *Store) ReplaceSymbols(ctx context.Context, projectID int64, symbols []Symbol) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace symbols: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM symbols WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete symbols: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO symbols(project_id, file_path, language, name, fq_name, kind, start_line, end_line, signature)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert symbols: %w", err)
	}
	defer stmt.Close()

	for _, sym := range symbols {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			sym.FilePath,
			sym.Language,
			sym.Name,
			sym.FQName,
			sym.Kind,
			sym.StartLine,
			sym.EndLine,
			nullable(sym.Signature),
		); err != nil {
			return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit symbols: %w", err)
	}
	return nil
}

func (s *Store) InsertSymbols(ctx context.Context, projectID int64, symbols []Symbol) error {
	if len(symbols) == 0 {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO symbols(project_id, file_path, language, name, fq_name, kind, start_line, end_line, signature)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert symbols: %w", err)
	}
	defer stmt.Close()

	for _, sym := range symbols {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			sym.FilePath,
			sym.Language,
			sym.Name,
			sym.FQName,
			sym.Kind,
			sym.StartLine,
			sym.EndLine,
			nullable(sym.Signature),
		); err != nil {
			return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
		}
	}
	return nil
}

func (s *Store) DeleteSymbolsByFilePaths(ctx context.Context, projectID int64, filePaths []string) error {
	return s.deleteByFilePaths(ctx, "symbols", "file_path", projectID, filePaths)
}

func (s *Store) ReplaceSymbolReferences(ctx context.Context, projectID int64, refs []SymbolReference) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace refs: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM symbol_refs WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete symbol refs: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO symbol_refs(project_id, from_name, from_file_path, to_name, to_fq_name, ref_type, confidence, evidence)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert refs: %w", err)
	}
	defer stmt.Close()

	for _, ref := range refs {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			ref.FromName,
			ref.FromFile,
			ref.ToName,
			nullable(ref.ToFQName),
			ref.RefType,
			ref.Confidence,
			nullable(ref.Evidence),
		); err != nil {
			return fmt.Errorf("insert symbol ref %s->%s: %w", ref.FromName, ref.ToName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit refs: %w", err)
	}
	return nil
}

func (s *Store) InsertSymbolReferences(ctx context.Context, projectID int64, refs []SymbolReference) error {
	if len(refs) == 0 {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO symbol_refs(project_id, from_name, from_file_path, to_name, to_fq_name, ref_type, confidence, evidence)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert refs: %w", err)
	}
	defer stmt.Close()

	for _, ref := range refs {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			ref.FromName,
			ref.FromFile,
			ref.ToName,
			nullable(ref.ToFQName),
			ref.RefType,
			ref.Confidence,
			nullable(ref.Evidence),
		); err != nil {
			return fmt.Errorf("insert symbol ref %s->%s: %w", ref.FromName, ref.ToName, err)
		}
	}
	return nil
}

func (s *Store) DeleteSymbolReferencesByFromFilePaths(ctx context.Context, projectID int64, filePaths []string) error {
	return s.deleteByFilePaths(ctx, "symbol_refs", "from_file_path", projectID, filePaths)
}

func (s *Store) ListIndexedFilesByProjectID(ctx context.Context, projectID int64) ([]IndexedFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT file_path, file_kind, sha256, size_bytes, mtime_unix
		FROM indexed_files
		WHERE project_id = ?
		ORDER BY file_path ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query indexed files: %w", err)
	}
	defer rows.Close()

	files := make([]IndexedFile, 0, 128)
	for rows.Next() {
		var f IndexedFile
		if err := rows.Scan(&f.FilePath, &f.FileKind, &f.SHA256, &f.SizeBytes, &f.MtimeUnix); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate indexed files: %w", err)
	}
	return files, nil
}

func (s *Store) ReplaceIndexedFiles(ctx context.Context, projectID int64, files []IndexedFile) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx replace indexed files: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM indexed_files WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete indexed files: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO indexed_files(project_id, file_path, file_kind, sha256, size_bytes, mtime_unix)
		VALUES(?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert indexed files: %w", err)
	}
	defer stmt.Close()

	for _, file := range files {
		if _, err := stmt.ExecContext(ctx,
			projectID,
			file.FilePath,
			file.FileKind,
			file.SHA256,
			file.SizeBytes,
			file.MtimeUnix,
		); err != nil {
			return fmt.Errorf("insert indexed file %s: %w", file.FilePath, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit indexed files: %w", err)
	}
	return nil
}

func (s *Store) deleteByFilePaths(ctx context.Context, tableName, columnName string, projectID int64, filePaths []string) error {
	if len(filePaths) == 0 {
		return nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(filePaths)), ",")
	query := fmt.Sprintf("DELETE FROM %s WHERE project_id = ? AND %s IN (%s)", tableName, columnName, placeholders)
	args := make([]any, 0, len(filePaths)+1)
	args = append(args, projectID)
	for _, p := range filePaths {
		args = append(args, p)
	}

	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("delete %s by paths: %w", tableName, err)
	}
	return nil
}

func (s *Store) FindSymbols(ctx context.Context, projectID int64, symbolQuery string, limit int) ([]Symbol, error) {
	return s.FindSymbolsWithFilter(ctx, projectID, symbolQuery, "", limit)
}

func (s *Store) FindSymbolsWithFilter(ctx context.Context, projectID int64, symbolQuery, filePath string, limit int) ([]Symbol, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT id, file_path, language, name, fq_name, kind, start_line, end_line, ifnull(signature,'')
		FROM symbols
		WHERE project_id = ? AND (name = ? OR fq_name = ? OR name LIKE ? OR fq_name LIKE ? OR name LIKE ? OR fq_name LIKE ?)`
	args := []any{projectID, symbolQuery, symbolQuery, symbolQuery + "%", symbolQuery + "%", "%" + symbolQuery + "%", "%" + symbolQuery + "%"}
	if filePath != "" {
		query += ` AND file_path LIKE ?`
		args = append(args, "%"+filePath+"%")
	}
	query += `
		ORDER BY
		CASE
		  WHEN fq_name = ? THEN 0
		  WHEN name = ? THEN 1
		  WHEN fq_name LIKE ? THEN 2
		  WHEN name LIKE ? THEN 3
		  ELSE 4
		END,
		CASE WHEN kind IN ('Type','Interface') THEN 0 ELSE 1 END,
		name ASC
		LIMIT ?`
	args = append(args, symbolQuery, symbolQuery, symbolQuery+"%", symbolQuery+"%", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query symbols: %w", err)
	}
	defer rows.Close()

	result := make([]Symbol, 0, limit)
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.ID, &sym.FilePath, &sym.Language, &sym.Name, &sym.FQName, &sym.Kind, &sym.StartLine, &sym.EndLine, &sym.Signature); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		result = append(result, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate symbols: %w", err)
	}
	return result, nil
}

func (s *Store) ListOutgoingReferences(ctx context.Context, projectID int64, symbol Symbol, limit int) ([]SymbolReference, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_name, from_file_path, to_name, ifnull(to_fq_name,''), ref_type, confidence, ifnull(evidence,'')
		FROM symbol_refs
		WHERE project_id = ? AND (from_name = ? OR from_file_path = ?)
		ORDER BY ref_type ASC, confidence DESC, to_name ASC
		LIMIT ?
	`, projectID, symbol.Name, symbol.FilePath, limit)
	if err != nil {
		return nil, fmt.Errorf("query outgoing refs: %w", err)
	}
	defer rows.Close()

	refs := make([]SymbolReference, 0, limit)
	for rows.Next() {
		var ref SymbolReference
		if err := rows.Scan(&ref.FromName, &ref.FromFile, &ref.ToName, &ref.ToFQName, &ref.RefType, &ref.Confidence, &ref.Evidence); err != nil {
			return nil, fmt.Errorf("scan outgoing ref: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outgoing refs: %w", err)
	}
	return refs, nil
}

func (s *Store) FindSymbolsByExactName(ctx context.Context, projectID int64, nameOrFQName string, limit int) ([]Symbol, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, file_path, language, name, fq_name, kind, start_line, end_line, ifnull(signature,'')
		FROM symbols
		WHERE project_id = ? AND (name = ? OR fq_name = ?)
		ORDER BY CASE WHEN fq_name = ? THEN 0 ELSE 1 END, CASE WHEN kind IN ('Type','Interface') THEN 0 ELSE 1 END, name ASC
		LIMIT ?
	`, projectID, nameOrFQName, nameOrFQName, nameOrFQName, limit)
	if err != nil {
		return nil, fmt.Errorf("query exact symbols: %w", err)
	}
	defer rows.Close()

	result := make([]Symbol, 0, limit)
	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.ID, &sym.FilePath, &sym.Language, &sym.Name, &sym.FQName, &sym.Kind, &sym.StartLine, &sym.EndLine, &sym.Signature); err != nil {
			return nil, fmt.Errorf("scan exact symbol: %w", err)
		}
		result = append(result, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exact symbols: %w", err)
	}
	return result, nil
}

func (s *Store) ListIncomingReferences(ctx context.Context, projectID int64, symbol Symbol, limit int) ([]SymbolReference, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_name, from_file_path, to_name, ifnull(to_fq_name,''), ref_type, confidence, ifnull(evidence,'')
		FROM symbol_refs
		WHERE project_id = ? AND (to_name = ? OR to_fq_name = ?)
		ORDER BY ref_type ASC, confidence DESC, from_name ASC
		LIMIT ?
	`, projectID, symbol.Name, symbol.FQName, limit)
	if err != nil {
		return nil, fmt.Errorf("query incoming refs: %w", err)
	}
	defer rows.Close()

	refs := make([]SymbolReference, 0, limit)
	for rows.Next() {
		var ref SymbolReference
		if err := rows.Scan(&ref.FromName, &ref.FromFile, &ref.ToName, &ref.ToFQName, &ref.RefType, &ref.Confidence, &ref.Evidence); err != nil {
			return nil, fmt.Errorf("scan incoming ref: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incoming refs: %w", err)
	}
	return refs, nil
}

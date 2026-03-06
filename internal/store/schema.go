package store

const schemaSQL = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  root_path TEXT NOT NULL UNIQUE,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS modules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(project_id, name),
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS dependencies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  module_name TEXT NOT NULL,
  group_id TEXT NOT NULL,
  artifact_id TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  scope TEXT NOT NULL,
  dep_type TEXT NOT NULL,
  dep_kind TEXT NOT NULL DEFAULT 'direct',
  binary_jar_path TEXT,
  source_jar_path TEXT,
  source_status TEXT NOT NULL,
  resolution_type TEXT NOT NULL,
  metadata_json TEXT,
  confidence REAL NOT NULL DEFAULT 0.7,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(project_id, module_name, group_id, artifact_id, version, scope),
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_projects_name ON projects(name);
CREATE INDEX IF NOT EXISTS idx_deps_project_id ON dependencies(project_id);
CREATE INDEX IF NOT EXISTS idx_deps_module ON dependencies(project_id, module_name);

CREATE TABLE IF NOT EXISTS symbols (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  file_path TEXT NOT NULL,
  language TEXT NOT NULL,
  name TEXT NOT NULL,
  fq_name TEXT NOT NULL,
  kind TEXT NOT NULL,
  start_line INTEGER NOT NULL,
  end_line INTEGER NOT NULL,
  signature TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_symbols_project_name ON symbols(project_id, name);
CREATE INDEX IF NOT EXISTS idx_symbols_project_fq ON symbols(project_id, fq_name);

CREATE TABLE IF NOT EXISTS symbol_refs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  from_name TEXT NOT NULL,
  from_file_path TEXT NOT NULL,
  to_name TEXT NOT NULL,
  to_fq_name TEXT,
  ref_type TEXT NOT NULL,
  confidence REAL NOT NULL DEFAULT 0.7,
  evidence TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_symbol_refs_project_to_name ON symbol_refs(project_id, to_name);
CREATE INDEX IF NOT EXISTS idx_symbol_refs_project_to_fq ON symbol_refs(project_id, to_fq_name);

CREATE TABLE IF NOT EXISTS chunks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL,
  file_path TEXT NOT NULL,
  language TEXT NOT NULL,
  chunk_type TEXT NOT NULL,
  chunk_index INTEGER NOT NULL,
  symbol_name TEXT,
  text TEXT NOT NULL,
  token_count INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_chunks_project ON chunks(project_id);
CREATE INDEX IF NOT EXISTS idx_chunks_project_file ON chunks(project_id, file_path);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
  project_id UNINDEXED,
  file_path,
  language,
  chunk_type,
  symbol_name,
  text,
  content='chunks',
  content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
  INSERT INTO chunks_fts(rowid, project_id, file_path, language, chunk_type, symbol_name, text)
  VALUES (new.id, new.project_id, new.file_path, new.language, new.chunk_type, ifnull(new.symbol_name,''), new.text);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, project_id, file_path, language, chunk_type, symbol_name, text)
  VALUES('delete', old.id, old.project_id, old.file_path, old.language, old.chunk_type, ifnull(old.symbol_name,''), old.text);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, project_id, file_path, language, chunk_type, symbol_name, text)
  VALUES('delete', old.id, old.project_id, old.file_path, old.language, old.chunk_type, ifnull(old.symbol_name,''), old.text);
  INSERT INTO chunks_fts(rowid, project_id, file_path, language, chunk_type, symbol_name, text)
  VALUES (new.id, new.project_id, new.file_path, new.language, new.chunk_type, ifnull(new.symbol_name,''), new.text);
END;
`

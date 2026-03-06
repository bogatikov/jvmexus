# JVM GitNexus-like MCP Server (Go) - Implementation Plan

## Goal
Build an MCP server in Go that provides deep project intelligence for JVM repos (Java/Kotlin first): dependencies and source mapping, build graph, code graph, and RAG-backed retrieval.

## MVP Decisions
- Language focus: Java + Kotlin
- Build system focus: Gradle only (MVP)
- Storage: SQLite + FTS5 (+ vector column or app-side cosine)
- Dependency enrichment: attach `-sources.jar` when available in local Gradle cache
- Embeddings: local in-process small model only (no OpenAI in MVP)
- Model distribution: download on first run to local cache (`.jvmexus/models`), then reuse offline

## Non-Goals (MVP)
- Maven support
- Full JVM bytecode analysis
- Perfect Kotlin semantic resolution across all advanced language features

## Architecture
- `cmd/indexer`: one-shot (or incremental) repository indexing
- `cmd/mcp-server`: stdio MCP server exposing tools/resources
- `internal/repo`: repo discovery, file walking, hashes
- `internal/gradle`: settings/build parsing + optional Gradle enrichment
- `internal/parser`: Tree-sitter Java/Kotlin extraction
- `internal/graph`: symbol/dependency/build graph construction
- `internal/rag`: chunking, embeddings, hybrid retrieval
- `internal/store`: SQLite schema, migrations, queries
- `internal/mcp`: tool handlers, DTOs, validation

## MCP Surface (MVP)
### Tools
1. `list_projects` - list indexed projects
2. `index_project` - index/update a project path
3. `get_dependencies` - declared/resolved deps + binary/source jar attachment metadata
4. `get_build_graph` - modules/tasks/config edges
5. `query_code` - hybrid RAG search over code/build chunks
6. `get_symbol_context` - symbol, callers/callees/importers (best effort)

### Resources
- `jvminfo://projects`
- `jvminfo://project/{name}/summary`
- `jvminfo://project/{name}/dependencies`
- `jvminfo://project/{name}/build-graph`

## Data Model (SQLite)
- `projects(id, name, root_path, created_at, updated_at)`
- `files(id, project_id, path, lang, sha256, size, mtime)`
- `modules(id, project_id, name, path, parent_module)`
- `dependencies(id, project_id, module_id, group_id, artifact_id, version, scope, type, binary_jar_path, source_jar_path, source_status, resolution_type, confidence)`
- `symbols(id, project_id, file_id, fq_name, name, kind, language, start_line, end_line, signature)`
- `references(id, project_id, from_symbol_id, to_symbol_id, ref_type, confidence, evidence)`
- `build_nodes(id, project_id, module_id, node_type, name, metadata_json)`
- `build_edges(id, project_id, from_node_id, to_node_id, edge_type, confidence)`
- `chunks(id, project_id, file_id, symbol_id, chunk_type, text, token_count, metadata_json)`
- `embeddings(id, chunk_id, model, dim, vector_blob)`
- `index_runs(id, project_id, mode, status, started_at, finished_at, stats_json, error)`

FTS:
- `chunks_fts` (FTS5) over chunk text + symbol names + file paths.

## Indexing Pipeline
1. Discover Gradle project layout from `settings.gradle(.kts)`
2. Parse `build.gradle(.kts)` files for modules/plugins/dependencies
3. Optional enrichment: run Gradle dependency tasks and ingest resolved versions
4. Resolve binary jar and attach `-sources.jar` from local Gradle cache when present
5. Parse Java/Kotlin files with Tree-sitter into symbols/imports/call-like refs
6. Build graph edges (imports/calls/module deps/build task deps)
7. Chunk content (symbol-centric + build files)
8. Generate embeddings and persist vectors
9. Build retrieval indexes (FTS + vector metadata)

## Embeddings Strategy
- Default local provider interface:
  - `Embed(texts []string) ([][]float32, error)`
- First implementation: local small model loaded in app process.
- Initial default model: `Snowflake/snowflake-arctic-embed-xs` (or equivalent 384-dim small model).
- Model artifacts are downloaded on first use and cached under `.jvmexus/models`.
- No cloud embedding provider in MVP.

## Incremental Indexing Strategy
- Track file hashes + mtimes
- Re-parse only changed files
- Recompute affected symbols/references/chunks only
- Keep full rebuild command for fallback

## Quality & Confidence
- Tag edges with confidence: `1.0` AST-strong, `<1.0` heuristic
- Distinguish `declared` vs `resolved` dependencies
- Distinguish dependency source attachment states: `attached`, `not_found`, `unresolved_version`
- Return confidence and evidence fields in MCP responses

## Error Handling & UX
- Indexing should degrade gracefully (partial results over hard fail)
- Tool responses should include warnings array when enrichment is unavailable
- Timeouts around Gradle command execution

## Milestones
1. Bootstrap project + schema + MCP skeleton
2. Gradle parser (settings/build) + deps extraction
3. Java/Kotlin symbol extraction with Tree-sitter
4. Build graph + symbol graph persistence
5. Hybrid retrieval + embeddings provider abstraction
6. MCP tools/resources v1
7. Incremental indexing + polish + docs

## Validation Plan
- Fixture repos:
  - single-module Java Gradle
  - multi-module Java/Kotlin Gradle
  - Kotlin-heavy repo with DSL + extension functions
- Assertions:
  - module graph correctness
  - dependency extraction coverage
  - symbol extraction sanity
  - query relevance smoke tests
  - MCP contract tests for all tools

## v1.1 Candidates
- Maven support
- `detect_changes` impact tool
- richer Kotlin semantic resolution
- graph query tool (Cypher-like or simplified DSL)

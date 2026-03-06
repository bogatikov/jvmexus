# JVMexus Implementation Phases

## Phase 0 - Foundation and Contracts
Goal: stabilize project skeleton and define strict contracts before feature work.

- Finalize repo structure (`cmd/indexer`, `cmd/mcp-server`, `internal/*`)
- Define config model (local embeddings only, Gradle source-fetch flags, timeouts)
- Define MCP tool/resource JSON contracts (request/response schemas)
- Create initial SQLite schema + migrations with dependency source fields:
  - `binary_jar_path`
  - `source_jar_path`
  - `source_status`
  - `resolution_type`
- Add baseline logging/error conventions

Exit criteria:
- `go test ./...` passes on scaffold
- MCP server starts and lists placeholder tools/resources
- DB migrations run successfully

## Phase 1 - Gradle Project Discovery + Declared Dependency Extraction
Goal: parse Gradle structure and declared dependencies for Java/Kotlin multi-module repos.

- Parse `settings.gradle` / `settings.gradle.kts` for module topology
- Parse `build.gradle` / `build.gradle.kts` for declared dependencies/scopes/configs
- Support Gradle dependency declaration variants:
  - `scope("group:artifact:version")`
  - `scope("group:artifact")` (BOM/version-catalog managed)
  - `scope(platform("group:artifact:version"))`
  - Groovy DSL equivalents (`scope 'g:a:v'`, etc.)
  - common scopes: `implementation`, `api`, `compileOnly`, `runtimeOnly`, `annotationProcessor`, `testImplementation`, `testRuntimeOnly`
- Persist modules + declared dependencies into SQLite
- Track dependency origin (`declared`)

Exit criteria:
- Multi-module project module graph extracted correctly
- Declared dependencies visible via `get_dependencies`
- Confidence/evidence fields populated (even if heuristic)
- Spring/BOM-heavy projects return full direct dependency list (not just explicit `g:a:v` entries)

## Phase 2 - Resolved Dependency Enrichment + Source Jar Attachment
Goal: resolve concrete artifacts and attach source jars if available.

- Resolve dependency versions (best-effort via Gradle command integration)
- Resolve versions for initially versionless declared dependencies (`group:artifact`) via Gradle output
- Locate binary jars in local Gradle cache
- Attach source jars from cache if present
- If source missing and allowed, attempt download, then re-check cache
- Add statuses:
  - `attached` (already present)
  - `downloaded` (fetched during run)
  - `not_found`
  - `unresolved_version`
  - `download_failed`
- Add indexing warnings for auth/network/mirror issues without failing whole index

Controls:
- `fetchMissingSources` (default: true)
- `offline` (default: false)
- `sourcesDownloadTimeout` (default: 60s)

Exit criteria:
- `get_dependencies` returns source metadata and status per dependency
- Index succeeds even if source fetching partially fails
- Clear warnings emitted and persisted
- Versionless direct dependencies become `resolved` when Gradle provides concrete versions

## Phase 2.1 - Dependency Model Coverage and Visibility
Goal: improve dependency output usability for real-world Gradle projects.

- Add explicit dependency kind classification (`direct`, `transitive`)
- Keep `get_dependencies` default to `direct` for concise output
- Add optional `includeTransitive` mode in MCP/API output
- Capture and persist dependency declarations with `platform(...)` / BOM context metadata

Exit criteria:
- Users can see expected direct dependencies in BOM-heavy projects
- Output can be expanded to transitive dependencies without changing direct default behavior

## Phase 3 - Java/Kotlin Symbol Extraction
Goal: build symbol inventory and reference edges from source code.

- Integrate Tree-sitter Java + Kotlin grammars in Go
- Extract symbols (class/interface/method/function/file-level entities)
- Extract imports + call-like references with confidence scoring
- Persist symbols and references

Exit criteria:
- Symbol counts and key entities match fixture expectations
- `get_symbol_context` returns useful callers/callees/importers (best effort)

## Phase 4 - Build Graph and Cross-Graph Linking
Goal: build graph dimensions needed for architecture reasoning.

- Build nodes for modules/tasks/configurations
- Build edges (`dependsOn`, module deps, selected call/import links)
- Link dependency artifacts to modules and source files where possible

Exit criteria:
- `get_build_graph` returns stable graph JSON for fixtures
- Graph nodes/edges are queryable by project and module

## Phase 5 - Local Embeddings + Hybrid Retrieval
Goal: ship local-only semantic retrieval (no OpenAI).

- Implement embeddings provider abstraction with local provider only in MVP
- Default model path: first-run download, cache under `.jvmexus/models`
- Chunking strategy: symbol-centric + build-script chunks
- Store vectors + FTS5 index
- Hybrid retrieval: BM25 + vector rerank

Exit criteria:
- First run downloads model; subsequent runs reuse cache
- `query_code` returns relevant hybrid results on fixture queries
- Works fully offline after model is cached

## Phase 6 - MCP Productization (v1 Tooling)
Goal: deliver complete, consistent MCP experience.

- Implement tools:
  - `list_projects`
  - `index_project`
  - `get_dependencies`
  - `get_build_graph`
  - `query_code`
  - `get_symbol_context`
- Implement resources:
  - `jvminfo://projects`
  - `jvminfo://project/{name}/summary`
  - `jvminfo://project/{name}/dependencies`
  - `jvminfo://project/{name}/build-graph`
- Add pagination/limits and deterministic sorting

Exit criteria:
- Tools/resources callable from MCP clients
- Outputs are schema-stable and documented

## Phase 7 - Incremental Indexing, Reliability, and Release Readiness
Goal: make indexing practical for daily usage.

- Hash-based incremental reindex
- Recompute only impacted symbols/chunks/references
- Improve retry/timeout behavior and failure diagnostics
- Add fixture/integration tests and performance smoke checks
- Prepare v1 docs (setup, flags, model cache behavior, troubleshooting)

Exit criteria:
- Incremental indexing materially faster than full on repeat runs
- End-to-end tests pass for single-module and multi-module fixtures
- v1 release checklist complete

# AGENTS Guide

This file defines practical rules for humans and coding agents contributing to JVMexus.

## Mission

JVMexus is a local-first intelligence layer for JVM repositories, exposed through MCP.

Core promise:

- reliable Gradle indexing
- useful symbol/build/dependency context
- fast repeat indexing via smart incremental mode
- stable MCP contracts for tools/resources

## Architecture Map

- `cmd/indexer`: CLI entrypoint for indexing
- `cmd/mcp-server`: stdio MCP server entrypoint
- `internal/indexer`: orchestration, incremental/full mode routing
- `internal/gradle`: module/dependency parsing and enrichment
- `internal/parser`: symbol/ref extraction
- `internal/store`: SQLite schema and persistence API
- `internal/rag`: query searcher and local embedder
- `internal/mcp`: tool/resource registration and response shaping

## Contribution Rules

- Preserve MCP response stability unless intentionally versioned.
- Prefer additive changes over breaking schema changes.
- Keep graceful degradation behavior (warnings over hard failures where possible).
- Do not commit local runtime artifacts (`.jvmexus/*`) or IDE metadata.

## Testing Expectations

Before merging code changes:

1. `go test ./...` passes
2. If indexing behavior changed, run manual index smoke:
   - `go run ./cmd/indexer -path <fixture> -force`
   - `go run ./cmd/indexer -path <fixture>`
3. If MCP behavior changed, validate `index_project` + `query_code` through MCP Inspector

## Incremental Indexing Safety

When touching indexing logic:

- keep build-impact triggers conservative (prefer full reindex if uncertain)
- ensure deleted files clear symbols/refs/chunks consistently
- keep `mode`, `changedFiles`, `deletedFiles`, `skippedFiles` semantics stable

## RAG/Embeddings Expectations

- Current retrieval is hybrid rerank over FTS candidates.
- Embeddings may run in fastembed or hashed-fallback mode.
- Avoid introducing remote embedding dependencies for v1 scope.

## Git Hygiene

- Keep commits focused and atomic.
- Avoid destructive git operations on shared branches.
- Never commit secrets, credentials, or local env data.

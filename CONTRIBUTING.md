# Contributing to JVMexus

Thanks for your interest in improving JVMexus.

## Development Setup

Requirements:

- Go 1.24+
- Git
- A sample Gradle JVM repository for manual integration checks

Clone and test:

```bash
go test ./...
```

Run binaries locally:

```bash
go run ./cmd/indexer -path /absolute/path/to/fixture
go run ./cmd/mcp-server
```

## Project Structure

- `cmd/indexer`: CLI indexing entrypoint
- `cmd/mcp-server`: MCP stdio server entrypoint
- `internal/gradle`: Gradle discovery/dependency enrichment/source jars
- `internal/indexer`: indexing orchestration and incremental logic
- `internal/parser`: Java/Kotlin symbol/reference extraction
- `internal/rag`: hybrid retrieval and local embeddings
- `internal/mcp`: MCP tools/resources contract surface
- `internal/store`: SQLite schema/repository layer

## Pull Request Guidelines

- Keep PRs focused and minimal in scope.
- Add or update tests for behavior changes.
- Keep public responses schema-stable for MCP tool/resource outputs.
- Run `go test ./...` before submitting.
- Update docs (`README.md`, `planning/*`) when behavior or contracts change.

## Commit Guidelines

- Use concise, imperative commit titles.
- Explain why in the commit body for non-trivial changes.
- Avoid mixing unrelated concerns in one commit.

## Reporting Bugs

Please open a GitHub issue and include:

- expected vs actual behavior
- reproduction steps
- sample project characteristics (single/multi module, Java/Kotlin, Gradle version)
- relevant logs/warnings (without secrets)

## Security

For security-related reports, see `SECURITY.md`.

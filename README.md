# JVMexus

JVMexus is a local-first MCP server and indexer for JVM repositories (Java/Kotlin, Gradle-first).

It provides project/module/dependency intelligence, symbol context, build graph output, and hybrid code retrieval for agent workflows.

## Features

- Gradle module discovery from `settings.gradle(.kts)`
- Declared and resolved dependency enrichment with source jar attachment metadata
- Symbol and reference extraction for Java/Kotlin
- Build graph API for modules and dependencies
- Hybrid retrieval for `query_code` (`fts5` + local vector rerank)
- Smart incremental indexing with build-impact fallback

## MCP Surface (v1)

Tools:

- `list_projects`
- `index_project`
- `get_dependencies`
- `get_build_graph`
- `query_code`
- `get_symbol_context`

Resources:

- `jvminfo://projects`
- `jvminfo://project/{name}/summary`
- `jvminfo://project/{name}/dependencies`
- `jvminfo://project/{name}/build-graph`

## Quick Start

Requirements:

- Go 1.24+
- Gradle wrapper (`./gradlew`) in the target project (recommended for resolved dependency enrichment)

Run indexer:

```bash
go run ./cmd/indexer -path /absolute/path/to/your/gradle-repo
```

Run MCP server over stdio:

```bash
go run ./cmd/mcp-server
```

## Installation

JVMexus currently ships as a Go project/binary (no package manager release yet).

Option A: run directly from source:

```bash
go run ./cmd/mcp-server
```

Option B: build a local binary:

```bash
go build -o bin/jvmexus-mcp ./cmd/mcp-server
./bin/jvmexus-mcp
```

Optional indexer binary:

```bash
go build -o bin/jvmexus-indexer ./cmd/indexer
./bin/jvmexus-indexer -path /absolute/path/to/repo
```

## MCP Client Setup

JVMexus uses stdio transport. Configure your MCP client to launch `cmd/mcp-server`.

Example generic MCP config:

```json
{
  "mcpServers": {
    "jvmexus": {
      "command": "go",
      "args": ["run", "./cmd/mcp-server"],
      "cwd": "/absolute/path/to/jvmexus",
      "env": {
        "JVMEXUS_DB_PATH": ".jvmexus/index.db",
        "JVMEXUS_MODEL_CACHE_DIR": ".jvmexus/models"
      }
    }
  }
}
```

If you built binaries, you can replace `command`/`args` with:

- `command`: `/absolute/path/to/jvmexus/bin/jvmexus-mcp`
- `args`: `[]`

## First 5 Minutes (Usage)

After connecting your MCP client:

1. Call `index_project` once:

```json
{
  "path": "/absolute/path/to/target-gradle-repo"
}
```

2. Use the project folder name returned by index, then call `query_code`:

```json
{
  "project": "target-gradle-repo",
  "query": "where is helper used to return pong",
  "limit": 5
}
```

3. Explore context and topology:

- `get_dependencies` with `{ "project": "target-gradle-repo" }`
- `get_build_graph` with `{ "project": "target-gradle-repo" }`
- `get_symbol_context` with `{ "project": "target-gradle-repo", "symbol": "helper" }`

Expected from `query_code`:

- `retrievalMode = "hybrid:fts5+local-vector-rerank"`
- non-empty `model`
- scored results (`lexicalScore`, `semanticScore`, `hybridScore`)

## Runtime Flags And Environment

Indexer CLI flags:

- `-path` project path to index (default `.`)
- `-force` force full reindex (skip smart incremental path)

Environment variables:

- `JVMEXUS_DB_PATH` path to SQLite DB (default `.jvmexus/index.db`)
- `JVMEXUS_MODEL_CACHE_DIR` local model cache directory (default `.jvmexus/models`)
- `EMBEDDINGS_PROVIDER` embeddings provider (default `local`)
- `EMBEDDINGS_MODEL_ID` embedding model id (default `Snowflake/snowflake-arctic-embed-xs`)
- `JVMEXUS_GRADLE_TIMEOUT_SECONDS` Gradle command timeout per call (default `120`)
- `JVMEXUS_FETCH_MISSING_SOURCES` fetch missing source jars (default `true`)
- `JVMEXUS_OFFLINE` skip network enrichment/download paths (default `false`)
- `JVMEXUS_SOURCES_DOWNLOAD_TIMEOUT_SECONDS` source jar download timeout (default `60`)

## Smart Incremental Indexing

By default, indexing is incremental:

- tracks file fingerprints (`sha256`, size, mtime) in `indexed_files`
- source-only changes update symbols/references/chunks for changed files
- build-impacting changes force full reindex for correctness

Build-impacting files:

- `settings.gradle`, `settings.gradle.kts`
- `build.gradle`, `build.gradle.kts`
- `gradle.properties`
- `gradle/libs.versions.toml`

`index_project` includes:

- `mode` (`full` or `incremental`)
- `changedFiles`
- `deletedFiles`
- `skippedFiles`

## RAG And Embeddings

- Retrieval path is hybrid: SQLite FTS candidates + local vector rerank
- `query_code` returns lexical, semantic, and hybrid scores
- Runtime uses local embedder; if fastembed model init fails, hashed fallback is used

Model cache behavior:

- default cache path: `.jvmexus/models`
- first semantic retrieval run initializes local embedding runtime and cache
- model manifest is written under model cache (`model.manifest`)

## Manual Testing (MCP Inspector)

In MCP Inspector (stdio):

- command: `go`
- args: `run ./cmd/mcp-server`
- working directory: `/absolute/path/to/jvmexus`

Then:

1. Start/connect the server session
2. In MCP Inspector, call `index_project`:

```json
{
  "path": "/absolute/path/to/repo"
}
```

3. Call `query_code`:

```json
{
  "project": "repo-folder-name",
  "query": "where is helper used",
  "limit": 5
}
```

Expected:

- `retrievalMode` is `hybrid:fts5+local-vector-rerank`
- `model` is non-empty
- `results` include `lexicalScore`, `semanticScore`, `hybridScore`

## Development

- Run tests: `go test ./...`
- Release checklist: `planning/03-v1-release-checklist.md`
- Contribution guide: `CONTRIBUTING.md`

## Troubleshooting

### Gradle enrichment warnings

Resolved dependency enrichment is best effort. Warnings include failure class and stderr summary:

- `timeout`: Gradle command exceeded configured timeout
- `network`: transient connectivity failures
- `repository`: remote repository fetch/metadata failures
- `auth`: authorization/credentials failures
- `execution`: non-retryable Gradle task failures

Suggestions:

- run with `JVMEXUS_OFFLINE=true` when network is unavailable
- increase `JVMEXUS_GRADLE_TIMEOUT_SECONDS` for large multi-module repos
- verify wrapper directly: `./gradlew dependencies --configuration runtimeClasspath --console=plain --quiet --no-daemon`

### No resolved dependencies returned

- ensure repo has executable `./gradlew`
- ensure indexing is not in offline mode
- ensure build files are valid and dependency tasks run for modules

### Source jars not attached

- confirm `JVMEXUS_FETCH_MISSING_SOURCES=true`
- verify artifact exists with `-sources.jar` in cache or remote repository
- inspect warning entries from indexing result

## License

MIT. See `LICENSE`.

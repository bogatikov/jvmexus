# JVMexus

JVMexus is a local-first MCP server and indexer for JVM repositories (Java/Kotlin, Gradle-first).

## Quick Start

Requirements:

- Go 1.24+
- Gradle wrapper (`./gradlew`) in the target project (recommended for resolved dependency enrichment)

Build and run indexer:

```bash
go run ./cmd/indexer -path /absolute/path/to/your/gradle-repo
```

Run MCP server over stdio:

```bash
go run ./cmd/mcp-server
```

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

Build-impacting files include:

- `settings.gradle`, `settings.gradle.kts`
- `build.gradle`, `build.gradle.kts`
- `gradle.properties`
- `gradle/libs.versions.toml`

`index_project` includes mode and counters:

- `mode` (`full` or `incremental`)
- `changedFiles`
- `deletedFiles`
- `skippedFiles`

## Model Cache Behavior

- default cache path: `.jvmexus/models`
- first semantic retrieval run initializes local embedding runtime and cache
- fallback hashed embeddings are used when fastembed model init fails

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
- verify Gradle wrapper works directly: `./gradlew dependencies --configuration runtimeClasspath --console=plain --quiet --no-daemon`

### No resolved dependencies returned

- ensure repo has executable `./gradlew`
- ensure indexing is not in offline mode
- ensure build files are valid and dependencies task runs for modules

### Source jars not attached

- confirm `JVMEXUS_FETCH_MISSING_SOURCES=true`
- verify artifact exists with `-sources.jar` in cache or remote repository
- inspect warning entries from indexing result

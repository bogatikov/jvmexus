# JVMexus v1 Release Checklist

Use this checklist to close Phase 7 and declare v1 release readiness.

## 1) Preflight

- [ ] Working tree is clean except intentionally ignored files
- [ ] `.idea/` is ignored or removed from release branch
- [ ] `go test ./...` passes on current branch
- [ ] `go run ./cmd/mcp-server` starts without panic
- [ ] `go run ./cmd/indexer -path <fixture>` completes without fatal errors

## 2) Functional Validation

### Single-module fixture

- [ ] `index_project` returns expected project/module/file counts
- [ ] `get_dependencies` shows direct dependencies and source metadata
- [ ] `get_build_graph` returns stable nodes/edges
- [ ] `query_code` returns relevant chunk hits for basic prompts
- [ ] `get_symbol_context` returns callers/callees/imports for known symbol

### Multi-module fixture

- [ ] module topology from `settings.gradle(.kts)` is correct
- [ ] per-module dependencies are correctly attributed
- [ ] build graph includes all modules and dependency edges
- [ ] symbol indexing spans modules and cross-file queries still work

## 3) Incremental Indexing Proof

Run these smoke commands on the same repo, back-to-back:

```bash
# forced baseline (full)
go run ./cmd/indexer -path <fixture> -force

# repeat run (should be incremental/no-op heavy)
go run ./cmd/indexer -path <fixture>
```

Record observed values from `index_project`/indexer output:

- [ ] first run mode is `full`
- [ ] second run mode is `incremental`
- [ ] second run reports `changedFiles=0` (or near-zero if environment mutates files)
- [ ] second run wall-clock is materially faster than full run

### Optional timing table

| Fixture | Full Run (s) | Incremental Run (s) | Speedup |
|---|---:|---:|---:|
| single-module |  |  |  |
| multi-module |  |  |  |

## 4) Reliability And Diagnostics

- [ ] Gradle timeout warning is classified as `timeout`
- [ ] transient network failure warning is classified as `network`
- [ ] repository fetch failure warning is classified as `repository`
- [ ] auth failure warning is classified as `auth`
- [ ] warning output includes attempt count and compact stderr

## 5) Docs And Operator UX

- [ ] `README.md` includes setup and run commands
- [ ] `README.md` lists all relevant env flags and defaults
- [ ] `README.md` explains model cache behavior
- [ ] `README.md` includes troubleshooting for Gradle/source download failures

## 6) Release Gate

- [ ] All checklist sections complete
- [ ] No blocker bugs open for v1 scope
- [ ] Phase 7 exit criteria satisfied
- [ ] Tag/announce v1

---

## Sign-off

- Engineering: [ ]
- QA/Validation: [ ]
- Release owner: [ ]
- Date: [ ]

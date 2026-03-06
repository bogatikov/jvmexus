# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Added

- Publication readiness docs (`README`, `AGENTS`, governance and community files)
- GitHub issue/PR templates and CI workflow

## [0.1.0] - 2026-03-07

### Added

- MCP server with tools: `list_projects`, `index_project`, `get_dependencies`, `get_build_graph`, `query_code`, `get_symbol_context`
- MCP resources for projects, summary, dependencies, and build graph
- Gradle project/module discovery and declared dependency extraction
- Resolved dependency enrichment and source jar attachment statuses
- Symbol and reference extraction pipeline (Java + Kotlin heuristic path)
- Build graph payload generation
- Hybrid retrieval baseline (`fts5` + local vector rerank)
- Smart incremental indexing with file fingerprint tracking and build-impact fallback
- Reliability improvements for Gradle dependency resolution with classified diagnostics
- Phase 7 release checklist (`planning/03-v1-release-checklist.md`)

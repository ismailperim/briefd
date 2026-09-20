# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `briefd index`: walks a knowledge directory, parses front matter, chunks
  Markdown on H2/H3 headings (200–800 tokens, stable chunk IDs) and stores
  documents + chunks in SQLite with an FTS5 index. Incremental by content hash;
  deleted files are removed from the index.
- `briefd search`: BM25 search over the index with scope filtering, JSON output,
  and diacritics-insensitive, stemmed matching.
- Sample knowledge repository under `testdata/knowledge/`.
- Project skeleton: Go module, `briefd version` command, Makefile, lint config,
  CI, ADR-0001 (core architecture) and ADR-0002 (SQLite driver, vector search).

# Installing briefd — instructions for a coding agent

You are a coding agent asked to set up briefd for this machine or repository.
Follow these steps in order, run the commands, and report what you did. Do
not skip the verification step.

## 1. Install the binary

Prefer the release binary. Detect the platform and pick the matching asset
from https://github.com/ismailperim/briefd/releases/latest
(`briefd_<version>_<os>_<arch>.tar.gz`; os is `linux` or `darwin`, arch is
`amd64` or `arm64`; Windows has a `.zip`). Extract `briefd` somewhere on
`PATH` (for example `/usr/local/bin` or `~/.local/bin`).

If Go ≥ 1.26 is installed, this also works:

```sh
go install github.com/ismailperim/briefd/cmd/briefd@latest
```

Verify: `briefd version` prints a version.

## 2. Decide where the knowledge lives

briefd serves Markdown from a directory or git repository with these folders:
`domain/` (shared rules, glossary, decisions), `conventions/` (how the team
writes code), `projects/<name>/` (notes for one project).

- If the user already has such a repository, use its path or git URL as
  `SOURCE`.
- Otherwise create a starter one: `briefd init ./knowledge` and use
  `./knowledge` as `SOURCE`. Tell the user the example documents are
  placeholders to replace.

Never put secrets or generated content into the knowledge repository.

## 3. Choose a mode

**One agent on this machine (default):** no server, no port, no token.
Configure the MCP client to launch briefd over stdio. For Claude Code, from
the project directory:

```sh
claude mcp add briefd -- briefd mcp --source SOURCE --db ~/.briefd/knowledge.db
```

For a project-level `.mcp.json` (Claude Code, Cursor) or Claude Desktop:

```json
{
  "mcpServers": {
    "briefd": {
      "command": "briefd",
      "args": ["mcp", "--source", "SOURCE", "--db", "~/.briefd/knowledge.db"]
    }
  }
}
```

**A team sharing one instance:** run `briefd serve --source SOURCE --token TOKEN`
on a host the team can reach (or the container image
`ghcr.io/ismailperim/briefd`), then register the HTTP endpoint:

```sh
claude mcp add --transport http briefd http://HOST:7788/mcp --header "Authorization: Bearer TOKEN"
```

The first start downloads the embedding model (about 470 MB, once). If the
machine has no network access, use `--embeddings none` (keyword search only).

## 4. Tell the agent when to use it

Add this to the project's `CLAUDE.md` / `AGENTS.md` (or the equivalent
instructions file) so future sessions actually call the tools:

```markdown
## Team knowledge

Team knowledge (domain rules, conventions, architecture decisions) is served
by the `briefd` MCP server. Before implementing anything that could be
governed by a team rule, call `compile_bundle` with a one-sentence
description of the task and follow what it returns. Use `search_context` to
inspect ranked sections and `get_document` for a full document. If the
knowledge is wrong, stale or missing, call `propose_update` — never edit the
knowledge repository directly. When done, call `report_usage` with the
bundle id and the chunk ids that helped (an empty list if nothing did).
```

## 5. Verify

Start a new session (or reload MCP servers) and call `list_scopes`. It must
return at least `domain` and `conventions`. Then call `compile_bundle` with a
real task description and confirm the bundle lists sections with source
lines. Report the tool names you saw and the number of documents indexed.

If `list_scopes` is empty, `SOURCE` has no `domain/`, `conventions/` or
`projects/<name>/` folders with Markdown files; fix the path and retry.

## Reference

- README: https://github.com/ismailperim/briefd#readme
- Deployment guide (Compose, systemd, forges, proxies): https://github.com/ismailperim/briefd/blob/main/deploy/README.md
- Config reference: https://github.com/ismailperim/briefd/blob/main/deploy/briefd.example.yaml

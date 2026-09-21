# Deploying briefd

briefd is one process with one SQLite file. Everything it needs — the git
checkout, the database, the embedding model — lives in a single data directory.
This guide covers the ways to run it and the things that bite in corporate
networks. For a laptop-only setup see [`local/`](local/).

- [Choose how to run it](#choose-how-to-run-it)
- [Docker Compose on a server](#docker-compose-on-a-server)
- [Binary as a service (systemd)](#binary-as-a-service-systemd)
- [Knowledge sources: git forges](#knowledge-sources-git-forges)
- [The embedding model, online and offline](#the-embedding-model-online-and-offline)
- [Corporate networks: proxies and private CAs](#corporate-networks-proxies-and-private-cas)
- [Exposure and security](#exposure-and-security)
- [Operations: upgrades, backups, monitoring](#operations-upgrades-backups-monitoring)
- [Connecting agents](#connecting-agents)

## Choose how to run it

| | When | Notes |
|---|---|---|
| **Docker Compose** | shared server for a team | `deploy/docker-compose.yml`, image `ghcr.io/ismailperim/briefd` (linux/amd64, arm64, ~34 MB) |
| **Binary + systemd** | server without Docker, or you want the model on the host | release archives for linux/darwin/windows |
| **Laptop, localhost** | private knowledge, no server yet | [`local/`](local/): launchd, `.mcp.json`, `CLAUDE.md` templates |

Resource guide: 1 vCPU / 1 GB serves a few thousand sections comfortably
(idle ~150 MB with the multilingual model mapped, queries in tens of ms).
Give it 2–4 vCPUs for the initial embedding of a large corpus — that is the
only heavy step, and it is incremental afterwards.

## Docker Compose on a server

```sh
git clone https://github.com/ismailperim/briefd && cd briefd/deploy
cat > .env <<'EOF'
BRIEFD_SOURCE=https://git.example.com/team/knowledge.git
BRIEFD_GIT_TOKEN=<read-only token for the knowledge repo>
BRIEFD_API_TOKEN=<openssl rand -hex 16>
EOF
docker compose up -d
docker compose logs -f briefd        # "briefd listening" … "embedding chunks" … "index updated"
```

The `briefd-data` volume holds `/data/briefd.db`, the checkout under
`/data/knowledge-repo`, and models under `/data/cache/briefd/models/`. The
image runs as a non-root user (uid 65532) and needs no other service.

To serve a directory that already exists on the host instead of a git URL,
mount it read-only and point `BRIEFD_SOURCE` at the mount:

```yaml
    volumes:
      - briefd-data:/data
      - /srv/knowledge:/knowledge:ro
    environment:
      BRIEFD_SOURCE: /knowledge
```

Every setting has a `BRIEFD_*` variable; see [`briefd.example.yaml`](briefd.example.yaml)
for the full list. `PORT` is honored when `BRIEFD_LISTEN` is unset, so PaaS
platforms (Render, Railway, Fly) work with their defaults.

## Binary as a service (systemd)

```sh
curl -L https://github.com/ismailperim/briefd/releases/latest/download/briefd_$(curl -s https://api.github.com/repos/ismailperim/briefd/releases/latest | grep -o '"tag_name": "v[^"]*' | cut -dv -f2)_linux_amd64.tar.gz | tar xz
sudo install -m 0755 briefd /usr/local/bin/briefd
sudo useradd --system --home /var/lib/briefd --create-home briefd
sudo -u briefd mkdir -p /var/lib/briefd
sudo -u briefd tee /var/lib/briefd/briefd.yaml >/dev/null <<'EOF'
listen: "127.0.0.1:7788"
db: "/var/lib/briefd/briefd.db"
source: "https://git.example.com/team/knowledge.git"
api_token: "change-me"
git:
  token: "change-me-too"
EOF
sudo tee /etc/systemd/system/briefd.service >/dev/null <<'EOF'
[Unit]
Description=briefd context compiler
After=network-online.target
Wants=network-online.target

[Service]
User=briefd
WorkingDirectory=/var/lib/briefd
ExecStart=/usr/local/bin/briefd serve --config /var/lib/briefd/briefd.yaml
Restart=on-failure
Environment=XDG_CACHE_HOME=/var/lib/briefd/cache
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/briefd
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl enable --now briefd
journalctl -u briefd -f
```

`briefd.yaml` contains tokens: keep it `0600` and owned by the service user.

## Knowledge sources: git forges

`source` is a directory or a git URL. For servers, **HTTPS with a token** is
the reliable choice: no SSH agent, no `known_hosts`, works inside the
container. briefd sends the token as the password with username `briefd`
(configurable with `git.username`), which every major forge accepts.

| Forge | URL shape | Token | Proposal PRs |
|---|---|---|---|
| GitHub | `https://github.com/org/knowledge.git` | fine-grained PAT, *Contents: read* (write for proposals) | `forge.type: github` opens pull requests |
| GitLab | `https://gitlab.example.com/team/knowledge.git` | project/group token, `read_repository` (+ `write_repository`) | branch is pushed; open the MR yourself ([#3](https://github.com/ismailperim/briefd/issues/3)) |
| Azure DevOps | `https://dev.azure.com/org/project/_git/knowledge` or `https://ado.example.com/collection/project/_git/knowledge` | PAT, *Code: Read* (+ Write) | branch is pushed; open the PR yourself |
| Bitbucket | `https://bitbucket.org/team/knowledge.git` | app password / access token | branch is pushed |
| Any SSH remote | `git@host:team/knowledge.git` | `git.ssh_key` (a key file) or the agent | as above |

Notes:

- Spaces or other special characters in the project path are fine in
  URL-encoded form (`%20`).
- The checkout is a mirror: briefd fetches and hard-resets to the remote
  branch every `sync.interval` (default 60 s). Nobody should edit it.
- `sync.webhook_secret` enables `POST /webhook/git` with a GitHub-style
  `X-Hub-Signature-256` HMAC. Forges that sign differently (GitLab token
  header, Azure DevOps basic auth) can't call it yet — polling covers them.
- Proposals need write access on the token; without it the branch stays in
  briefd's local checkout and `propose_update` reports `pushed: false`.
- A directory that is itself a git checkout is read in place (no fetch);
  proposals become local branches. Useful when a CI job or another process
  already keeps the checkout current.

## The embedding model, online and offline

The default model, `multilingual-e5-small` (470 MB), is downloaded once from
huggingface.co into the model cache and verified against pinned SHA-256
digests. `all-MiniLM-L6-v2` (87 MB, English only, ~2.5× faster) is the
alternative: `embeddings.model: all-MiniLM-L6-v2`.

Model cache location:

| Setup | Directory |
|---|---|
| Docker image | `/data/cache/briefd/models/<model>/` |
| Linux binary | `$XDG_CACHE_HOME/briefd/models/<model>/` (default `~/.cache/briefd/models/<model>/`) |
| macOS binary | `~/Library/Caches/briefd/models/<model>/` |
| Explicit | `embeddings.model_dir` / `BRIEFD_EMBEDDINGS_MODEL_DIR` |

If the server cannot reach huggingface.co, fetch the files on a machine that
can and copy the directory:

```sh
# on a machine with internet access
briefd model pull                       # or: briefd model pull --model all-MiniLM-L6-v2
tar -C ~/.cache/briefd/models -czf e5-small.tgz multilingual-e5-small   # macOS: ~/Library/Caches/briefd/models

# on the server (Docker): unpack into the volume, owned by the image's uid
V=$(docker volume inspect deploy_briefd-data -f '{{.Mountpoint}}')
sudo mkdir -p "$V/cache/briefd/models"
sudo tar -C "$V/cache/briefd/models" -xzf e5-small.tgz
sudo chown -R 65532:65532 "$V/cache"
docker compose restart briefd

# on the server (binary): unpack into the cache dir of the service user
sudo -u briefd mkdir -p /var/lib/briefd/cache/briefd/models
sudo -u briefd tar -C /var/lib/briefd/cache/briefd/models -xzf e5-small.tgz
```

Only two files matter: `model.safetensors` and `tokenizer.json` (`vocab.txt`
for MiniLM). With them present briefd never touches the network for
embeddings; set `embeddings.auto_download: false` to make that explicit.

For very large corpora (tens of thousands of sections) or GPU hosts, point
`embeddings.provider` at Ollama (`bge-m3` is a strong multilingual choice) or
an OpenAI-compatible endpoint; vectors are keyed by model, so switching
re-embeds automatically.

## Corporate networks: proxies and private CAs

- **Proxy:** Go honors `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` for the
  model download and HTTPS git remotes. Put the internal forge in `NO_PROXY`.
- **Private CA:** an internal forge with a company certificate is rejected by
  the distroless image's default trust store. Mount the CA bundle and point
  Go at it:
  ```yaml
      volumes:
        - /etc/ssl/certs/company-ca.pem:/certs/ca.pem:ro
      environment:
        SSL_CERT_FILE: /certs/ca.pem
  ```
  For the binary on Linux, adding the CA to the system store is enough; on
  macOS the keychain is used.
- **TLS interception** (Zscaler and friends) works the same way — trust the
  interception CA.
- **No outbound access at all:** copy the model as above, use a directory or
  an internal git URL as the source; nothing else leaves the box. briefd has
  no telemetry.

## Exposure and security

- **Bind to localhost or a private interface** and keep the bearer token
  (`api_token` / `BRIEFD_API_TOKEN`) secret. An empty token disables auth —
  only for a trusted machine.
- briefd terminates plain HTTP and has **no rate limiting**. On a shared
  network put a reverse proxy in front for TLS (and limits if you need them):
  ```
  # Caddyfile
  knowledge.internal.example.com {
      reverse_proxy 127.0.0.1:7788
  }
  ```
  or reach it over a private overlay (WireGuard, Tailscale) with no public
  exposure at all.
- One token per instance in v0.x; every team member uses the same one. Rotate
  by changing the config and restarting.
- `/metrics` is unauthenticated by default (counts only, no content); set
  `metrics.require_auth: true` to protect it.
- The dashboard at `/` is a static page; it asks for the token before
  reading anything.
- What agents retrieve (a bundle, ~2k tokens) is sent to the model provider
  by the agent, exactly as `CLAUDE.md` content is today — briefd itself sends
  nothing anywhere.

## Operations: upgrades, backups, monitoring

- **Upgrades:** pull the new image / binary and restart. Schema migrations run
  automatically. The database is a disposable cache — worst case delete it
  and briefd rebuilds from the git checkout (`briefd index --rebuild` for the
  binary). Bundles and usage feedback are also in the DB; if you want to keep
  `usage_events` across a rebuild, back the file up first.
- **Backups:** the knowledge is in git; the model is re-downloadable; the DB
  is rebuildable. Back up `briefd.db` only if you care about usage history.
- **Monitoring:** scrape `GET /metrics` (Prometheus text). Useful alerts:
  `briefd_sync_runs_total{status="error"}` increasing, `briefd_vectors_pending`
  not draining, p95 of `briefd_request_duration_seconds` above 1 s. `GET /healthz`
  and `GET /api/health` are unauthenticated liveness endpoints; `/api/health`
  includes the last sync time and error.
- **Logs:** structured `log/slog` on stderr; `log_level: debug` also logs
  every HTTP request and MCP session.

## Connecting agents

Distribute one snippet to the team; it is the same for every project:

```json
{
  "mcpServers": {
    "briefd": {
      "type": "http",
      "url": "https://knowledge.internal.example.com/mcp",
      "headers": { "Authorization": "Bearer <token>" }
    }
  }
}
```

Claude Code: `claude mcp add --transport http briefd <url>/mcp --header "Authorization: Bearer <token>"`.
A short `CLAUDE.md` telling the agent to call `compile_bundle` before
implementing anything governed by team rules is in [`local/CLAUDE.md`](local/CLAUDE.md);
the "Tokens saved" tile on the dashboard shows what that is worth.

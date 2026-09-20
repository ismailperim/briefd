# Running briefd on your own machine (macOS)

For a knowledge repo that must stay on your laptop or inside a company network:
briefd binds to localhost only, nothing is exposed, and the index lives in
`~/.briefd`. Clients on the same machine (Claude Code, Cursor, Codex) connect
over `http://127.0.0.1:7788/mcp`.

## 1. Install

```sh
brew install go && go install github.com/ismailperim/briefd/cmd/briefd@latest   # or download a release binary
briefd model pull                                                                # 470 MB, once; do this on a network that can reach huggingface.co
```

## 2. Configure

```sh
mkdir -p ~/.briefd
cp deploy/local/briefd.yaml ~/.briefd/briefd.yaml      # edit source and api_token
```

`source` can be a local checkout of your knowledge repo (proposals become local
branches you push yourself — works offline and over a flaky VPN) or the git URL
(needs `git.token` for private HTTPS remotes; briefd pulls every 60 s).

## 3. Run as a background service (launchd)

```sh
cp deploy/local/com.briefd.serve.plist ~/Library/LaunchAgents/
sed -i '' "s|__HOME__|$HOME|g; s|__BRIEFD__|$(which briefd)|g" ~/Library/LaunchAgents/com.briefd.serve.plist
launchctl load ~/Library/LaunchAgents/com.briefd.serve.plist
tail -f ~/.briefd/briefd.log
```

`launchctl unload …` stops it. The dashboard is at <http://127.0.0.1:7788/>.

## 4. Point your projects at it

Copy `deploy/local/mcp.json` to each project as `.mcp.json` (set the token), and
replace the project's `CLAUDE.md` knowledge dump with `deploy/local/CLAUDE.md`.

## Corporate network notes

- Model download blocked? Run `briefd model pull` at home and copy
  `~/Library/Caches/briefd/models/` to the work machine; after that briefd is
  fully offline.
- Go honors `HTTPS_PROXY` / `NO_PROXY` for the git remote and the model download.
- TLS inspection (Zscaler etc.) is fine: Go uses the macOS system keychain.
- The knowledge never leaves the machine. What the agent retrieves (a bundle,
  ~2k tokens) goes to the model provider exactly as `CLAUDE.md` content does today — only less of it.

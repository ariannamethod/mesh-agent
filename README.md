# mesh-agent

Capability-broker daemon for the **arianna** Tailscale mesh.

Each node in the mesh runs one `mesh-agent` process. It:

1. Loads **slot manifests** from `~/.mesh/slots/*.toml` — these describe runnable capabilities the node exposes (training jobs, inference engines, datasets, hardware classes).
2. Serves an HTTP API bound to the node's tailnet IPv4 (the `100.x.x.x` address).
3. Discovers peer agents and caches their slot registries (Phase 2).
4. Provides an inbox for agent-to-agent messages — usable by Claude Code sessions on each node to communicate without going through git.

This is the **networking layer** of the arianna ecosystem. The compute layer (`notorch`, `metaharmonix`, `aml`) registers slots into mesh-agent so its capabilities become network-routable.

> Repo: `github.com/ariannamethod/mesh-agent` &mdash; standalone, no metaharmonix dependency. mhx is one of many consumers.

---

## Status

**Phase 1 ✓:** HTTP server, manifest loader, message inbox.
**Phase 2 ✓:** peer discovery loop (read `~/.mesh/peers.txt`, GET `/slots` every 60s, persist `peers.json`).
**Phase 3 ✓:** `/slot/{id}/invoke` async exec via `sh -c`, job manager with ring-buffer stdout/stderr capture, `/job/{id}/stream` SSE (backfill + live), `DELETE /job/{id}` kill via SIGTERM.
**Phase 4 (next):** `mesh` CLI wrapper.
**Phase 5:** cross-compile + deploy.
**Phase 6:** mhx pipeline-stage → slot auto-registration.

---

## Build

```bash
go build -o mesh-agent .
```

Cross-compile:

```bash
GOOS=linux  GOARCH=amd64 go build -o mesh-agent-linux-amd64
GOOS=linux  GOARCH=arm64 go build -o mesh-agent-linux-arm64
GOOS=darwin GOARCH=arm64 go build -o mesh-agent-darwin-arm64
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o mesh-agent-android-arm64
```

---

## Run

```bash
mesh-agent serve              # binds to first 100.x.x.x IPv4, port 4747
mesh-agent serve --port 4747  # explicit port
mesh-agent serve --bind 100.109.196.93 --port 4747
```

Smoke test the local agent:

```bash
curl http://$(tailscale ip -4):4747/
curl http://$(tailscale ip -4):4747/slots
curl http://$(tailscale ip -4):4747/presence
```

---

## Slot manifests

A manifest is a TOML file under `~/.mesh/slots/`:

```toml
[slot]
id          = "train/llama3-bpe-15m"
description = "LLaMA 3 BPE 15.7M trainer (notorch)"
exec        = "/home/ataeff/arianna/notorch/train_llama3_bpe"
async       = true
args_schema = "[steps:int, lr:float, corpus:path, merges:path]"
output      = "checkpoint .bin file path"

[requires]
ram  = "8GB"
disk = "500MB"
gpu  = false

[provides]
streaming_metrics = "loss, val, steps/s"

[ownership]
arch = ["aarch64", "x86_64"]
os   = ["linux", "darwin", "android"]
```

Register a manifest:

```bash
mesh-agent register ./train-llama3-bpe-15m.toml
```

This copies the file into `~/.mesh/slots/<id>.toml`. Restart the daemon to reload (Phase 1) or `kill -HUP` (Phase 2 will add hot-reload).

---

## API

| method | path                    | description                                            |
|--------|-------------------------|--------------------------------------------------------|
| GET    | `/`                     | health + version + slot count                          |
| GET    | `/slots`                | list local slots                                       |
| GET    | `/slot/{id}`            | manifest for one slot                                  |
| POST   | `/slot/{id}/invoke`     | spawn async job — body `{"args":["..."]}`              |
| GET    | `/jobs`                 | list all jobs (latest first)                           |
| GET    | `/job/{id}`             | job status JSON                                        |
| GET    | `/job/{id}/stream`      | SSE: backfill ring buffer, then live `log` events, finish with `state` event |
| DELETE | `/job/{id}`             | SIGTERM the running process                            |
| GET    | `/peers`                | cached peer registry                                   |
| POST   | `/msg`                  | deposit `{from,to,body}` JSON in inbox                 |
| GET    | `/presence`             | host name, version, slot count                         |

## Peer discovery

Create `~/.mesh/peers.txt` with one `host:port` per line:

```
neo:4747
intel:4747
polygon:4747
arianna-method:4747
galaxy-a07:4747
```

Lines starting with `#` are comments. The agent polls each peer's `/slots` every 60s and persists the result in `~/.mesh/peers.json`.

## Invoke example

```bash
# spawn
curl -s -X POST http://neo:4747/slot/echo/hello/invoke -d '{}' \
  | jq -r .job_id
# stream stdout
curl -N http://neo:4747/job/<id>/stream
```

---

## Why slots?

Each piece of arianna infrastructure (a notorch trainer, a Janus inference binary, an AML evaluator, a dataset slice) becomes a *named, routable capability*. The mesh stops looking like "five separate boxes I ssh into" and starts looking like one logical compute fabric where I ask for `train/llama3-bpe-15m` and the system finds a node that can run it.

This is exactly the abstraction that **metaharmonix** (`mhx`) wants for kernel-pipeline stages, but generalized across nodes. mesh-agent is independent of mhx; mhx is one of its consumers.

---

## Dependencies

- Go 1.22+
- `github.com/BurntSushi/toml` (only external dep, for manifest parsing)
- A Tailscale tailnet for transport (any node in the tailnet can reach any other)

The HTTP server is plain `net/http` — no framework, no middleware kitchen sink.

---

## License

MIT (see `LICENSE`).

---

## Deployment notes

When deploying across the mesh, two operational issues commonly appear:

**1. macOS Application Firewall** blocks unsigned binaries from accepting incoming connections, even if the port is bound. Symptoms: nc connects but curl returns "Empty reply from server". Fix:

```bash
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add ~/.local/bin/mesh-agent
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp ~/.local/bin/mesh-agent
```

Or codesign+notarize for production. Or `--bind 0.0.0.0` doesn't bypass — firewall still blocks. Workaround: disable firewall (`socketfilterfw --setglobalstate off`) on trusted Tailscale-only nodes.

**2. Linux: process dies on ssh disconnect.** `nohup ... &` + `disown` is unreliable across some shell + sshd combinations. Use `setsid -f` for full detachment, or — better — a user systemd service:

```ini
# ~/.config/systemd/user/mesh-agent.service
[Unit]
Description=arianna mesh-agent
After=network.target

[Service]
Type=simple
ExecStart=%h/.local/bin/mesh-agent serve
Restart=on-failure
StandardOutput=append:%h/.mesh/agent.log
StandardError=append:%h/.mesh/agent.log

[Install]
WantedBy=default.target
```

Then `systemctl --user enable --now mesh-agent`.

**3. macOS launchd alternative** (Neo/Intel):

```xml
<!-- ~/Library/LaunchAgents/method.arianna.mesh-agent.plist -->
<plist version="1.0"><dict>
  <key>Label</key><string>method.arianna.mesh-agent</string>
  <key>ProgramArguments</key><array>
    <string>/opt/homebrew/bin/mesh-agent</string>
    <string>serve</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/mesh-agent.log</string>
  <key>StandardErrorPath</key><string>/tmp/mesh-agent.log</string>
</dict></plist>
```

Then `launchctl load ~/Library/LaunchAgents/method.arianna.mesh-agent.plist`.

**4. Termux on Android**: `nohup` works, but the Termux process must stay alive (acquire wakelock with `termux-wake-lock`).

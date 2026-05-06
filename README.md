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

**Phase 1 (in flight):** HTTP server, manifest loader, message inbox, peers cache (skeleton).
**Phase 2:** active discovery loop (poll peers, cache, persist).
**Phase 3:** `/slot/{id}/invoke` async exec + `/job/{id}/stream` SSE.
**Phase 4:** `mesh` CLI wrapper for human-friendly invocation.
**Phase 5:** cross-compile + deploy (darwin-arm64 / linux-amd64 / linux-arm64 / android-arm64).
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

## API (Phase 1)

| method | path             | description                             |
|--------|------------------|-----------------------------------------|
| GET    | `/`              | health + version + slot count           |
| GET    | `/slots`         | list local slots                        |
| GET    | `/slot/{id}`     | manifest for one slot                   |
| GET    | `/peers`         | cached peer registry                    |
| POST   | `/msg`           | deposit `{from,to,body}` JSON in inbox  |
| GET    | `/presence`      | host name, version, slot count          |

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

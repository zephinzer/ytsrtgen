# ytsrtgen

A tiny Go HTTP service that fetches YouTube auto-generated subtitles as SRT via `yt-dlp` and returns them in the HTTP response body.

## Quick start

Build and run the hardened Docker image with the supplied Makefile:

```bash
make build         # build the image, tagged :dev and :latest
make run           # run it on :8080 with --read-only, --cap-drop=ALL, no-new-privileges
```

Then in another shell:

```bash
curl -s -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"data":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}' \
  | jq -r .srt
```

To publish a multi-arch image to `docker.io/zephinzer/ytsrtgen`:

```bash
make login         # prompts for credentials (or use $CR_PAT for ghcr.io)
make publish       # buildx multi-arch build + push in one step
```

See **[Makefile](#makefile)** for all targets and variables.

## How it works

1. Client `POST`s `{"data":"<youtube-url>"}` to `/`.
2. The server creates a fresh temp directory and invokes:
   ```
   yt-dlp --skip-download --write-auto-subs --sub-lang en \
          --sub-format srt --convert-subs srt \
          -o "%(id)s.%(ext)s" <youtube-url>
   ```
3. The resulting `*.srt` file is read from the temp dir.
4. The server responds with `{"srt":"<srt-data>"}` and removes the temp dir.

`exec.CommandContext` ties yt-dlp's lifetime to the inbound request, so a client disconnect cancels the download.

## HTTP API

### `POST /`

**Request body**

```json
{ "data": "https://www.youtube.com/watch?v=dQw4w9WgXcQ" }
```

| Field  | Type   | Required | Notes                                       |
| ------ | ------ | -------- | ------------------------------------------- |
| `data` | string | yes      | A URL that `yt-dlp` accepts (full URL form) |

**Success — `200 OK`**

```json
{ "srt": "1\n00:00:00,000 --> 00:00:02,000\nHello\n..." }
```

**Errors**

| Status | Body                                  | When                                              |
| ------ | ------------------------------------- | ------------------------------------------------- |
| 400    | `{"error":"invalid json: ..."}`       | Body is not valid JSON                            |
| 400    | `{"error":"missing 'data' field"}`    | `data` is empty or absent                         |
| 500    | `{"error":"yt-dlp failed: ..."}`      | `yt-dlp` exited non-zero (output included)        |
| 500    | `{"error":"no srt produced; ..."}`    | yt-dlp succeeded but no `.srt` was written        |
| 500    | `{"error":"mktemp: ..."}` etc.        | Filesystem / read failures                        |

Only `POST` is routed; other methods on `/` return `405`.

### Example

```bash
curl -s -X POST http://localhost:8080/ \
  -H 'Content-Type: application/json' \
  -d '{"data":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}' \
  | jq -r .srt
```

## Configuration

| Env var       | Default  | Purpose                                  |
| ------------- | -------- | ---------------------------------------- |
| `LISTEN_ADDR` | `:8080`  | Listener address passed to `http.ListenAndServe` |
| `TMPDIR`      | OS default (`/tmp` in container) | Where `os.MkdirTemp` places per-request work dirs |

## Running locally

Requires Go 1.25+ and `yt-dlp` on `PATH`.

```bash
go build -o ytsrtgen .
./ytsrtgen
```

Or:

```bash
go run .
```

## Running in Docker

```bash
docker build -t ytsrtgen .
docker run --rm -p 8080:8080 ytsrtgen
```

Recommended defense-in-depth flags (the image is built to support all of these):

```bash
docker run --rm -p 8080:8080 \
  --read-only --tmpfs /tmp \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  ytsrtgen
```

## Makefile

`make help` lists every target. Variables can be overridden on the command line, e.g. `make publish TAG=v0.1.0 REGISTRY=ghcr.io`.

### Variables

| Variable    | Default                                                  | Notes                                                                 |
| ----------- | -------------------------------------------------------- | --------------------------------------------------------------------- |
| `REGISTRY`  | `docker.io`                                              | Container registry host                                               |
| `NAMESPACE` | `zephinzer`                                              | Org / user namespace under the registry                               |
| `IMAGE`     | `ytsrtgen`                                               | Image name                                                            |
| `TAG`       | `git describe --tags --always --dirty`, else `dev`       | Tag for the versioned image; `:latest` is always also pushed/built    |
| `PLATFORMS` | `linux/amd64,linux/arm64`                                | Comma-separated buildx platforms                                      |
| `PORT`      | `8080`                                                   | Host port for `make run`                                              |
| `CR_PAT`    | _(unset)_                                                | If set and `REGISTRY=ghcr.io`, used by `make login` for non-interactive auth |

Resolved image ref: `$(REGISTRY)/$(NAMESPACE)/$(IMAGE):$(TAG)`. Run `make print` to see what would be built.

### Targets

**Go (host)**

| Target      | Purpose                                                       |
| ----------- | ------------------------------------------------------------- |
| `tidy`      | `go mod tidy`                                                 |
| `build-go`  | Static host binary at `./ytsrtgen` (same flags as the image)  |
| `test`      | `go test ./...`                                               |

**Docker — single-arch, local daemon**

| Target  | Purpose                                                                            |
| ------- | ---------------------------------------------------------------------------------- |
| `build` | `docker build` for the local arch, tags `:$(TAG)` and `:latest`                    |
| `run`   | Runs the image on `$(PORT)` with `--read-only --tmpfs /tmp --cap-drop=ALL --security-opt=no-new-privileges` |
| `shell` | Drops you into `/bin/sh` inside a fresh container for debugging                    |

**Docker — multi-arch via buildx**

| Target         | Purpose                                                                                 |
| -------------- | --------------------------------------------------------------------------------------- |
| `buildx-setup` | Creates and activates a buildx builder named `ytsrtgen` (idempotent)                    |
| `buildx`       | Multi-arch build for `$(PLATFORMS)` without pushing (cached layers only)                |
| `publish`      | Multi-arch build **and** push in one buildx invocation (canonical way to ship a manifest list) |

**Publishing**

| Target   | Purpose                                                                                      |
| -------- | -------------------------------------------------------------------------------------------- |
| `login`  | `docker login $(REGISTRY)`. If `REGISTRY=ghcr.io` and `$CR_PAT` is set, logs in non-interactively |
| `push`   | Pushes the **locally-built** `:$(TAG)` and `:latest` tags (single-arch; use `publish` for multi-arch) |

**Housekeeping**

| Target  | Purpose                                                       |
| ------- | ------------------------------------------------------------- |
| `print` | Prints resolved `IMAGE_REF_TAG`, `IMAGE_REF_LATEST`, `PLATFORMS` |
| `clean` | Removes `./ytsrtgen` and the local image tags                  |
| `help`  | Lists targets (default goal)                                   |

### Typical flows

Local development loop:

```bash
make build && make run
```

Cut a release:

```bash
git tag v0.1.0
make login
make publish               # multi-arch, tagged v0.1.0 and latest
```

One-off image to a different registry:

```bash
make publish REGISTRY=ghcr.io NAMESPACE=youruser TAG=v0.1.0
```

## Image layout / hardening

The `Dockerfile` is a two-stage build:

**Stage 1 — `golang:1.25-alpine` (build)**
- `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly` — fully static binary, no on-the-fly module mutation.
- `-trimpath -ldflags="-s -w"` — strips build host paths and debug symbols.
- BuildKit cache mounts for `/go/pkg/mod` and `/root/.cache/go-build` speed up rebuilds without bloating the image.

**Stage 2 — `alpine:3.22.4` (runtime)**
- Installs `python3`, `py3-pip`, `ca-certificates`, `tini`.
- Creates a non-root system user `app:app` with `/sbin/nologin`.
- `yt-dlp` and `bgutil-ytdlp-pot-provider` are installed into `/opt/venv` **as the `app` user** — no root-owned site-packages.
- `PATH` is prefixed with `/opt/venv/bin` so the Go binary's `exec.Command("yt-dlp", ...)` resolves.
- The Go binary is copied in `root:root` mode `0555` — executable by `app`, but `app` cannot overwrite it.
- `tini` is PID 1, so SIGTERM is forwarded to the server and reaped yt-dlp subprocesses don't become zombies.
- `TMPDIR=/tmp` is set explicitly so `--read-only` containers work when `/tmp` is a tmpfs mount.

## Project layout

```
.
├── Dockerfile       # multi-stage hardened build
├── Makefile         # build / run / publish recipes
├── README.md
├── go.mod           # module zephinzer/ytsrtgen, Go 1.25.5
├── go.sum
└── main.go          # entire server (single file)
```

## Dependencies

- [`github.com/gorilla/mux`](https://github.com/gorilla/mux) v1.8.1 — HTTP routing.
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) — installed in the runtime image's venv.
- [`bgutil-ytdlp-pot-provider`](https://pypi.org/project/bgutil-ytdlp-pot-provider/) — yt-dlp plugin for PO Token provisioning (mitigates YouTube's bot-check throttling).

## Caveats and known limitations

- **English auto-subs only.** `--sub-lang en` is hard-coded. Videos without English auto-captions will return `no srt produced`.
- **Synchronous.** Each request blocks until `yt-dlp` finishes. There is no queueing or rate limiting; put a reverse proxy in front if exposing publicly.
- **No request-size limit.** `json.NewDecoder` reads the full body. Add `http.MaxBytesReader` if you expect untrusted clients.
- **`data` is passed directly to `yt-dlp` as a CLI argument.** This is safe against shell injection (no shell is invoked — `exec.Command` is `argv`-style), but `yt-dlp` itself accepts many URL forms, including playlists and non-YouTube sites. Validate the URL upstream if you need to restrict that.
- **Single SRT.** If yt-dlp emits multiple `.srt` files, only the first match from `filepath.Glob` is returned.
- **No `/health` endpoint.** Liveness/readiness must be inferred from the listener accepting connections.

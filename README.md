![banner](./assets/banner.jpg)

# RegTool

Point npm, yarn, pip, gem, go, cargo and homebrew at the registry mirror closest to you, from one command.

[![CI](https://github.com/ZHallen122/RegTool/actions/workflows/go.yml/badge.svg)](https://github.com/ZHallen122/RegTool/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

## Features

- **File-based backends.** RegTool edits `~/.npmrc`, `~/.yarnrc`, `~/.yarnrc.yml`, `pip.conf`, `~/.gemrc`, the Go env file and `~/.cargo/config.toml` itself. It never shells out to the package manager, so it works on a machine where npm or gem is not installed at all. Homebrew is the one exception: it is configured through environment variables, so it still goes through your shell rc file and `brew`.
- **Atomic writes.** Every file is written to a temporary file in the same directory, fsynced and renamed over the target, so a reader sees either the old file or the new one — never half of each.
- **Snapshot and undo.** Every file a change is about to touch is copied into a snapshot first. `regtool undo` puts it back byte for byte, and the restore is itself snapshotted, so an undo can be undone.
- **Dry-run diff.** `--dry-run` prints the unified diff of every file that would be rewritten and writes nothing.
- **Offline embedded mirror list.** The mirror list is fetched from a configurable URL, cached locally with its ETag, and falls back to the cache and then to the copy embedded in the binary. `REGTOOL_OFFLINE=1` skips the network entirely.
- **Concurrent mirror probing.** `regtool doctor` measures every mirror in parallel; `regtool use --fastest` picks the quickest region per backend.
- **CLI and TUI.** Scriptable subcommands with `--json` for automation, an interactive interface when you would rather click around.
- **Cross platform.** Linux, macOS and Windows; every path goes through `os.UserConfigDir` and `path/filepath`.

## Install

Homebrew (macOS, Linux):

```sh
brew install ZHallen122/tap/regtool
```

Scoop (Windows):

```powershell
scoop bucket add regtool https://github.com/ZHallen122/scoop-bucket
scoop install regtool
```

Go:

```sh
go install github.com/ZHallen122/RegTool@latest
```

Or download a prebuilt archive for your platform from the
[releases page](https://github.com/ZHallen122/RegTool/releases) and put the
`regtool` binary somewhere on your `PATH`.

## Usage

Run `regtool` with no arguments to open the interactive TUI. Everything it can
do is also available as a subcommand.

Switch package managers to a region's mirrors. With no app names, every app
installed on the machine is switched:

```console
$ regtool use cn npm
APP  FROM                           TO                              RESULT
npm  https://registry.npmjs.org     https://registry.npmmirror.com  changed

snapshot 20260914T041530.123Z-3f9a1c: run 'regtool undo' to put it back

$ regtool use cn npm pip
$ regtool use us
```

Preview the change instead of making it:

```console
$ regtool use cn npm --dry-run
dry run: no configuration was changed
APP  FROM                        TO                              RESULT
npm  https://registry.npmjs.org  https://registry.npmmirror.com  would change

npm:
--- ~/.npmrc
+++ ~/.npmrc
@@ -1 +1 @@
-registry=https://registry.npmjs.org
+registry=https://registry.npmmirror.com
```

Show what every installed package manager currently points at:

```console
$ regtool status
APP  REGION  URL
npm  us      https://registry.npmjs.org/
gem  local   https://gems.example.com
```

List the mirrors RegTool knows about, optionally for one package manager:

```console
$ regtool list npm
APP  REGION  URL
npm  cn      https://registry.npmmirror.com
npm  eu      https://registry.npmjs.org
npm  us      https://registry.npmjs.org

$ regtool list
```

List the snapshots taken before each change, and roll one back:

```console
$ regtool history
ID                          CREATED                    NOTE    FILES
20260914T041530.123Z-3f9a1c 2026-09-14T04:15:30-04:00  use cn  .npmrc

$ regtool undo
restored snapshot 20260914T041530.123Z-3f9a1c (use cn)
  restored /home/you/.npmrc

$ regtool undo 20260914T041530.123Z-3f9a1c
```

Record the current registries so a later change can be compared against them:

```console
$ regtool refresh
recorded the current registries
```

Print the version, commit and build metadata:

```console
$ regtool version
regtool v1.0.0 (3f9a1c2, built 2026-09-14T04:15:30Z, darwin/arm64)
```

`status`, `list`, `use` and `history` accept `--json`, which writes the result
to stdout so it can be piped into `jq`. Errors go to stderr and the process
exits with status 1.

### Probing mirrors

`doctor` probes every mirror of every backend concurrently and reports latency
and reachability, fastest first. `use --fastest` runs the same probe and then
switches each backend to whichever region answered quickest:

```console
$ regtool doctor npm
APP  REGION  URL                             LATENCY  STATUS
npm  us      https://registry.npmjs.org      114ms    ok
npm  cn      https://registry.npmmirror.com  675ms    ok
npm  eu      https://registry.npmjs.org      120ms    ok

$ regtool use --fastest npm --dry-run
```

Concurrency and the per-probe timeout are tunable with `--concurrency` and
`--timeout`; `doctor` exits with status 1 only when no mirror answered at all.

### Where the mirror list comes from

`regtool sources` shows which mirror list is in effect and where it came from
(`remote`, `cache`, `embedded` or a file), and `regtool sources refresh` forces
a fetch. The resolution order and every related environment variable are
documented in [`docs/sources.md`](./docs/sources.md).

## Supported backends

| Backend | Config file edited | Notes |
| --- | --- | --- |
| `npm` | `$NPM_CONFIG_USERCONFIG` or `~/.npmrc` | Line based; only the bare `registry` key is rewritten, so comments, auth tokens and scoped keys survive byte for byte. |
| `yarn` | `~/.yarnrc` | Yarn 1 format; the `registry "…"` line is edited in place. |
| `yarn-berry` | `~/.yarnrc.yml` | Yarn 2+ format; the top-level `npmRegistryServer` key. Detected separately from yarn 1. |
| `pip` | `$PIP_CONFIG_FILE`, `%APPDATA%\pip\pip.ini`, `~/Library/Application Support/pip/pip.conf` or `~/.config/pip/pip.conf` | Line based; `index-url` under `[global]`. Other sections and `trusted-host` are preserved; `[global]` is created when missing. |
| `gem` | `~/.gemrc` | Round-tripped through a YAML parser, because `:sources:` is a list. The list is replaced with a single entry; quoting, indentation and comments may be rewritten or lost. |
| `go` | `$GOENV` or `<user config dir>/go/env` | Line based; the value is written as `<mirror>,direct` so modules missing from the mirror still resolve. Other variables survive byte for byte. |
| `cargo` | `~/.cargo/config.toml` | Round-tripped through a TOML parser, because a mirror is a `[source.crates-io] replace-with` replacement. Other tables keep their values, but comments are dropped and keys come back sorted. |
| `homebrew` | your shell rc file (`~/.bashrc`, `~/.zshrc`) | The exception: homebrew has no config file, so the `HOMEBREW_*` mirror variables are exported from the shell rc and `brew update` is run afterwards. Requires `brew` on the `PATH`. |

## Regions

Three regions ship out of the box: `us`, `cn` and `eu`. The mirror list lives in
[`source/sources.json`](./source/sources.json), which is embedded into the
binary with `go:embed`. At runtime RegTool tries the remote copy first (`REGTOOL_SOURCES_URL`, or the
default), then its local cache, then the embedded one, so it always works
offline. Set `REGTOOL_OFFLINE=1` to skip the network fetch entirely; see
[`docs/sources.md`](./docs/sources.md) for the full resolution order.

## Hub service (optional)

`regtool-hub` is a small HTTP service that serves the same mirror list and
checks every mirror in it on a schedule. It exists for two reasons: a mirror
list that can be updated without cutting a CLI release, and a record of which
mirrors were actually reachable from a given network over time. The CLI works
perfectly well without one — it falls back to the list embedded in its binary —
so running a hub is entirely optional.

Bring up the hub, Prometheus and a provisioned Grafana:

```sh
make compose-up      # docker compose -f deploy/docker-compose.yml up -d --build
make compose-down    # ...down -v, which also drops the check history
```

Then <http://localhost:8080/v1/sources> is the mirror list,
<http://localhost:9090> is Prometheus and <http://localhost:3000> is Grafana,
which opens straight onto a "RegTool Hub" dashboard with no login. The image is
also published to `ghcr.io/zhallen122/regtool-hub` and a standalone binary ships
in the `regtool-hub_<version>_<os>_<arch>` release archive.

### Endpoints

| Endpoint | What it returns |
| --- | --- |
| `GET /v1/sources` | The mirror list, in exactly the shape of [`source/sources.json`](./source/sources.json). Carries a strong `ETag` and `Cache-Control: public, max-age=300`, and answers `If-None-Match` with `304 Not Modified`. |
| `GET /v1/health` | The latest check for every mirror: `{app, region, url, ok, latency_ms, status_code, error, checked_at}`. |
| `GET /v1/health/history?app=&region=&limit=` | Past checks, newest first. `app` and `region` narrow the result, `limit` defaults to 100 and is clamped to 1000. |
| `GET /healthz` | Liveness. Touches nothing, so a slow database does not get the process restarted. |
| `GET /readyz` | Readiness. `503` until the first check has finished or taken 30 seconds. |
| `GET /metrics` | Prometheus exposition. |

### Metrics

| Metric | Type | Labels |
| --- | --- | --- |
| `regtool_hub_mirror_up` | gauge | `app`, `region`, `url` |
| `regtool_hub_mirror_latency_seconds` | gauge | `app`, `region`, `url` |
| `regtool_hub_checks_total` | counter | `result` (`ok` / `error`) |
| `regtool_hub_check_duration_seconds` | histogram | — |
| `regtool_hub_http_requests_total` | counter | `route`, `method`, `code` |
| `regtool_hub_http_request_duration_seconds` | histogram | `route` |

The Go runtime and process collectors are registered too, so `go_goroutines`,
heap usage and file descriptors are on the same scrape.

### Configuration

Every flag has a `REGTOOL_HUB_*` environment variable behind it, so the
container needs no entrypoint script.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--addr` | `REGTOOL_HUB_ADDR` | `:8080` |
| `--sources` | `REGTOOL_HUB_SOURCES` | the list embedded in the binary |
| `--db` | `REGTOOL_HUB_DB` | `hub.db` (`/data/hub.db` in the image) |
| `--check-interval` | `REGTOOL_HUB_CHECK_INTERVAL` | `5m` |
| `--check-timeout` | `REGTOOL_HUB_CHECK_TIMEOUT` | `5s` |
| `--retention` | `REGTOOL_HUB_RETENTION` | `7d` |
| `--log-level` | `REGTOOL_HUB_LOG_LEVEL` | `info` |

Results go into a SQLite file, which needs no server of its own; anything older
than the retention window is swept after each check. Logs are `log/slog` JSON,
and `SIGINT` or `SIGTERM` stops the checker and drains in-flight requests for up
to ten seconds.

### Pointing the CLI at a hub

Set `REGTOOL_SOURCES_URL` to the hub's `/v1/sources` and the CLI fetches its
mirror list from there instead of the default remote, falling back to the
embedded copy if the hub is unreachable:

```sh
export REGTOOL_SOURCES_URL=http://localhost:8080/v1/sources
regtool list npm
```

## Demo

<!-- TODO: asciinema/VHS demo -->

## Development

```sh
make build      # build ./regtool with version, commit and date injected
make hub        # build ./regtool-hub
make hub-run    # run the hub locally with debug logging
make compose-up # hub + Prometheus + Grafana (make compose-down to tear down)
make test       # go test -race ./...
make vet        # go vet ./...
make lint       # golangci-lint v2.5.0
make snapshot   # goreleaser snapshot build into ./dist
make install    # go install with the same ldflags
```

CI runs lint on Linux and the full `-race` test suite on Linux, macOS and
Windows. End-to-end CLI behaviour is covered by
[testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)
files under `internal/cli/testdata/script`.

Releases are cut by pushing a `v*` tag: `.github/workflows/release.yml` runs
goreleaser, which builds six binaries (linux/darwin/windows × amd64/arm64),
attaches archives, checksums and SBOMs to the GitHub release, and updates the
[Homebrew tap](https://github.com/ZHallen122/homebrew-tap) and the
[Scoop bucket](https://github.com/ZHallen122/scoop-bucket).

## Contributing

Bug reports, feature requests and pull requests are welcome — see
[CONTRIBUTING.md](./CONTRIBUTING.md).

## License

[MIT](./LICENSE)

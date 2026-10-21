# Changelog

All notable changes to straum-console are documented here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project uses [Semantic Versioning](https://semver.org/). Before 1.0 a minor
version may change flags, variables and pages. The `/api/v1` documents have
their own schema version (`docs/state-json.md`).

## [Unreleased]

### Added

- Polling: one request per configured node per interval (`--node`,
  `--interval`, default 2 s), through straum-cli's `metrics` package. Only
  the series the core declares stable are read; nothing but `GET /metrics`
  is ever sent to a node.
- The `/api/v1` state, events and stream (schema version 1,
  `docs/state-json.md`), on loopback only (`--listen`, default
  `127.0.0.1:8080`). Every value carries its node and the time it was read;
  a node that does not answer is `unknown`, with its last values and their
  age; no health verdict is drawn.
- Six web pages at `/`: Overview, Cluster & Leader, Replication &
  Durability, Events, Configuration, Not exposed by the core. Light and
  dark, in the Straum docs site's look; the fonts are embedded.
- `STRAUM_CONSOLE_NODES`, `STRAUM_CONSOLE_INTERVAL_MS`,
  `STRAUM_CONSOLE_LISTEN` and `STRAUM_CONSOLE_DEBUG_LISTEN` in place of the
  flags; an unknown `STRAUM_CONSOLE_*` variable is a startup error.
- Keyboard shortcuts: `1`–`6` open the pages, `/` filters (`Esc` clears
  it), `t` switches the theme, `?` lists the keys.
- A filter on every page: nodes by id, address, state or error, events by
  name, node or text; it stays in the URL (`#/events?q=leading`).
- `--debug-listen`: `net/http/pprof` on a second loopback address, off by
  default.
- `--version`.
- Release builds with GoReleaser for linux, darwin and windows, each on
  amd64 and arm64, and an example systemd unit
  (`docs/straum-console.service`).

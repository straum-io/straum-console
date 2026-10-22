# <img src="docs/assets/mark.svg" width="28" alt=""> straum-console

[![CI](https://github.com/straum-io/straum-console/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/straum-io/straum-console/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
![Status: experimental](https://img.shields.io/badge/status-experimental-orange)

A web console for [Straum](https://github.com/straum-io/straum) clusters:
every node on one screen, with each value showing where it came from and
how old it is.

**Docs:** [straum-io.github.io/ecosystem/straum-console](https://straum-io.github.io/ecosystem/straum-console/)

> **Status: early.** It reads the nodes and shows them on six pages
> (Overview, Cluster & Leader, Replication & Durability, Events,
> Configuration, Not exposed by the core), with a read-only JSON API, on
> loopback. Experimental, like the broker.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/overview-light.png">
  <img src="docs/assets/overview-dark.png" alt="The Overview of a three-node cluster on one machine: node 1 reports leading, node 2 is unknown after it was stopped and keeps its last values dimmed with their age; recent events: node 2 unreachable, a marker where the console restarted, then a leading change after a handoff and the highest slot changing">
</picture>

## Why

Operating a cluster means watching every node at once: who sequences the
log, how far each one has applied and made durable, who stopped answering.
Today that takes
[straum-cli](https://github.com/straum-io/straum-cli) `status` in a loop,
or a Prometheus scraper and a Grafana board you build yourself. The console
is meant to put the cluster on one live page with a short history, as one
binary that runs next to the cluster.

## What it will do

- **Shows only what the broker promises.** It reads the metrics the broker
  declares stable, nothing else, through the same Go package as
  `straum-cli`. A screen the stable metrics cannot fill says so instead of
  guessing.
- **Every value has a source and an age.** Each number carries its node,
  its address and when it was read. A node that stops answering turns
  `unknown`, and its last values stay visible with their age, never as
  current.
- **A short history, in memory.** The last 1,800 rounds of every stable
  series (1 h at the default 2 s), charted per node: the commit rate,
  slot and leading, and what each node has not yet synced or committed.
  A rate is never drawn across a gap, a restart or a leader change.
- **No health verdict.** It reports what each node says, including two
  nodes that both claim to lead. It never paints the cluster "healthy".
- **Read-only.** It sends nothing to a node except `GET /metrics`.
  Administration waits until the broker has an admin API and
  authentication.
- **Loopback unless signed in.** It listens on `127.0.0.1:8080` by
  default. It leaves loopback only with TLS, a password file in the
  broker's format and the host names browsers use, all three; otherwise
  `ssh -L 8080:127.0.0.1:8080 <console-host>` is the way in.

## Install

No release is published yet, so for now build from source (below). Once
there is one, any of these works:

```sh
# Linux or macOS: the release archive for this machine, checked against
# the release's checksums; --user installs into ~/.local, --systemd adds the
# example unit (Linux, not enabled).
curl -fsSL https://raw.githubusercontent.com/straum-io/straum-console/main/install.sh | sh

# Docker on a Linux host, with the host's network (the console listens on
# loopback unless signed in; Docker Desktop's host networking on macOS and
# Windows is not tested):
docker run --network host straumio/straum-console --node 0=127.0.0.1:9100

# The archives themselves, for Linux, macOS and Windows on amd64 and arm64:
# https://github.com/straum-io/straum-console/releases
```

`uninstall.sh` removes what `install.sh` put down (it keeps a list), the
systemd unit included. A published port (`docker run -p`) reaches the
console only when it signs in (below), listening on `:8443` inside the
container; on loopback, the container's loopback is not the host's.

From source, with Go 1.26:

```sh
make build    # bin/straum-console
make image    # straum-console:dev, for this machine's CPU (CONTAINER=podman works too)
```

## Getting started

You need a Straum cluster with its metrics endpoints on
(`STRAUM_CLUSTER_METRICS_LISTEN` on each node).

```sh
bin/straum-console --node 0=127.0.0.1:9100,1=127.0.0.1:9101,2=127.0.0.1:9102
```

Each node is `id=host:port`, the id the node should report. A node's
metrics listener is plain HTTP and loopback-only unless the node is told
otherwise, so the console reaches it in one of the ways the broker allows:
on the host of a single-host cluster, through an SSH local forward to each
node (`ssh -L 9100:127.0.0.1:9100 node-a`), or on an isolated network
where the nodes set `STRAUM_CLUSTER_UNAUTHENTICATED_NETWORK=1`, on the
broker's own terms.

The console polls every 2 s (`--interval`) and serves on `127.0.0.1:8080`
(`--listen`, loopback only): open http://localhost:8080. Press `?` for the
keys: `1`–`6` for the pages, `/` to filter what a page lists; the same
list turns them off. The same data
is JSON:

```sh
curl -s localhost:8080/api/v1/state      # every node, as last read
curl -sN localhost:8080/api/v1/stream    # the state each round, and what changed
curl -s 'localhost:8080/api/v1/events?after=0'
```

[docs/state-json.md](docs/state-json.md) is the schema. Each change is also
logged once, as a JSON line on stderr. `STRAUM_CONSOLE_NODES`,
`STRAUM_CONSOLE_INTERVAL_MS`, `STRAUM_CONSOLE_LISTEN`,
`STRAUM_CONSOLE_DEBUG_LISTEN`, `STRAUM_CONSOLE_TLS_CERT`,
`STRAUM_CONSOLE_TLS_KEY`, `STRAUM_CONSOLE_PASSWORD_FILE` and
`STRAUM_CONSOLE_PUBLIC_HOSTS` stand in for the flags; never put them in a
straum server's environment, which refuses unknown `STRAUM_*` variables.
[docs/straum-console.service](docs/straum-console.service) is an example
systemd unit. `--debug-listen 127.0.0.1:6060` serves Go's `pprof` on a
second loopback address, off by default: a local diagnostic that any local
process can use to start profiles, so leave it off in normal running.

### Beyond loopback

The console signs people in before it leaves loopback. It needs all of:

```sh
bin/straum-console --node 0=127.0.0.1:9100 \
  --listen 0.0.0.0:8443 \
  --tls-cert console.pem --tls-key console.key \
  --password-file console.passwords \
  --public-host console.example.org
```

- `--password-file` is the broker's format, one `user:hash` per line, the
  hash from `straum-server hash-password` (a password on stdin). Only that
  command's parameters are accepted (argon2id, m=19456, t=2, p=1), so a
  refusal costs the same as an acceptance whatever the user name.
- `--public-host` lists the names browsers use; any other `Host` is
  refused (421), which keeps a web page from reaching the console through
  a name of its own. `localhost` keeps working.
- Without the password file the console stays on loopback, and the
  password file needs TLS.

A sign-in is checked one at a time, each check holding about 19 MiB for
its duration; at most 64 wait, and one address (an IPv6 /64) gets 10
attempts a minute. A session lasts 12 hours, in memory: a restart signs
everyone out. `kill -HUP` re-reads the password file and the certificate;
a user taken out of the file is signed out at the next request.

A release is a pushed `v*` tag on `main`. The workflow builds the archives,
`checksums.txt` and a signed build provenance for each
(`gh attestation verify <archive> -R straum-io/straum-console`), and leaves
the release as a draft that a person reads and publishes; publishing it
pushes the image to Docker Hub (`straumio/straum-console`: `X.Y.Z`, `X.Y`
and `latest` for a release; a prerelease gets only its own version). A bad
release is withdrawn by unpublishing it (its tag stays, so the archives
keep their provenance) and, for the image, by deleting its version tag on
Docker Hub and pointing `X.Y` and `latest` back at the previous release;
it is fixed by a new patch version; `CHANGELOG.md` says what
changed and why.

`make check` runs the linters and tests; `make e2e` runs a real
three-node cluster built from a core checkout at `../straum`, including a
killed leader and a frozen node. `make soak` holds 5 nodes and 64 streams
for an hour against the memory and CPU budgets; `make bench` measures a
round. What was measured is in [docs/performance.md](docs/performance.md). The web pages
are Preact in TypeScript under `web/`; the built files in `web/dist` are
committed, so `go build` needs no Node. After changing `web/src`, run
`make web` (Node 22) and `make web-test` (Playwright).

## More

- [docs/state-json.md](docs/state-json.md): the read-only JSON API, version 1.
- [Straum](https://github.com/straum-io/straum): the broker, and its
  [metrics endpoint](https://github.com/straum-io/straum/blob/main/docs/configuration.md).
- [The console's page](https://straum-io.github.io/ecosystem/straum-console/)
  on the Straum docs site. The console takes the site's look: the colors
  and fonts of [`tokens.css`](https://github.com/straum-io/straum-io.github.io/blob/17cfc25/src/styles/tokens.css)
  at `17cfc25`, dark by default.

Apache License 2.0.

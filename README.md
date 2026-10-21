# <img src="docs/assets/mark.svg" width="28" alt=""> straum-console

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
- **No health verdict.** It reports what each node says, including two
  nodes that both claim to lead. It never paints the cluster "healthy".
- **Read-only.** It sends nothing to a node except `GET /metrics`.
  Administration waits until the broker has an admin API and
  authentication.
- **Stays on loopback.** It listens on loopback only (default
  `127.0.0.1:8080`) and refuses any other address. Open it from elsewhere with
  `ssh -L 8080:127.0.0.1:8080 <console-host>`.

## Install

No release is published yet, so for now build from source (below). Once
there is one, any of these works:

```sh
# Linux or macOS: the release archive for this machine, checked against
# the release's checksums; --user installs into ~/.local, --systemd adds the
# example unit (Linux, not enabled).
curl -fsSL https://raw.githubusercontent.com/straum-io/straum-console/main/install.sh | sh

# Docker on a Linux host, with the host's network (the console listens on
# loopback only; Docker Desktop's host networking on macOS and Windows is
# not tested):
docker run --network host straumio/straum-console --node 0=127.0.0.1:9100

# The archives themselves, for Linux, macOS and Windows on amd64 and arm64:
# https://github.com/straum-io/straum-console/releases
```

`uninstall.sh` removes what `install.sh` put down (it keeps a list), the
systemd unit included. A published port (`docker run -p`) cannot reach the
console: inside the container's own network its loopback is not the host's,
and the console does not listen beyond loopback, since it has no
authentication.

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
keys: `1`–`6` for the pages, `/` to filter what a page lists. The same data
is JSON:

```sh
curl -s localhost:8080/api/v1/state      # every node, as last read
curl -sN localhost:8080/api/v1/stream    # the state each round, and what changed
curl -s 'localhost:8080/api/v1/events?after=0'
```

[docs/state-json.md](docs/state-json.md) is the schema. Each change is also
logged once, as a JSON line on stderr. `STRAUM_CONSOLE_NODES`,
`STRAUM_CONSOLE_INTERVAL_MS`, `STRAUM_CONSOLE_LISTEN` and
`STRAUM_CONSOLE_DEBUG_LISTEN` stand in for the flags; never put them in a
straum server's environment, which refuses unknown `STRAUM_*` variables.
[docs/straum-console.service](docs/straum-console.service) is an example
systemd unit. `--debug-listen 127.0.0.1:6060` serves Go's `pprof` on a
second loopback address, off by default: a local diagnostic that any local
process can use to start profiles, so leave it off in normal running.

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

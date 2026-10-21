# <img src="docs/assets/mark.svg" width="28" alt=""> straum-console

A web console for [Straum](https://github.com/straum-io/straum) clusters:
every node on one screen, with each value showing where it came from and
how old it is.

> **Status: early.** The backend reads the nodes and logs what changes;
> the HTTP API and the web pages are not built yet. The picture below is a
> design mockup, not a screenshot.

<picture>
  <source media="(prefers-color-scheme: light)" srcset="docs/assets/overview-light.png">
  <img src="docs/assets/overview-dark.png" alt="Design mockup of the Overview screen: three node cards, two observed and one unknown with its last values and their age, and a list of recent events">
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

## Getting started

You need Go 1.26 and a Straum cluster with its metrics endpoints on
(`STRAUM_CLUSTER_METRICS_LISTEN` on each node).

```sh
make build
bin/straum-console --node 0=127.0.0.1:9100,1=127.0.0.1:9101,2=127.0.0.1:9102
```

Each node is `id=host:port`, the id the node should report. The console
polls every 2 s (`--interval`) and logs, as JSON lines on stderr, each node
that turns observed or unknown, a node reporting another id, and which
nodes report leading. `STRAUM_CONSOLE_NODES` and
`STRAUM_CONSOLE_INTERVAL_MS` stand in for the flags; never put them in a
straum server's environment, which refuses unknown `STRAUM_*` variables.

Until the web pages exist, `straum-cli status` shows the cluster in a
terminal:

```sh
straum-cli status --node 127.0.0.1:9100,127.0.0.1:9101,127.0.0.1:9102
```

`make check` runs the linters and tests; `make e2e` runs a real
three-node cluster built from a core checkout at `../straum`.

## More

- [docs/state-json.md](docs/state-json.md): the read-only JSON API, version 1.
- [Straum](https://github.com/straum-io/straum): the broker, and its
  [metrics endpoint](https://github.com/straum-io/straum/blob/main/docs/configuration.md).
- The look comes from the Straum docs site, which is not published yet:
  the same colors and fonts (`src/styles/tokens.css` in straum-docs), dark
  by default.

Apache License 2.0.

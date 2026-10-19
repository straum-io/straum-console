# straum-console

The administrative web console for [Straum](https://github.com/straum-io/straum),
frontend and backend in one product.

> **Status: not started.** There is no code yet. This page says what the
> console is for, so nobody mistakes it for something that already runs.

## Why

Operating a cluster means watching every node at once: who sequences the
log, how far each replica lags, which connections are open, what was
refused and why. Today that takes
[straum-cli](https://github.com/straum-io/straum-cli) `status` and a
metrics scraper. The console is meant to show it in one place, and later
to run the administrative operations the core exposes.

## What it will do

- **Observe the cluster** from the metrics the core declares stable: every
  value tagged with its node and the time it was read, and no health
  verdict invented from the numbers.
- **Run administrative operations** only through operations the core
  defines. Each effect goes through the core's log; the console never
  changes cluster state on its own.
- **Authenticate first.** No administrative surface leaves loopback until
  it has authentication.

The core has no administrative API yet, so the first version can only
observe.

## Getting started

Nothing to run yet. Follow the [Issues](https://github.com/straum-io/straum-console/issues),
or try [straum-cli](https://github.com/straum-io/straum-cli) for the
cluster view it already provides.

## More

- [Straum](https://github.com/straum-io/straum): the broker this console is for.
- The architecture proposal lives in the private
  `straum-io/straum-dev-plan` repository, `plans/straum-console.md`.

Apache License 2.0.

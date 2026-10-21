# Performance and resource use

What was measured against the budgets of the console's plan
(straum-dev-plan `plans/straum-console.md` §14), how, and on what. These
numbers come from a laptop running macOS, so they are **preliminary**: the
Straum project accepts performance numbers only from a quiet Linux host.

## Environment

| | |
|---|---|
| Machine | Apple M1 (8 cores), 16 GiB, macOS 27.0.1, on battery and charging |
| Go | 1.26.5 darwin/arm64 |
| Console | `f59f37b` plus the Phase F changes (straum-console#22); interval 2 s, request timeout 1 s |
| Date | 2026-10-21 |

The soak ran in parallel with other work on the machine for part of its
hour, and the machine slept during it: the hour is awake time, which Go's
clock counts and `ps` CPU time matches.

## The soak (`make soak`)

5 fixture nodes serving the core's own metrics text (16 KiB each), with
positions that advance every round and a leader that moves every 30 s; the
console binary polling them every 2 s; 64 streams reading
`/api/v1/stream` the whole time. Sampled every 10 s: resident memory
(`ps`), CPU time (`ps`), heap in use (`/debug/pprof/heap`).

| Budget (§14) | Target | Measured | |
|---|---|---|---|
| Backend RSS | ≤ 40 MB steady | 21.5 MiB at most (22,048 KiB) | met |
| Backend CPU | ≤ 1 % of one core | 0.40 % over 3,606 s | met |
| No growth | heap at hour's end = earlier | median heap in use 3.84 MB in the second quarter, 3.93 MB in the last (+2.3 %; the check allows 20 %) | met |
| Browser clients | 64 concurrent | 64 streams opened; 246 MB read in all, about what 64 streams receive in an hour (1,800 states of about 2 KB each, plus events). This run predates the soak's check that every stream stays open to the end, added afterwards | met, by inference |

360 samples; the raw series is not kept in the repository. Rerun with
`SOAK_OUT=soak.csv make soak`.

## Per-round cost (`make bench`)

`make bench`: 10 runs each, medians. The machine was not idle: the load
average was 6–8 during the run (macOS background services and other
sessions), so every result here is environment-dependent as well as
preliminary.

| Benchmark | Median | Spread (max − min) | Memory per op | Class |
|---|---|---|---|---|
| A round's own work, 5 nodes: next state, events, one JSON encode | 13.3 µs | 6 % | 12,950 B, 145 allocations | environment-dependent |
| The same, 15 nodes | 34.8 µs | 7 % | 36,195 B, 406 allocations | environment-dependent |
| Publishing a round to 64 streams that keep up | 16.7 µs | 38 % | 12,037 B, 136 allocations | environment-dependent, noisy |
| A real `Collect` of 5 nodes over loopback (HTTP and straum-cli's parsing of 16 KiB each) | 585 µs | 11 % | 559,529 B, 2,760 allocations | environment-dependent |

At the default interval that is one `Collect` and one round every 2 s,
about 0.03 % of a core for that work alone. What the rest of the soak's
0.40 % is spent on (the Go runtime, the HTTP server, the 64 streams) is not
measured.

## Other §14 budgets

| Budget | Target | Measured | |
|---|---|---|---|
| Frontend bundle | ≤ 60 KB gzip JS + CSS | 15,775 B (`make web-size`, CI) | met |
| SSE per round | ≤ 4 KiB per client at 5 nodes | 1,976 B (`TestStreamRoundSize`) | met |
| Nodes supported | 3–15 | 15 fixture nodes observed in one round (`TestFifteenNodes`); 3 real nodes (`make e2e`) | met |
| Event payload | ≤ 1 KiB | node error text capped at 256 B where it enters the state | met by construction |
| Event retention | 2000 events | a ring of 2000 (`TestRing`) | met |
| Allocations per round | ≤ 1 parse buffer per node, 1 state encode | 2,762 allocations and 560 KB per `Collect` of 5 nodes | **not met** |
| Scrape load on a node | ≤ 0.5 req/s, ≤ 1 concurrent | 0.5 req/s at the default interval, one request per node in flight, by construction | met; the cost on the node is not measured (below) |
| History | 1 h | not applicable: schema v1 keeps no history | — |

The allocations budget was written for a parser in the console. The
parsing is straum-cli's `metrics` package (CON-D-1 A), which reads each
body whole and builds a map of every sample. At 0.5 rounds a second that
is about 280 KB of garbage a second, and the soak shows it does not move
memory or CPU past their budgets. Meeting the number would be a change in
straum-cli, not here (straum-io/straum-cli#17).

## Failure modes (plan §15)

Against a real three-node cluster (`make e2e`), run three times, all
passed. Locally the core binary came from a checkout at `ec8cc71`, a branch
not yet on the core's `main`; CI runs the same tests against the core's
`main` on every pull request.


- a killed node is `unknown` in the next round, with its last values; it is
  `observed` again after a restart;
- SIGUSR1 to the leader: `cluster.leading.changed` names another node;
- `kill -9` of the leader: `console.node.unreachable`, then another node
  reports leading, and the killed node is never shown as leading;
- a node frozen with SIGSTOP: `unknown` with "no answer within" as the
  reason, `observed` again after SIGCONT.

In the browser (`make web-test`): a 10 s network cut (a TCP proxy dropping
every connection) turns the view not current, and after it the missed
event raised during the cut (by its id, higher than any before the cut) is
replayed, with no gap; a console restart shows a "console restarted" gap.

## Not measured

- **Non-interference with the broker.** Plan §15 asks for PUBACK p50/p99
  and commit throughput with the console polling versus not, by the core's
  overhead method (alternating rounds, D-127, D-129). That needs the core's
  benchmark setup on a quiet Linux host (straum-io/straum#51); until it is
  run, the 2 s default interval is a placeholder.
- **Update latency** (scrape done → browser render, p95 ≤ 250 ms): not
  measured. The browser tests see a leading change within 2.5 s at a 1 s
  interval.
- **Leader display ≤ 3.5 s at the defaults** (§15): not measured as a
  bound. The e2e tests wait up to 20–30 s for a takeover or a handoff.
- **Publish load** (`straum-cli pub` in a loop during the e2e, §15): not
  run. No console page shows throughput (CON-D-1 A).
- **Displayed values against the raw bodies** (§15, a test hook recording
  each round's body): not built. The state's values are straum-cli's
  parse, pinned by its own tests and by `TestGoldenState` here.
- **The allocations budget** is not met (above).
- **Linux numbers.** Everything above is from macOS.

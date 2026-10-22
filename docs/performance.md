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

### With history (straum-console#32)

The same soak, on 2026-10-21/22 at revision `9dfe69a` plus the history
change, for 75 minutes so the 1,800-round ring fills, with 64 history
readers beside the 64 streams. Each reader acts as an open Replication &
Durability page, the page with the most charts: one request for its three
series over the 1 h range, then every 15 s only the rounds `since` its last
(`web/src/chart.tsx`). The binary predates one later server change:
the series of a request are read under one lock rather than one each.

| Budget (§14) | Target | Measured | |
|---|---|---|---|
| Backend RSS | ≤ 40 MB steady | 23.2 MiB at most (23,792 KiB) | met |
| Backend CPU | ≤ 1 % of one core | 0.56 % over 4,507 s (15-minute windows 0.50–0.57 %) | met |
| No growth | heap at the end = earlier | median heap in use 5.37 MB in the second quarter, 5.50 MB in the last (+2.3 %) | met |
| History requests | none fail | none failed; every stream stayed open | met |

The design took three failed runs to get there, all on this machine:

| Readers fetch | CPU | Heap, 2nd → last quarter | |
|---|---|---|---|
| The whole 1 h of `commit_rate` every 5 s | 6.12 % | 6.46 → 8.45 MB (+31 %) | failed |
| `since`, one series every 5 s (stopped at 3,225 s: it modelled one chart per page) | 1.00 % | — | stopped |
| `since`, three series in one request every 5 s | 1.07 % | 5.18 → 5.44 MB (+5 %) | failed |
| `since`, three series in one request every 15 s | 0.56 % | 5.37 → 5.50 MB (+2.3 %) | met |

A 60 s CPU profile during the third run spent almost all its samples in
system calls and thread wakeups for loopback writes; building and
encoding the history documents did not show. The load is stricter than
§14's CPU condition (10 browsers): 64 readers. The pre-history soak
(0.40 %) ran on another day, so the difference is not the cost of history
alone.

## Per-round cost (`make bench`)

`make bench`'s benchmarks on 2026-10-22, at revision `26ad88a` with
straum-cli at `985ddfe` (straum-cli#21), against a test binary built with
the previous straum-cli (`553a5c1`): the two binaries run alternately,
`-test.count 2` each, for 5 rounds (10 runs each), medians. Go 1.26.5
darwin/arm64, on battery. The machine was not idle (load average 2.5–5.3
during the run), so every result here is environment-dependent as well as
preliminary.

| Benchmark | Median | Spread (max − min) | Memory per op | Class |
|---|---|---|---|---|
| A round's own work, 5 nodes: next state, events, one JSON encode | 21.4 µs | 1 % | 12,956 B, 145 allocations | environment-dependent |
| The same, 15 nodes | 55.6 µs | 14 % | 36,201 B, 406 allocations | environment-dependent |
| Publishing a round to 64 streams that keep up | 27.8 µs | 42 % | 12,038 B, 136 allocations | environment-dependent, noisy |
| A real `Collect` of 5 nodes over loopback (HTTP and straum-cli's parsing of 16 KiB each) | 556 µs | 14 % | 266,362 B, 1,457 allocations | environment-dependent |

Against the previous straum-cli in the same rounds, `Collect` went from
873 µs to 556 µs (−36 %), 548.5 KiB to 260.1 KiB (−53 %) and 2,761 to
1,457 allocations (−47 %). The other three run code that did not change;
benchstat still called a round of 5 nodes (+2.7 %) and of 15 (+4.2 %)
slower, and the publishing benchmark unchanged. Those two shifts are put
down to the machine's load, which rose during the run, not measured as
such. The first run of this
table (2026-10-21, a load average of 6–8) had other medians: numbers from
different days are not comparable here.

At the default interval that is one `Collect` and one round every 2 s,
about 0.03 % of a core for that work alone. What the rest of the soak's
0.40 % is spent on (the Go runtime, the HTTP server, the 64 streams) is not
measured.

## Sign-in (CON-D-8)

The console binary with TLS and a password file on loopback, one fixture
node, `curl` signing in; resident memory by `ps` every 50 ms, the heap by
`/debug/pprof/heap`. 2026-10-22, macOS, on battery.

| | Resident memory |
|---|---|
| Idle, sign-in on | 14.3–15.2 MiB |
| During one check (argon2id, m = 19 MiB) | 37.6 MiB at most |
| After 5 sign-ins, then 64 at once from one address (5 more hashed, 59 refused by the rate limit) | 42.1 MiB; heap in use 3.3 MB, 28.4 MB released to the OS |
| 20 s later | 36.3 MiB |

The first build kept each check's 19 MiB: 56 MiB after five sign-ins and
80 MiB after the burst, staying there. Each check now returns its memory
before the next may start (`debug.FreeOSMemory`); the heap figures above
show it released. macOS keeps released pages in a process's resident
size until it needs them back, which is why the last two rows stay above
the idle figure; Linux is expected to drop them at once, not measured.

A check adds about 22–23 MiB of resident memory to whatever the console
holds (the 19 MiB of the hash and the runtime around it, from idle to
peak above): at the history soak's load (23.2 MiB) one sign-in would
briefly take the console to about 45 MiB. The budget is 40 MB steady,
plus at most one check while a sign-in runs (CON-D-9 A), so this is
within it. Sign-ins are not part of the soak.

## Other §14 budgets

| Budget | Target | Measured | |
|---|---|---|---|
| Frontend bundle | ≤ 60 KB gzip JS + CSS | 20,259 B (`make web-size`, CI) | met |
| SSE per round | ≤ 4 KiB per client at 5 nodes | 1,976 B (`TestStreamRoundSize`) | met |
| Nodes supported | 3–15 | 15 fixture nodes observed in one round (`TestFifteenNodes`); 3 real nodes (`make e2e`) | met |
| Event payload | ≤ 1 KiB | node error text capped at 256 B where it enters the state | met by construction |
| Event retention | 2000 events | a ring of 2000 (`TestRing`) | met |
| Allocations per round | ≤ 1 parse buffer per node, 1 state encode | 1,457 allocations and 266 KB per `Collect` of 5 nodes (2,761 and 560 KB before straum-cli#21) | **not met** |
| Scrape load on a node | ≤ 0.5 req/s, ≤ 1 concurrent | 0.5 req/s at the default interval, one request per node in flight, by construction | met; the cost on the node is not measured (below) |
| History | 1,800 rounds (1 h at 2 s), at most 15 nodes (CON-D-7 A) | a ring of 1,800 rounds allocated once (`TestHistoryRing`); 16 nodes refused at startup (`TestNodeCap`); the soak above | met |

The allocations budget was written for a parser in the console. The
parsing is straum-cli's `metrics` package (CON-D-1 A). Since
straum-cli#21 it parses each response as it arrives and skips comments
and labelled samples without allocating; it still makes a string for
every sample it keeps. At 0.5 rounds a second that is about 130 KB of
garbage a second, and the soak shows it does not move memory or CPU past
their budgets. Meeting the number would be a further change in
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

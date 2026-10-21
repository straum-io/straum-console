# `/api/v1`: the state and event schemas

The console serves two JSON documents: the **state**, a snapshot of every
configured node, and the **event**, a change the console saw between two
polls. Both are read-only. The console sends nothing to a node except
`GET /metrics`, and it reads only the series the core declares stable.

| Where | What |
|---|---|
| `GET /api/v1/state` | The latest state. |
| `GET /api/v1/stream` | Server-sent events: `event: state` carries a state after every poll round, `event: event` carries one event with `id:` set to the event's `id`, and `event: gap` says events were missed. |
| `GET /api/v1/events?after=<id>&limit=<n>` | `{"events": [...]}`: the events after `id`, oldest first, at most `limit` (≤ 2000). |

## Compatibility

`schema_version` is 1, for both documents.

These changes keep the version:
- adding a field;
- adding a metric to `metrics`, after the core declares it stable;
- adding an event name.

These changes bump the version:
- removing or renaming a field or an event name;
- changing a field's type or meaning;
- removing a metric.

A consumer should ignore fields and event names it doesn't know.

Each name in `metrics` is a series the core declares stable, in
`docs/configuration.md` of the straum repository, "Stable metrics" (D-141
A). Every value is the node's own number, as the node rendered it. Fields
the console computes from those numbers are listed under "Derived", and
none of them is a health verdict.

## State

```json
{
  "schema_version": 1,
  "epoch": "9f2c4e1ab07d5e33",
  "round": 1801,
  "generated_at": "2026-10-21T12:00:02.010Z",
  "interval_ms": 2000,
  "cluster": {
    "configured": 3,
    "observed": 2,
    "max_slot": 4,
    "leading": [1]
  },
  "nodes": [
    {
      "id": 0,
      "address": "10.0.0.1:9100",
      "state": "observed",
      "observed_at": "2026-10-21T12:00:02.004Z",
      "age_ms": 6,
      "latency_ms": 3,
      "metrics": {
        "straum_applied": 120,
        "straum_commit": 120,
        "straum_lag": 0,
        "straum_leading": 0,
        "straum_log_durable": 120,
        "straum_log_end": 120,
        "straum_node": 0,
        "straum_outbox_capacity": 341,
        "straum_outbox_entries": 0,
        "straum_serving": 1,
        "straum_slot": 4
      }
    },
    {
      "id": 1,
      "address": "10.0.0.2:9100",
      "state": "observed",
      "observed_at": "2026-10-21T12:00:02.005Z",
      "age_ms": 5,
      "latency_ms": 4,
      "metrics": {
        "straum_applied": 120,
        "straum_commit": 120,
        "straum_lag": 0,
        "straum_leading": 1,
        "straum_log_durable": 120,
        "straum_log_end": 120,
        "straum_node": 1,
        "straum_outbox_capacity": 341,
        "straum_outbox_entries": 0,
        "straum_serving": 1,
        "straum_slot": 4
      }
    },
    {
      "id": 2,
      "address": "10.0.0.3:9100",
      "state": "unknown",
      "latency_ms": 1000,
      "error": "no answer within 1s",
      "last": {
        "observed_at": "2026-10-21T11:59:58.003Z",
        "metrics": {
          "straum_applied": 112,
          "straum_commit": 112,
          "straum_lag": 0,
          "straum_leading": 0,
          "straum_log_durable": 112,
          "straum_log_end": 112,
          "straum_node": 2,
          "straum_outbox_capacity": 341,
          "straum_outbox_entries": 0,
          "straum_serving": 1,
          "straum_slot": 4
        }
      }
    }
  ]
}
```

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | integer | This document's version. |
| `epoch` | string | Chosen at random when the console starts. A new value means the console restarted: rounds, event ids and history start over. |
| `round` | integer | Poll rounds completed in this epoch. Increases by one per state. |
| `generated_at` | RFC 3339 UTC | When the round closed, by the console's clock. |
| `interval_ms` | integer | The configured poll interval. A consumer that gets no new state for two intervals should show the whole view as not current. |
| `nodes` | array | One entry per configured node, in configuration order. |
| `nodes[].id` | integer | The id given in the console's configuration (`--node` or `STRAUM_CONSOLE_NODES`). |
| `nodes[].address` | string | The `host:port` the values came from, as configured. |
| `nodes[].state` | `"observed"` \| `"unknown"` | `observed`: in this round the node answered HTTP 200 with every stable metric as a non-negative integer. `unknown`: anything else, including no answer. Neither is a health verdict. |
| `nodes[].observed_at` | RFC 3339 UTC | Only when observed. When the response was read, by the console's clock. The core does not timestamp its metrics. |
| `nodes[].age_ms` | integer | Only when observed. `generated_at − observed_at`. |
| `nodes[].latency_ms` | integer | How long this round's request took, whether it succeeded or not. |
| `nodes[].error` | string | Only when unknown. Why, for a person to read. The text is not a stable interface. |
| `nodes[].metrics` | object | Only when observed. Stable metric name → unsigned 64-bit integer. Keys are sorted. |
| `nodes[].last` | object | Only when unknown, and only if the node was observed earlier in this epoch: `observed_at` and `metrics` of that last observation, so a consumer can show old values with their age, never as current. |

The node entry without `id` and `last` is the node entry of
`straum-cli status -o json` (straum-cli `docs/status-json.md`), with the
same names and meanings.

`id` is what the console was told and `metrics.straum_node` is what the
node reports. The console does not correct either one. When they differ,
it raises `console.node.id_mismatch` and still shows both.

### Derived

Computed by the console from the observed nodes of this round only. A node
in `unknown` state counts for nothing here, `last` included.

| Field | Type | Meaning |
|---|---|---|
| `cluster.configured` | integer | The number of configured nodes. |
| `cluster.observed` | integer | The number of nodes observed in this round. |
| `cluster.max_slot` | integer \| null | The highest `straum_slot` any observed node reports; null when none is observed. |
| `cluster.leading` | array of integer | The configured ids of the observed nodes that report `straum_leading` = 1, in configuration order. It can be empty or hold more than one id; the console reports what the nodes say and does not pick one. |

## Event

```json
{
  "schema_version": 1,
  "id": 1742,
  "epoch": "9f2c4e1ab07d5e33",
  "at": "2026-10-21T12:00:02.010Z",
  "source": "console",
  "name": "cluster.leading.changed",
  "node": null,
  "data": { "from": [0], "to": [1], "max_slot": 4 }
}
```

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | integer | This document's version. |
| `id` | integer | Increases by one per event within an epoch, starting at 1. |
| `epoch` | string | As in the state. |
| `at` | RFC 3339 UTC | The `generated_at` of the round that showed the change. |
| `source` | `"console"` | Who saw the change. Every v1 event is computed by the console from two consecutive rounds; the core itself reports no events. |
| `name` | string | One of the names below. |
| `node` | integer \| null | The configured id the event is about, or null for the cluster or the console. |
| `data` | object | Depends on `name`. |

Events are as fine as the poll interval: several changes inside one
interval show up as one event, or as none if they cancel out. The console
keeps the last 2000 events in memory only. A console restart loses them,
and `epoch` changes.

| `name` | Raised when, between round k−1 and round k | `node` | `data` |
|---|---|---|---|
| `console.node.unreachable` | observed → unknown | the node | `{"error", "last_observed_at"}` |
| `console.node.reachable` | unknown → observed (not on the node's first observation) | the node | `{"unknown_for_ms"}` |
| `console.node.id_mismatch` | the node is observed reporting a `straum_node` ≠ its configured id, and has not reported that id before in this epoch. Once per epoch per node and reported id, whatever the node's state in round k−1 | the node | `{"configured", "reported"}` |
| `console.collector.overrun` | a poll tick was skipped because the node's previous request was still running. In that round the node is `unknown`, `error` says so, and `latency_ms` is how long the request has been running | the node | `{"skipped"}` |
| `cluster.max_slot.changed` | `cluster.max_slot` changed, and the same nodes were observed in both rounds | null | `{"from", "to"}` |
| `cluster.leading.changed` | `cluster.leading` differs from round k−1, and the same nodes were observed in both rounds | null | `{"from", "to", "max_slot"}` |
| `node.serving.changed` | `straum_serving` differs, the node observed in both rounds | the node | `{"from", "to"}` |
| `node.lag.started` | `straum_lag` 0 → > 0, the node observed in both rounds | the node | `{"lag"}` |
| `node.lag.cleared` | `straum_lag` > 0 → 0, the node observed in both rounds | the node | `{"lag_was"}` |
| `node.outbox.full` | `straum_outbox_entries` reaches `straum_outbox_capacity` | the node | `{"entries", "capacity"}` |

A change in a node that was unknown in either round raises nothing but
`unreachable`, `reachable` or `id_mismatch`. The `cluster.*` events need
the same set of observed nodes in both rounds, so a node going unknown or
coming back never shows up as a cluster change. The console does not guess
what happened while it could not see.

Not in v1: restarts, takeovers, successions, refusals, and MQTT and link
counts. The core reports them only in unstable series (CON-D-1 A), and the
core has no restart marker among its stable series.

### `gap`

On `GET /api/v1/stream`, a reconnect with `Last-Event-ID` that cannot be
served gets, after the first state:

```json
{ "schema_version": 1, "epoch": "9f2c4e1ab07d5e33", "reason": "evicted", "since_id": 1742 }
```

| `reason` | Meaning |
|---|---|
| `evicted` | Events after `since_id` are no longer in the console's memory. |
| `console_restarted` | `since_id` is from another epoch. |

`state` messages carry no `id:`, so they never move `Last-Event-ID`.

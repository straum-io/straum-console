package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// event is the event document of docs/state-json.md, schema version 1.
// Every event is the console's: computed from two consecutive rounds.
type event struct {
	SchemaVersion int            `json:"schema_version"`
	ID            uint64         `json:"id"`
	Epoch         string         `json:"epoch"`
	At            time.Time      `json:"at"`
	Source        string         `json:"source"`
	Name          string         `json:"name"`
	Node          *int           `json:"node"`
	Data          map[string]any `json:"data"`
}

const maxMisreported = 16

// deriveEvents lists what changed from prev to next, in the order of the
// table in docs/state-json.md; id and epoch are set when published.
// misreported holds the ids each node has already been seen to report
// against its configuration this epoch, and is updated. A node that is
// unknown in either round raises only unreachable, reachable or
// id_mismatch: the console does not guess what it could not see.
func deriveEvents(prev, next state, skipped int, misreported map[int]map[uint64]bool) []event {
	var evs []event
	add := func(name string, node *int, data map[string]any) {
		evs = append(evs, event{SchemaVersion: 1, At: next.GeneratedAt, Source: "console", Name: name, Node: node, Data: data})
	}
	if skipped > 0 {
		add("console.collector.overrun", nil, map[string]any{"skipped": skipped})
	}
	for i, n := range next.Nodes {
		var was nodeState
		if i < len(prev.Nodes) {
			was = prev.Nodes[i]
		}
		id := &n.ID
		switch {
		case was.State == metrics.Observed && n.State == metrics.Unknown:
			add("console.node.unreachable", id, map[string]any{"error": n.Error, "last_observed_at": *was.ObservedAt})
		case was.State == metrics.Unknown && n.State == metrics.Observed && was.Last != nil:
			add("console.node.reachable", id, map[string]any{"unseen_for_ms": n.ObservedAt.Sub(was.Last.ObservedAt).Milliseconds()})
		}
		// At most maxMisreported ids per node are remembered: past that, a
		// node that keeps changing its id raises nothing more this epoch.
		if reported, ok := n.Metrics["straum_node"]; ok && reported != uint64(n.ID) && !misreported[n.ID][reported] && len(misreported[n.ID]) < maxMisreported {
			if misreported[n.ID] == nil {
				misreported[n.ID] = map[uint64]bool{}
			}
			misreported[n.ID][reported] = true
			add("console.node.id_mismatch", id, map[string]any{"configured": n.ID, "reported": reported})
		}
		if was.State != metrics.Observed || n.State != metrics.Observed {
			continue
		}
		before, now := was.Metrics, n.Metrics
		if before["straum_serving"] != now["straum_serving"] {
			add("node.serving.changed", id, map[string]any{"from": before["straum_serving"], "to": now["straum_serving"]})
		}
		switch {
		case before["straum_lag"] == 0 && now["straum_lag"] > 0:
			add("node.lag.started", id, map[string]any{"lag": now["straum_lag"]})
		case before["straum_lag"] > 0 && now["straum_lag"] == 0:
			add("node.lag.cleared", id, map[string]any{"lag_was": before["straum_lag"]})
		}
		if full := func(m map[string]uint64) bool { return m["straum_outbox_entries"] >= m["straum_outbox_capacity"] }; full(now) && !full(before) {
			add("node.outbox.full", id, map[string]any{"entries": now["straum_outbox_entries"], "capacity": now["straum_outbox_capacity"]})
		}
	}
	if !sameObserved(prev, next) {
		return evs
	}
	pc, nc := prev.Cluster, next.Cluster
	if pc.MaxSlot != nil && nc.MaxSlot != nil && *pc.MaxSlot != *nc.MaxSlot {
		add("cluster.max_slot.changed", nil, map[string]any{"from": *pc.MaxSlot, "to": *nc.MaxSlot})
	}
	if !slices.Equal(pc.Leading, nc.Leading) {
		add("cluster.leading.changed", nil, map[string]any{"from": pc.Leading, "to": nc.Leading, "max_slot": nc.MaxSlot})
	}
	return evs
}

// sameObserved: the cluster fields of two rounds are comparable only when
// the same nodes were observed in both. The first round has nothing before it.
func sameObserved(prev, next state) bool {
	if prev.Round == 0 || len(prev.Nodes) != len(next.Nodes) {
		return false
	}
	for i := range next.Nodes {
		if (prev.Nodes[i].State == metrics.Observed) != (next.Nodes[i].State == metrics.Observed) {
			return false
		}
	}
	return true
}

// ring keeps the last events of the epoch, ids 1, 2, … without holes.
// Dropping the oldest shifts the slice (size copies per event); events come
// at most a few per round, so a circular buffer is not worth it.
type ring struct {
	buf  []event // oldest first
	size int
	last uint64
}

func (r *ring) add(e event) event {
	r.last++
	e.ID = r.last
	if len(r.buf) == r.size {
		r.buf = slices.Delete(r.buf, 0, 1)
	}
	r.buf = append(r.buf, e)
	return e
}

// after returns up to limit events with an id above id, oldest first, and
// whether none of the events after id has been evicted.
func (r *ring) after(id uint64, limit int) ([]event, bool) {
	if id >= r.last {
		return nil, true
	}
	i, _ := slices.BinarySearchFunc(r.buf, id+1, func(e event, id uint64) int { return cmp.Compare(e.ID, id) })
	complete := r.buf[0].ID <= id+1
	return slices.Clone(r.buf[i:min(len(r.buf), i+limit)]), complete
}

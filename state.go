package main

import (
	"time"
	"unicode/utf8"

	"github.com/straum-io/straum-cli/metrics"
)

// state is the state document of docs/state-json.md, schema version 1.
type state struct {
	SchemaVersion int         `json:"schema_version"`
	Epoch         string      `json:"epoch"`
	Round         uint64      `json:"round"`
	GeneratedAt   time.Time   `json:"generated_at"`
	IntervalMS    int64       `json:"interval_ms"`
	TimeoutMS     int64       `json:"timeout_ms"`
	Cluster       clusterView `json:"cluster"`
	Nodes         []nodeState `json:"nodes"`
}

// clusterView holds the derived fields: computed from this round's observed
// nodes only, never from an unknown node's last values.
type clusterView struct {
	Configured int     `json:"configured"`
	Observed   int     `json:"observed"`
	MaxSlot    *uint64 `json:"max_slot"`
	Leading    []int   `json:"leading"`
}

// nodeState is straum-cli's status-json node entry plus the configured id
// and, for an unknown node, its last observation in this epoch.
type nodeState struct {
	ID int `json:"id"`
	metrics.Node
	Last *observation `json:"last,omitempty"`
}

type observation struct {
	ObservedAt time.Time         `json:"observed_at"`
	Metrics    map[string]uint64 `json:"metrics"`
}

const maxError = 256

// truncate cuts s to at most max bytes, on a character boundary.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// nextState builds round prev.Round+1 from one report, whose nodes are in
// configuration order. prev carries the last observations forward.
func nextState(prev state, r metrics.Report, cfg config) state {
	s := state{
		SchemaVersion: 1,
		Epoch:         prev.Epoch,
		Round:         prev.Round + 1,
		GeneratedAt:   r.GeneratedAt,
		IntervalMS:    cfg.interval.Milliseconds(),
		TimeoutMS:     requestTimeout(cfg).Milliseconds(),
		Cluster:       clusterView{Configured: len(cfg.nodes), Leading: []int{}},
	}
	for i, n := range r.Nodes {
		// An error can quote the node's own bytes (up to its whole body):
		// capped here, so neither the state nor an event holds more.
		n.Error = truncate(n.Error, maxError)
		ns := nodeState{ID: cfg.nodes[i].id, Node: n}
		if n.State == metrics.Observed {
			s.Cluster.Observed++
			slot := n.Metrics["straum_slot"]
			if s.Cluster.MaxSlot == nil || slot > *s.Cluster.MaxSlot {
				s.Cluster.MaxSlot = &slot
			}
			if n.Metrics["straum_leading"] == 1 {
				s.Cluster.Leading = append(s.Cluster.Leading, ns.ID)
			}
		} else if i < len(prev.Nodes) {
			p := prev.Nodes[i]
			if p.State == metrics.Observed {
				ns.Last = &observation{*p.ObservedAt, p.Metrics}
			} else {
				ns.Last = p.Last
			}
		}
		s.Nodes = append(s.Nodes, ns)
	}
	return s
}

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

var update = flag.Bool("update", false, "rewrite golden files")

func values(id, leading, slot, pos uint64) map[string]uint64 {
	return map[string]uint64{
		"straum_applied": pos, "straum_commit": pos, "straum_lag": 0, "straum_leading": leading,
		"straum_log_durable": pos, "straum_log_end": pos, "straum_node": id,
		"straum_outbox_capacity": 341, "straum_outbox_entries": 0, "straum_serving": 1, "straum_slot": slot,
	}
}

func observed(addr string, at time.Time, m map[string]uint64) metrics.Node {
	age := int64(5)
	return metrics.Node{Address: addr, State: metrics.Observed, ObservedAt: &at, AgeMS: &age, LatencyMS: 3, Metrics: m}
}

func unknown(addr string) metrics.Node {
	return metrics.Node{Address: addr, State: metrics.Unknown, LatencyMS: 1000, Error: "no answer within 1s"}
}

var threeNodes = config{
	nodes:    []node{{0, "10.0.0.1:9100"}, {1, "10.0.0.2:9100"}, {2, "10.0.0.3:9100"}},
	interval: 2 * time.Second,
}

// The example of docs/state-json.md, built over two rounds: node 2 is
// observed, then stops answering.
func exampleState() state {
	t0 := time.Date(2026, 10, 21, 11, 59, 58, 3e6, time.UTC)
	t1 := t0.Add(4007 * time.Millisecond)
	s := nextState(state{Epoch: "9f2c4e1ab07d5e33", Round: 1799}, metrics.Report{GeneratedAt: t0, Nodes: []metrics.Node{
		observed("10.0.0.1:9100", t0, values(0, 1, 3, 112)),
		observed("10.0.0.2:9100", t0, values(1, 0, 3, 112)),
		observed("10.0.0.3:9100", t0, values(2, 0, 4, 112)),
	}}, threeNodes)
	return nextState(s, metrics.Report{GeneratedAt: t1, Nodes: []metrics.Node{
		observed("10.0.0.1:9100", t1, values(0, 0, 4, 120)),
		observed("10.0.0.2:9100", t1, values(1, 1, 4, 120)),
		unknown("10.0.0.3:9100"),
	}}, threeNodes)
}

func TestNextState(t *testing.T) {
	s := exampleState()
	if s.Round != 1801 || s.Epoch != "9f2c4e1ab07d5e33" || s.IntervalMS != 2000 {
		t.Errorf("header: %+v", s)
	}
	c := s.Cluster
	if c.Configured != 3 || c.Observed != 2 || c.MaxSlot == nil || *c.MaxSlot != 4 || !slices.Equal(c.Leading, []int{1}) {
		t.Errorf("cluster: %+v", c)
	}
	n2 := s.Nodes[2]
	if n2.State != metrics.Unknown || n2.Last == nil || n2.Last.Metrics["straum_commit"] != 112 || n2.Metrics != nil {
		t.Errorf("node 2 should be unknown with its last values: %+v", n2)
	}
	if s.Nodes[0].Last != nil {
		t.Error("an observed node has no last")
	}

	// Still unknown a round later: the same last values, not lost.
	s = nextState(s, metrics.Report{GeneratedAt: s.GeneratedAt.Add(2 * time.Second), Nodes: []metrics.Node{
		unknown("10.0.0.1:9100"), unknown("10.0.0.2:9100"), unknown("10.0.0.3:9100"),
	}}, threeNodes)
	if s.Nodes[2].Last == nil || s.Nodes[2].Last.Metrics["straum_commit"] != 112 || s.Nodes[0].Last.Metrics["straum_commit"] != 120 {
		t.Errorf("last values lost: %+v", s.Nodes)
	}
	// Nothing observed: no slot, nobody leading, never a stale value.
	if s.Cluster.Observed != 0 || s.Cluster.MaxSlot != nil || len(s.Cluster.Leading) != 0 {
		t.Errorf("cluster with nothing observed: %+v", s.Cluster)
	}
}

func TestNextStateReportsWhatNodesSay(t *testing.T) {
	now := time.Now().UTC()
	s := nextState(state{}, metrics.Report{GeneratedAt: now, Nodes: []metrics.Node{
		observed("10.0.0.1:9100", now, values(0, 1, 3, 10)),
		observed("10.0.0.2:9100", now, values(1, 1, 4, 10)),
		observed("10.0.0.3:9100", now, values(7, 0, 4, 10)),
	}}, threeNodes)
	// Two nodes claim to lead: both are listed, none is picked.
	if !slices.Equal(s.Cluster.Leading, []int{0, 1}) {
		t.Errorf("leading = %v", s.Cluster.Leading)
	}
	// A wrong id is shown as reported, beside the configured one.
	if s.Nodes[2].ID != 2 || s.Nodes[2].Metrics["straum_node"] != 7 {
		t.Errorf("node 2: %+v", s.Nodes[2])
	}
}

// TestGoldenState pins the JSON form of the state: field names, order and
// shape follow docs/state-json.md.
func TestGoldenState(t *testing.T) {
	got, err := json.MarshalIndent(exampleState(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	const path = "testdata/state.golden.json"
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the state JSON changed; if intended, run make golden and check docs/state-json.md\ngot:\n%s", got)
	}
}

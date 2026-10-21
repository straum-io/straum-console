package main

import (
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// rounds builds two consecutive states of three nodes from their metrics;
// a nil map is an unknown node.
func rounds(a, b []map[string]uint64) (state, state) {
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	build := func(prev state, at time.Time, ms []map[string]uint64) state {
		r := metrics.Report{GeneratedAt: at}
		for i, m := range ms {
			addr := threeNodes.nodes[i].addr
			if m == nil {
				r.Nodes = append(r.Nodes, unknown(addr))
			} else {
				r.Nodes = append(r.Nodes, observed(addr, at, maps.Clone(m)))
			}
		}
		return nextState(prev, r, threeNodes)
	}
	prev := build(state{Epoch: "e"}, t0, a)
	return prev, build(prev, t0.Add(2*time.Second), b)
}

func with(m map[string]uint64, kv ...any) map[string]uint64 {
	m = maps.Clone(m)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = uint64(kv[i+1].(int))
	}
	return m
}

func TestDeriveEvents(t *testing.T) {
	n0, n1, n2 := values(0, 1, 4, 10), values(1, 0, 4, 10), values(2, 0, 4, 10)
	for _, tc := range []struct {
		name string
		a, b []map[string]uint64
		want []string
	}{
		{"nothing changed", []map[string]uint64{n0, n1, n2}, []map[string]uint64{n0, n1, n2}, nil},
		{"a node stops answering", []map[string]uint64{n0, n1, n2}, []map[string]uint64{n0, n1, nil}, []string{"console.node.unreachable"}},
		{"serving flips", []map[string]uint64{n0, n1, n2}, []map[string]uint64{n0, with(n1, "straum_serving", 0), n2}, []string{"node.serving.changed"}},
		{"lag starts", []map[string]uint64{n0, n1, n2}, []map[string]uint64{n0, n1, with(n2, "straum_lag", 7)}, []string{"node.lag.started"}},
		{"lag clears", []map[string]uint64{n0, n1, with(n2, "straum_lag", 7)}, []map[string]uint64{n0, n1, n2}, []string{"node.lag.cleared"}},
		{"lag grows: no event", []map[string]uint64{n0, n1, with(n2, "straum_lag", 7)}, []map[string]uint64{n0, n1, with(n2, "straum_lag", 9)}, nil},
		{"outbox fills", []map[string]uint64{n0, n1, n2}, []map[string]uint64{with(n0, "straum_outbox_entries", 341), n1, n2}, []string{"node.outbox.full"}},
		{"outbox stays full: no event", []map[string]uint64{with(n0, "straum_outbox_entries", 341), n1, n2}, []map[string]uint64{with(n0, "straum_outbox_entries", 341), n1, n2}, nil},
		{"slot and leader move", []map[string]uint64{n0, n1, n2}, []map[string]uint64{with(n0, "straum_leading", 0, "straum_slot", 5), with(n1, "straum_leading", 1, "straum_slot", 5), n2}, []string{"cluster.max_slot.changed", "cluster.leading.changed"}},
		{"leader goes unknown: no cluster event", []map[string]uint64{n0, n1, n2}, []map[string]uint64{nil, n1, n2}, []string{"console.node.unreachable"}},
		{"highest slot goes unknown: no cluster event", []map[string]uint64{n0, n1, with(n2, "straum_slot", 6)}, []map[string]uint64{n0, n1, nil}, []string{"console.node.unreachable"}},
		{"changes on a node unknown in the round before: none (and it was never seen, so not reachable either)", []map[string]uint64{n0, n1, nil}, []map[string]uint64{n0, n1, with(n2, "straum_lag", 3, "straum_serving", 0)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev, next := rounds(tc.a, tc.b)
			var got []string
			for _, e := range deriveEvents(prev, next, 0, map[int]map[uint64]bool{}) {
				got = append(got, e.Name)
				if e.Source != "console" || e.SchemaVersion != 1 || !e.At.Equal(next.GeneratedAt) {
					t.Errorf("envelope: %+v", e)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeriveEventsReachableAndOverrun(t *testing.T) {
	n0, n1, n2 := values(0, 1, 4, 10), values(1, 0, 4, 10), values(2, 0, 4, 10)
	// Observed, then unknown, then back: reachable, with the time unseen.
	_, s2 := rounds([]map[string]uint64{n0, n1, n2}, []map[string]uint64{n0, n1, nil})
	s3 := nextState(s2, metrics.Report{GeneratedAt: s2.GeneratedAt.Add(2 * time.Second), Nodes: []metrics.Node{
		observed(s2.Nodes[0].Address, s2.GeneratedAt.Add(2*time.Second), n0),
		observed(s2.Nodes[1].Address, s2.GeneratedAt.Add(2*time.Second), n1),
		observed(s2.Nodes[2].Address, s2.GeneratedAt.Add(2*time.Second), n2),
	}}, threeNodes)
	evs := deriveEvents(s2, s3, 3, map[int]map[uint64]bool{})
	if len(evs) != 2 || evs[0].Name != "console.collector.overrun" || evs[0].Data["skipped"] != 3 || evs[0].Node != nil {
		t.Fatalf("overrun: %+v", evs)
	}
	if evs[1].Name != "console.node.reachable" || *evs[1].Node != 2 || evs[1].Data["unseen_for_ms"] != int64(4000) {
		t.Fatalf("reachable: %+v", evs[1])
	}
	// A node never observed before raises nothing when it first answers.
	never, seen := rounds([]map[string]uint64{n0, n1, nil}, []map[string]uint64{n0, n1, n2})
	if evs := deriveEvents(never, seen, 0, map[int]map[uint64]bool{}); len(evs) != 0 {
		t.Errorf("first answer of a never-seen node: %+v", evs)
	}
}

func TestRing(t *testing.T) {
	r := ring{size: 3}
	for range 5 {
		r.add(event{Name: "x"})
	}
	ids := func(evs []event) (out []uint64) {
		for _, e := range evs {
			out = append(out, e.ID)
		}
		return out
	}
	// Ids 1 and 2 were evicted.
	if evs, complete := r.after(0, 10); !slices.Equal(ids(evs), []uint64{3, 4, 5}) || complete {
		t.Errorf("after 0: %v %v", ids(evs), complete)
	}
	if evs, complete := r.after(2, 10); !slices.Equal(ids(evs), []uint64{3, 4, 5}) || !complete {
		t.Errorf("after 2: %v %v", ids(evs), complete)
	}
	if evs, complete := r.after(3, 1); !slices.Equal(ids(evs), []uint64{4}) || !complete {
		t.Errorf("after 3, limit 1: %v %v", ids(evs), complete)
	}
	if evs, complete := r.after(5, 10); len(evs) != 0 || !complete {
		t.Errorf("after the newest: %v %v", ids(evs), complete)
	}
	// No wrap at the top of the id range.
	if evs, complete := r.after(math.MaxUint64, 10); len(evs) != 0 || !complete {
		t.Errorf("after MaxUint64: %v %v", ids(evs), complete)
	}
	if evs, complete := (&ring{size: 3}).after(0, 10); len(evs) != 0 || !complete {
		t.Errorf("empty ring: %v %v", ids(evs), complete)
	}
}

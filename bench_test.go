package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// rounds of n observed nodes whose positions advance, for the benchmarks.
func benchReports(n, count int) (config, []metrics.Report) {
	cfg := config{interval: 2 * time.Second}
	for i := range n {
		cfg.nodes = append(cfg.nodes, node{i, fmt.Sprintf("10.0.0.%d:9100", i+1)})
	}
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	var rs []metrics.Report
	for k := range count {
		at := t0.Add(time.Duration(k) * cfg.interval)
		r := metrics.Report{GeneratedAt: at}
		for i := range n {
			leading := 0
			if k/10%n == i {
				leading = 1
			}
			r.Nodes = append(r.Nodes, observed(cfg.nodes[i].addr, at, values(uint64(i), uint64(leading), uint64(1+k/10), uint64(10+k))))
		}
		rs = append(rs, r)
	}
	return cfg, rs
}

// BenchmarkRound is what the console does per round once the nodes have
// answered: the next state, its events, and publishing them (one JSON
// encode, shared by every stream).
func BenchmarkRound(b *testing.B) {
	for _, n := range []int{5, 15} {
		b.Run(fmt.Sprintf("nodes=%d", n), func(b *testing.B) {
			cfg, rs := benchReports(n, 100)
			h := newHub("bench")
			s := state{Epoch: "bench"}
			mis := map[int]map[uint64]bool{}
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				next := nextState(s, rs[i%len(rs)], cfg)
				h.publish(next, deriveEvents(s, next, 0, mis))
				s = next
			}
		})
	}
}

// BenchmarkFanout is publishing a round to 64 streams that keep up.
func BenchmarkFanout(b *testing.B) {
	cfg, rs := benchReports(5, 100)
	h := newHub("bench")
	for range 64 {
		_, c, _ := h.subscribe("")
		go func() {
			for range c {
			}
		}()
	}
	b.Cleanup(h.close)
	s := state{Epoch: "bench"}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		s = nextState(s, rs[i%len(rs)], cfg)
		h.publish(s, nil)
	}
}

// BenchmarkCollect is one real round against 5 fixture nodes on loopback:
// the HTTP requests and straum-cli's parsing of the core's text.
func BenchmarkCollect(b *testing.B) {
	body := fixture(b, "node0.prom")
	var addrs []string
	for range 5 {
		addrs = append(addrs, serve(b, 200, body, 0))
	}
	c := metrics.Client{Timeout: time.Second}
	b.ReportAllocs()
	for b.Loop() {
		if r, err := c.Collect(context.Background(), addrs); err != nil || r.Nodes[0].State != metrics.Observed {
			b.Fatal(err, r.Nodes[0].Error)
		}
	}
}

// TestStreamRoundSize checks plan §14: at most 4 KiB per stream per round
// at 5 nodes, the state being the whole of it.
func TestStreamRoundSize(t *testing.T) {
	cfg, rs := benchReports(5, 1)
	h := newHub("e1")
	h.publish(nextState(state{Epoch: "e1"}, rs[0], cfg), nil)
	if n := len(h.stateSSE); n > 4096 {
		t.Errorf("a state message is %d bytes at 5 nodes, over 4 KiB", n)
	} else {
		t.Logf("a state message is %d bytes at 5 nodes", n)
	}
}

// TestFifteenNodes checks plan §14's upper bound of configured nodes on real
// requests: one round against 15 fixture nodes, all observed, in one round.
func TestFifteenNodes(t *testing.T) {
	body := fixture(t, "node0.prom")
	cfg := config{interval: 2 * time.Second}
	for i := range 15 {
		cfg.nodes = append(cfg.nodes, node{i, serve(t, 200, strings.Replace(body, "straum_node 0", fmt.Sprintf("straum_node %d", i), 1), 0)})
	}
	c := newCollector(cfg, slog.New(slog.DiscardHandler))
	s, evs, ok := c.round(context.Background())
	if !ok || s.Cluster.Observed != 15 {
		t.Fatalf("observed %d of 15", s.Cluster.Observed)
	}
	for _, e := range evs {
		if e.Name == "console.node.id_mismatch" {
			t.Errorf("unexpected %+v", e)
		}
	}
}

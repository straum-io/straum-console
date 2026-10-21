package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

func fixture(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serve answers every request with status and body, after delay.
func serve(t testing.TB, status int, body string, delay time.Duration) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			t.Errorf("the console sent %s %s; only GET /metrics is allowed", r.Method, r.URL.Path)
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestRound(t *testing.T) {
	node0 := fixture(t, "node0.prom")
	nodes := []struct {
		body   string
		status int
		delay  time.Duration
		error  string
	}{
		{body: node0, status: 200},
		{body: node0, status: 200, delay: 2 * time.Second, error: "no answer within"},
		{body: "oops", status: 500, error: "HTTP 500"},
		{body: "not a metric line {", status: 200, error: "malformed"},
		{body: strings.ReplaceAll(node0, "straum_lag 0", ""), status: 200, error: "missing stable metric straum_lag"},
		{body: strings.Repeat("#", 2<<20), status: 200, error: "larger than"},
	}
	cfg := config{interval: 2 * time.Second}
	for i, n := range nodes {
		cfg.nodes = append(cfg.nodes, node{i, serve(t, n.status, n.body, n.delay)})
	}
	c := newCollector(cfg, slog.New(slog.DiscardHandler))

	start := time.Now()
	s, evs, ok := c.round(context.Background())
	if !ok {
		t.Fatal("round dropped")
	}
	// The slow node must not hold the round past the timeout (1s).
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("round took %s", took)
	}
	if s.Nodes[0].State != metrics.Observed || s.Cluster.Observed != 1 {
		t.Fatalf("node 0: %+v", s.Nodes[0])
	}
	for i, n := range nodes[1:] {
		got := s.Nodes[i+1]
		if got.State != metrics.Unknown || !strings.Contains(got.Error, n.error) || got.ID != i+1 {
			t.Errorf("node %d: %+v, want unknown with %q", i+1, got, n.error)
		}
	}
	// The first round has nothing to compare with, and node 0 reports its
	// configured id: no event.
	if len(evs) != 0 {
		t.Errorf("events: %+v", evs)
	}
}

func TestRoundIDMismatch(t *testing.T) {
	var down atomic.Bool
	body := fixture(t, "node0.prom")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg := config{interval: 2 * time.Second, nodes: []node{{5, strings.TrimPrefix(srv.URL, "http://")}}}
	c := newCollector(cfg, slog.New(slog.DiscardHandler))
	s, _, _ := c.round(context.Background())
	// Observed, shown with both ids.
	if s.Nodes[0].State != metrics.Observed || s.Nodes[0].ID != 5 || s.Nodes[0].Metrics["straum_node"] != 0 {
		t.Fatalf("%+v", s.Nodes[0])
	}
	// Raised once per epoch, however often the node goes and comes back.
	c.round(context.Background())
	down.Store(true)
	c.round(context.Background())
	down.Store(false)
	c.round(context.Background())
	var names []string
	for _, e := range c.hub.after(0, 2000) {
		names = append(names, e.Name)
	}
	want := []string{"console.node.id_mismatch", "console.node.unreachable", "console.node.reachable"}
	if !slices.Equal(names, want) {
		t.Errorf("events %v, want %v", names, want)
	}
}

func TestMissedTicks(t *testing.T) {
	t0 := time.Now()
	const i = 2 * time.Second
	for _, tc := range []struct {
		prev, now time.Time
		want      int
	}{
		{time.Time{}, t0, 0},                      // the first round
		{t0, t0.Add(i), 0},                        // on time
		{t0, t0.Add(i + 900*time.Millisecond), 0}, // late, but no tick lost
		{t0, t0.Add(2 * i), 1},                    // one tick dropped
		{t0, t0.Add(5*i + time.Second), 4},        // a long stall
	} {
		if got := missedTicks(tc.prev, tc.now, i); got != tc.want {
			t.Errorf("after %s: %d, want %d", tc.now.Sub(tc.prev), got, tc.want)
		}
	}
}

func TestRoundCancelled(t *testing.T) {
	cfg := config{interval: 2 * time.Second, nodes: []node{{0, serve(t, 200, fixture(t, "node0.prom"), 0)}}}
	c := newCollector(cfg, slog.New(slog.DiscardHandler))
	c.round(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Shutting down does not turn an answering node unknown.
	if s, _, ok := c.round(ctx); ok || s.Round != 1 || s.Nodes[0].State != metrics.Observed {
		t.Fatalf("ok=%v %+v", ok, s)
	}
}

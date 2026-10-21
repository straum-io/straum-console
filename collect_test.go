package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// serve answers every request with status and body, after delay.
func serve(t *testing.T, status int, body string, delay time.Duration) string {
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
	var logs bytes.Buffer
	c := newCollector(cfg, slog.New(slog.NewTextHandler(&logs, nil)))

	start := time.Now()
	s, ok := c.round(context.Background())
	if !ok {
		t.Fatal("round dropped")
	}
	// The slow node must not hold the round past the timeout (1s).
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("round took %s", took)
	}
	if s.Nodes[0].State != metrics.Observed || s.Cluster.Observed != 1 || !strings.Contains(logs.String(), "node observed") {
		t.Fatalf("node 0: %+v\n%s", s.Nodes[0], logs.String())
	}
	for i, n := range nodes[1:] {
		got := s.Nodes[i+1]
		if got.State != metrics.Unknown || !strings.Contains(got.Error, n.error) || got.ID != i+1 {
			t.Errorf("node %d: %+v, want unknown with %q", i+1, got, n.error)
		}
	}
	// Only node 0 answers, and it reports its configured id.
	if strings.Contains(logs.String(), "reports another id") {
		t.Errorf("no id mismatch expected:\n%s", logs.String())
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
	var logs bytes.Buffer
	c := newCollector(cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	s, _ := c.round(context.Background())
	// Observed, shown with both ids.
	if s.Nodes[0].State != metrics.Observed || s.Nodes[0].ID != 5 || s.Nodes[0].Metrics["straum_node"] != 0 {
		t.Fatalf("%+v", s.Nodes[0])
	}
	// Logged once per epoch, however often the node goes and comes back.
	c.round(context.Background())
	down.Store(true)
	c.round(context.Background())
	down.Store(false)
	c.round(context.Background())
	if n := strings.Count(logs.String(), "reports another id"); n != 1 {
		t.Errorf("logged %d times:\n%s", n, logs.String())
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
	if s, ok := c.round(ctx); ok || s.Round != 1 || s.Nodes[0].State != metrics.Observed {
		t.Fatalf("ok=%v %+v", ok, s)
	}
}

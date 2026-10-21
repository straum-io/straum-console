//go:build e2e

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// cluster is three straum-server processes on loopback, with what it takes
// to restart one. The harness follows straum-cli's e2e_test.go.
type cluster struct {
	t     *testing.T
	bin   string
	env   [][]string
	logs  []*os.File
	procs []*exec.Cmd
	cfg   config
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// startCluster starts the nodes; STRAUM_SERVER_BIN is the core's binary
// (make e2e builds it from ../straum).
func startCluster(t *testing.T) *cluster {
	t.Helper()
	c := &cluster{t: t, bin: os.Getenv("STRAUM_SERVER_BIN"), cfg: config{interval: time.Second}}
	if c.bin == "" {
		t.Fatal("STRAUM_SERVER_BIN is not set")
	}
	data := t.TempDir()
	var peers []string
	for i := range 3 {
		peers = append(peers, fmt.Sprintf("%d=%s", i, freeAddr(t)))
		c.cfg.nodes = append(c.cfg.nodes, node{i, freeAddr(t)})
	}
	for i := range 3 {
		c.env = append(c.env, []string{
			"STRAUM_EXPERIMENTAL_CLUSTER=1",
			fmt.Sprintf("STRAUM_CLUSTER_NODE_ID=%d", i),
			"STRAUM_CLUSTER_PEERS=" + strings.Join(peers, ","),
			"STRAUM_CLUSTER_MQTT_LISTEN=" + freeAddr(t),
			"STRAUM_CLUSTER_METRICS_LISTEN=" + c.cfg.nodes[i].addr,
			fmt.Sprintf("STRAUM_CLUSTER_DATA_DIR=%s/n%d", data, i),
			"STRAUM_LOG_LEVEL=warn",
			"NO_COLOR=1",
		})
		logPath := fmt.Sprintf("%s/n%d.log", data, i)
		log, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		c.logs = append(c.logs, log)
		c.procs = append(c.procs, nil)
		t.Cleanup(func() {
			c.stop(i)
			log.Close()
			if t.Failed() {
				if b, err := os.ReadFile(logPath); err == nil {
					t.Logf("node %d log:\n%s", i, b)
				}
			}
		})
		c.start(i)
	}
	return c
}

func (c *cluster) start(i int) {
	cmd := exec.Command(c.bin)
	cmd.Env = c.env[i]
	var out io.Writer = c.logs[i]
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	c.procs[i] = cmd
}

func (c *cluster) stop(i int) {
	if p := c.procs[i]; p != nil {
		p.Process.Kill()
		p.Wait()
		c.procs[i] = nil
	}
}

// await runs rounds, one per interval, until ok holds or the deadline.
func await(t *testing.T, col *collector, within time.Duration, what string, ok func(state) bool) state {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		s, _ := col.round(context.Background())
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s; last state %+v", what, within, s)
		}
		time.Sleep(col.cfg.interval)
	}
}

func TestE2E(t *testing.T) {
	c := startCluster(t)
	col := newCollector(c.cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	allServing := func(s state) bool {
		serving := 0
		for _, n := range s.Nodes {
			serving += int(n.Metrics["straum_serving"])
		}
		return s.Cluster.Observed == 3 && serving == 3 && len(s.Cluster.Leading) == 1
	}
	s := await(t, col, 20*time.Second, "cluster up", allServing)

	// leading names the node that reports straum_leading = 1, in the same round.
	leader := s.Cluster.Leading[0]
	if s.Nodes[leader].Metrics["straum_leading"] != 1 {
		t.Fatalf("leading %d, but node says %+v", leader, s.Nodes[leader])
	}
	for i, n := range s.Nodes {
		if n.ID != i || n.Metrics["straum_node"] != uint64(i) || n.Address != c.cfg.nodes[i].addr {
			t.Errorf("node %d answered for another: %+v", i, n)
		}
	}

	// A killed node is unknown in the next round (within one interval plus
	// the timeout), with its last values kept.
	down := (leader + 1) % 3
	c.stop(down)
	time.Sleep(col.cfg.interval)
	s, _ = col.round(context.Background())
	if n := s.Nodes[down]; n.State != metrics.Unknown || n.Last == nil || n.Last.Metrics["straum_node"] != uint64(down) {
		t.Fatalf("killed node %d: %+v", down, n)
	}
	if s.Cluster.Observed != 2 {
		t.Errorf("observed = %d", s.Cluster.Observed)
	}

	// Restarted, it is observed again.
	c.start(down)
	await(t, col, 20*time.Second, "restarted node observed", allServing)

	// A handoff (SIGUSR1 to the leader) moves leading to another node.
	if err := c.procs[leader].Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	s = await(t, col, 20*time.Second, "leading moves after handoff", func(s state) bool {
		return len(s.Cluster.Leading) == 1 && s.Cluster.Leading[0] != leader
	})
	if slices.Contains(s.Cluster.Leading, leader) {
		t.Fatalf("leading = %v", s.Cluster.Leading)
	}
}

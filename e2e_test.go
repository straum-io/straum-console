//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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

// console runs the whole program against the cluster on a free loopback
// port and returns its base URL; it stops with the test.
func (c *cluster) console(t *testing.T) string {
	t.Helper()
	var spec []string
	for _, n := range c.cfg.nodes {
		spec = append(spec, fmt.Sprintf("%d=%s", n.id, n.addr))
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	exited := make(chan int, 1)
	go func() {
		exited <- run(ctx, []string{"--node", strings.Join(spec, ","), "--interval", "1s", "--listen", "127.0.0.1:0"}, nil, os.Stderr, ready)
	}()
	t.Cleanup(func() {
		cancel()
		if code := <-exited; code != 0 {
			t.Errorf("console exited %d", code)
		}
	})
	select {
	case addr := <-ready:
		return "http://" + addr
	case code := <-exited:
		t.Fatalf("console exited %d before listening", code)
	}
	return ""
}

// awaitState polls GET /api/v1/state until ok holds.
func awaitState(t *testing.T, url string, within time.Duration, what string, ok func(state) bool) state {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var s state
		if code, body, _ := get(t, url+"/api/v1/state"); code == 200 && json.Unmarshal([]byte(body), &s) == nil && ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, within)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// awaitEvent reads the stream until an event named name arrives.
func awaitEvent(t *testing.T, s *sse, name string) event {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		f := s.next(t)
		if f["event"] != "event" {
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(f["data"]), &e); err != nil {
			t.Fatal(err)
		}
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no %s within 20s", name)
	return event{}
}

func TestE2E(t *testing.T) {
	c := startCluster(t)
	url := c.console(t)
	allServing := func(s state) bool {
		serving := 0
		for _, n := range s.Nodes {
			serving += int(n.Metrics["straum_serving"])
		}
		return s.Cluster.Observed == 3 && serving == 3 && len(s.Cluster.Leading) == 1
	}
	s := awaitState(t, url, 20*time.Second, "cluster up", allServing)

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
	stream := openStream(t, url, "")

	// A killed node: unreachable on the stream, unknown with its last values.
	down := (leader + 1) % 3
	c.stop(down)
	if e := awaitEvent(t, stream, "console.node.unreachable"); e.Node == nil || *e.Node != down {
		t.Fatalf("unreachable: %+v", e)
	}
	s = awaitState(t, url, 5*time.Second, "killed node unknown", func(s state) bool { return s.Nodes[down].State == metrics.Unknown })
	if n := s.Nodes[down]; n.Last == nil || n.Last.Metrics["straum_node"] != uint64(down) || s.Cluster.Observed != 2 {
		t.Fatalf("killed node %d: %+v", down, n)
	}

	// Restarted: reachable again.
	c.start(down)
	if e := awaitEvent(t, stream, "console.node.reachable"); *e.Node != down {
		t.Fatalf("reachable: %+v", e)
	}
	awaitState(t, url, 20*time.Second, "restarted node serving", allServing)

	// A handoff (SIGUSR1 to the leader) moves leading to another node.
	if err := c.procs[leader].Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	e := awaitEvent(t, stream, "cluster.leading.changed")
	to, _ := e.Data["to"].([]any)
	if len(to) != 1 || int(to[0].(float64)) == leader {
		t.Fatalf("leading.changed: %+v", e)
	}
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"slices"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// collector polls every configured node once per interval. Browsers never
// cause a poll, and nothing but GET /metrics is ever sent (ECO-D-11 A).
type collector struct {
	cfg    config
	client metrics.Client
	addrs  []string
	log    *slog.Logger
	state  state
	// The ids each node has reported against its configuration, this epoch.
	misreported map[int]map[uint64]bool
	// Ticks skipped because a round started late, this epoch.
	skipped int
}

func newCollector(cfg config, log *slog.Logger) *collector {
	var addrs []string
	for _, n := range cfg.nodes {
		addrs = append(addrs, n.addr)
	}
	var epoch [8]byte
	rand.Read(epoch[:])
	return &collector{
		cfg: cfg,
		// Below the interval, so the requests of a round end before the next
		// tick: one request in flight per node.
		client:      metrics.Client{Timeout: min(time.Second, cfg.interval/2)},
		addrs:       addrs,
		log:         log,
		state:       state{Epoch: hex.EncodeToString(epoch[:])},
		misreported: map[int]map[uint64]bool{},
	}
}

// round asks every node once. Collect returns when each has answered or
// timed out, which closes the round. A round cut short by ctx is dropped,
// so shutting down never marks a node unknown.
func (c *collector) round(ctx context.Context) (state, bool) {
	r, err := c.client.Collect(ctx, c.addrs)
	if err != nil || ctx.Err() != nil {
		// err is only a bad address or timeout, both refused by parseConfig.
		return c.state, false
	}
	next := nextState(c.state, r, c.cfg)
	c.logChanges(c.state, next)
	c.state = next
	return next, true
}

// run polls until ctx ends: one round now, then one per tick.
func (c *collector) run(ctx context.Context) {
	t := time.NewTicker(c.cfg.interval)
	defer t.Stop()
	var last time.Time
	for {
		start := time.Now()
		if n := missedTicks(last, start, c.cfg.interval); n > 0 {
			c.skipped += n
			c.log.Warn("ticks skipped", "skipped", n, "total", c.skipped)
		}
		last = start
		c.round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// missedTicks counts the ticks between two round starts that never became
// a round. The requests cannot cause it (their timeout is under half the
// interval); a stall of the whole process can, and time.Ticker drops the
// ticks it could not deliver.
func missedTicks(prev, now time.Time, interval time.Duration) int {
	if prev.IsZero() {
		return 0
	}
	return max(0, int(now.Sub(prev)/interval)-1)
}

// logChanges logs each transition once, never each poll.
func (c *collector) logChanges(prev, next state) {
	for i, n := range next.Nodes {
		var was nodeState
		if i < len(prev.Nodes) {
			was = prev.Nodes[i]
		}
		attrs := []any{"id", n.ID, "address", n.Address}
		if n.State != was.State {
			if n.State == metrics.Observed {
				c.log.Info("node observed", attrs...)
			} else {
				c.log.Warn("node unknown", append(attrs, "error", n.Error)...)
			}
		}
		// The console reports a wrong id, once per node and id; it does not
		// correct it.
		reported, ok := n.Metrics["straum_node"]
		if ok && reported != uint64(n.ID) && !c.misreported[n.ID][reported] {
			if c.misreported[n.ID] == nil {
				c.misreported[n.ID] = map[uint64]bool{}
			}
			c.misreported[n.ID][reported] = true
			c.log.Warn("node reports another id", append(attrs, "reported", reported)...)
		}
	}
	if !slices.Equal(prev.Cluster.Leading, next.Cluster.Leading) {
		c.log.Info("nodes reporting leading", "leading", next.Cluster.Leading, "observed", next.Cluster.Observed)
	}
}

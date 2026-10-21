package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// collector polls every configured node once per interval and publishes
// each round to the hub. Browsers never cause a poll, and nothing but
// GET /metrics is ever sent (ECO-D-11 A).
type collector struct {
	cfg    config
	client metrics.Client
	addrs  []string
	log    *slog.Logger
	hub    *hub
	state  state
	// The ids each node has reported against its configuration, this epoch.
	misreported map[int]map[uint64]bool
	// Ticks skipped since the last round, raised with the next one.
	skipped int
}

func newCollector(cfg config, log *slog.Logger) *collector {
	var addrs []string
	for _, n := range cfg.nodes {
		addrs = append(addrs, n.addr)
	}
	var b [8]byte
	rand.Read(b[:])
	epoch := hex.EncodeToString(b[:])
	return &collector{
		cfg: cfg,
		// Below the interval, so the requests of a round end before the next
		// tick: one request in flight per node.
		client:      metrics.Client{Timeout: min(time.Second, cfg.interval/2)},
		addrs:       addrs,
		log:         log,
		hub:         newHub(epoch),
		state:       state{Epoch: epoch},
		misreported: map[int]map[uint64]bool{},
	}
}

// round asks every node once. Collect returns when each has answered or
// timed out, which closes the round. A round cut short by ctx is dropped,
// so shutting down never marks a node unknown.
func (c *collector) round(ctx context.Context) (state, []event, bool) {
	r, err := c.client.Collect(ctx, c.addrs)
	if err != nil || ctx.Err() != nil {
		// err is only a bad address or timeout, both refused by parseConfig.
		return c.state, nil, false
	}
	next := nextState(c.state, r, c.cfg)
	evs := c.hub.publish(next, deriveEvents(c.state, next, c.skipped, c.misreported))
	if c.state.Round == 0 {
		for _, n := range next.Nodes {
			c.log.Info("first round", "id", n.ID, "address", n.Address, "state", n.State, "error", n.Error)
		}
	}
	for _, e := range evs {
		c.log.Info("event", "id", e.ID, "name", e.Name, "node", e.Node, "data", e.Data)
	}
	c.state, c.skipped = next, 0
	return next, evs, true
}

// run polls until ctx ends: one round now, then one per tick.
func (c *collector) run(ctx context.Context) {
	t := time.NewTicker(c.cfg.interval)
	defer t.Stop()
	var last time.Time
	for {
		start := time.Now()
		c.skipped += missedTicks(last, start, c.cfg.interval)
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

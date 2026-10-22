package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// historyLen is the rounds kept (CON-D-7 A): 1 h at the default 2 s, 30 min
// at 1 s, whatever the interval. With at most maxNodes nodes the values
// take 1,800 × 15 × 11 × 8 B ≈ 2.4 MB, allocated once.
const historyLen = 1800

// The series the console computes from the stable ones (docs/state-json.md,
// History). None is a health verdict.
var derivedSeries = []string{"commit_rate", "log_end_minus_commit", "log_end_minus_durable"}

// histRound is one entry of the ring: when it closed, who reported leading,
// and per node its observed_at (0 when unknown) and stable values.
type histRound struct {
	at      int64 // generated_at, Unix ms
	leaders string
	seen    []int64 // per node, observed_at in Unix ms; 0 = unknown
	vals    []uint64
}

type history struct {
	mu       sync.Mutex
	series   []string // metrics.Stable(), in order
	nodes    []node
	interval time.Duration
	rounds   []histRound // a ring of historyLen
	next     int         // where the next round goes
	n        int         // rounds held
}

func newHistory(nodes []node) *history {
	ids := len(nodes)
	h := &history{series: metrics.Stable(), nodes: nodes, rounds: make([]histRound, historyLen)}
	vals := make([]uint64, historyLen*ids*len(h.series))
	seen := make([]int64, historyLen*ids)
	per := ids * len(h.series)
	for i := range h.rounds {
		h.rounds[i].vals = vals[i*per : (i+1)*per : (i+1)*per]
		h.rounds[i].seen = seen[i*ids : (i+1)*ids : (i+1)*ids]
	}
	return h
}

// add records one round, overwriting the oldest once the ring is full.
func (h *history) add(s state) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := &h.rounds[h.next]
	h.interval = time.Duration(s.IntervalMS) * time.Millisecond
	r.at = s.GeneratedAt.UnixMilli()
	r.leaders = fmt.Sprint(s.Cluster.Leading)
	for i, n := range s.Nodes {
		r.seen[i] = 0
		if n.State != metrics.Observed {
			continue
		}
		r.seen[i] = n.ObservedAt.UnixMilli()
		for j, name := range h.series {
			r.vals[i*len(h.series)+j] = n.Metrics[name]
		}
	}
	h.next = (h.next + 1) % historyLen
	h.n = min(h.n+1, historyLen)
}

// historyDoc is the history document of docs/state-json.md, schema version 1.
type historyDoc struct {
	SchemaVersion int           `json:"schema_version"`
	Epoch         string        `json:"epoch"`
	Series        string        `json:"series"`
	Derived       bool          `json:"derived"`
	IntervalMS    int64         `json:"interval_ms"`
	RangeMS       int64         `json:"range_ms"`
	From          *time.Time    `json:"from"`
	To            *time.Time    `json:"to"`
	T             []int64       `json:"t"`
	Nodes         []historyNode `json:"nodes"`
}

// historyNode is one node's points: its value, and when it was read
// (observed_at, Unix ms), both null where it was unknown.
type historyNode struct {
	ID         int    `json:"id"`
	Address    string `json:"address"`
	ObservedAt []any  `json:"observed_at"`
	V          []any  `json:"v"`
}

// maxSeries bounds one request: every stable and derived series once.
var maxSeries = len(metrics.Stable()) + len(derivedSeries)

var historyRanges = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour}

// queryAll answers GET /api/v1/history: every series named, under one lock,
// so all cover the same rounds.
func (h *history) queryAll(epoch string, series []string, node int, span time.Duration, since int64) ([]historyDoc, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	docs := []historyDoc{}
	for _, name := range series {
		doc, err := h.query(epoch, name, node, span, since)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// query is one series of queryAll; the caller holds h.mu. node < 0 means
// every node; since > 0 keeps only the rounds after it, so a page fetches
// the range once and then only what is new. An unknown series or node is an
// error for the client.
func (h *history) query(epoch, series string, node int, span time.Duration, since int64) (historyDoc, error) {
	col, derived := slices.Index(h.series, series), slices.Contains(derivedSeries, series)
	if col < 0 && !derived {
		return historyDoc{}, fmt.Errorf("series: want one of %s", strings.Join(append(slices.Clone(h.series), derivedSeries...), ", "))
	}
	nodes := []int{}
	for i, n := range h.nodes {
		if node < 0 || n.id == node {
			nodes = append(nodes, i)
		}
	}
	if len(nodes) == 0 {
		return historyDoc{}, fmt.Errorf("node: %d is not configured", node)
	}
	interval := h.interval
	doc := historyDoc{SchemaVersion: 1, Epoch: epoch, Series: series, Derived: derived,
		IntervalMS: interval.Milliseconds(), RangeMS: span.Milliseconds(), T: []int64{}, Nodes: []historyNode{}}
	// The rounds in the range, oldest first.
	var idx []int
	if h.n > 0 {
		last := h.rounds[(h.next+historyLen-1)%historyLen].at
		for k := h.n - 1; k >= 0; k-- {
			i := (h.next + historyLen - h.n + k) % historyLen
			if h.rounds[i].at < last-span.Milliseconds() || h.rounds[i].at <= since {
				break
			}
			idx = append(idx, i)
		}
		slices.Reverse(idx)
	}
	for _, i := range idx {
		doc.T = append(doc.T, h.rounds[i].at)
	}
	if len(idx) > 0 {
		from, to := time.UnixMilli(doc.T[0]).UTC(), time.UnixMilli(doc.T[len(doc.T)-1]).UTC()
		doc.From, doc.To = &from, &to
	}
	ns := len(h.series)
	at := func(ni, k int, name string) uint64 {
		return h.rounds[k].vals[ni*ns+slices.Index(h.series, name)]
	}
	for _, ni := range nodes {
		hn := historyNode{ID: h.nodes[ni].id, Address: h.nodes[ni].addr, ObservedAt: make([]any, len(idx)), V: make([]any, len(idx))}
		for p, k := range idx {
			r := &h.rounds[k]
			if r.seen[ni] == 0 {
				continue // null: the node was unknown
			}
			hn.ObservedAt[p] = r.seen[ni]
			switch series {
			case "commit_rate":
				if k == (h.next+historyLen-h.n)%historyLen {
					continue // the oldest round held has no predecessor
				}
				prev := (k + historyLen - 1) % historyLen
				hn.V[p] = rate(&h.rounds[prev], r, ni, at(ni, prev, "straum_commit"), at(ni, k, "straum_commit"), interval)
			case "log_end_minus_commit":
				hn.V[p] = int64(at(ni, k, "straum_log_end")) - int64(at(ni, k, "straum_commit"))
			case "log_end_minus_durable":
				hn.V[p] = int64(at(ni, k, "straum_log_end")) - int64(at(ni, k, "straum_log_durable"))
			default:
				hn.V[p] = r.vals[ni*ns+col]
			}
		}
		doc.Nodes = append(doc.Nodes, hn)
	}
	return doc, nil
}

// rate is the commit rate per second between two consecutive rounds, or nil
// where none can be honestly drawn: the node unknown in either round, a
// round missed between them (a gap), commit going backwards (a restart), or
// a different set of nodes reporting leading.
func rate(prev, cur *histRound, ni int, c0, c1 uint64, interval time.Duration) any {
	if prev.seen[ni] == 0 || cur.at-prev.at > interval.Milliseconds()*3/2 || c1 < c0 ||
		prev.leaders != cur.leaders || cur.seen[ni] <= prev.seen[ni] {
		return nil
	}
	return float64(c1-c0) * 1000 / float64(cur.seen[ni]-prev.seen[ni])
}

// parseHistoryQuery reads series, node, range and since from a request's
// query. series is a comma-separated list, so a page's charts share one
// request; at most maxSeries names.
func parseHistoryQuery(series, node, rng, since string) ([]string, int, time.Duration, int64, error) {
	names := strings.Split(series, ",")
	if series == "" || len(names) > maxSeries {
		return nil, 0, 0, 0, fmt.Errorf("series: want 1 to %d comma-separated names", maxSeries)
	}
	id := -1
	if node != "" {
		v, err := strconv.Atoi(node)
		if err != nil || v < 0 {
			return nil, 0, 0, 0, fmt.Errorf("node: want a configured id")
		}
		id = v
	}
	if rng == "" {
		rng = "15m"
	}
	span, ok := historyRanges[rng]
	if !ok {
		return nil, 0, 0, 0, fmt.Errorf("range: want 15m or 1h")
	}
	var after int64
	if since != "" {
		v, err := strconv.ParseInt(since, 10, 64)
		if err != nil || v < 0 {
			return nil, 0, 0, 0, fmt.Errorf("since: want Unix milliseconds")
		}
		after = v
	}
	return names, id, span, after, nil
}

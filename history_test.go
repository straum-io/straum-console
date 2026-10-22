package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

var oneNode = config{nodes: []node{{0, "10.0.0.1:9100"}}, interval: 2 * time.Second}

// commitAt is one round of one node at t ms after t0, with commit c and
// straum_leading l; ok false makes it unknown.
type commitAt struct {
	ms   int64
	c, l uint64
	ok   bool
}

func historyOf(t *testing.T, cfg config, rounds []commitAt) *history {
	t.Helper()
	t0 := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	h := newHistory(cfg.nodes)
	s := state{Epoch: "e1"}
	for _, r := range rounds {
		at := t0.Add(time.Duration(r.ms) * time.Millisecond)
		n := unknown("10.0.0.1:9100")
		if r.ok {
			n = observed("10.0.0.1:9100", at, values(0, r.l, 3, r.c))
		}
		s = nextState(s, metrics.Report{GeneratedAt: at, Nodes: []metrics.Node{n}}, cfg)
		h.add(s)
	}
	return h
}

func TestHistoryCommitRate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rounds []commitAt
		want   any
	}{
		{"steady", []commitAt{{0, 100, 1, true}, {2000, 120, 1, true}}, 10.0},
		{"the oldest round held", []commitAt{{0, 100, 1, true}}, nil},
		{"unknown before", []commitAt{{0, 100, 1, false}, {2000, 120, 1, true}}, nil},
		{"a round missed", []commitAt{{0, 100, 1, true}, {4000, 120, 1, true}}, nil},
		{"commit went backwards", []commitAt{{0, 120, 1, true}, {2000, 100, 1, true}}, nil},
		{"leader changed", []commitAt{{0, 100, 1, true}, {2000, 120, 0, true}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := historyOf(t, oneNode, tc.rounds).query("e1", "commit_rate", -1, time.Hour, 0)
			if err != nil {
				t.Fatal(err)
			}
			v := doc.Nodes[0].V
			if got := v[len(v)-1]; got != tc.want {
				t.Errorf("rate %v, want %v", got, tc.want)
			}
			if !doc.Derived {
				t.Error("commit_rate is derived")
			}
		})
	}
}

// The ring keeps the newest historyLen rounds, and a range returns at most
// what it holds, with the span it covers.
func TestHistoryRing(t *testing.T) {
	var rounds []commitAt
	for i := range historyLen + 5 {
		rounds = append(rounds, commitAt{int64(i) * 2000, uint64(i), 1, true})
	}
	h := historyOf(t, oneNode, rounds)
	doc, err := h.query("e1", "straum_commit", 0, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.T) != historyLen || doc.Nodes[0].V[0] != uint64(5) || doc.Nodes[0].V[historyLen-1] != uint64(historyLen+4) {
		t.Fatalf("%d points, first %v, last %v", len(doc.T), doc.Nodes[0].V[0], doc.Nodes[0].V[historyLen-1])
	}
	if span := doc.To.Sub(*doc.From); span != (historyLen-1)*2*time.Second {
		t.Errorf("span %s", span)
	}
	// since keeps only the rounds after it: here the last two.
	doc, _ = h.query("e1", "straum_commit", 0, time.Hour, doc.T[historyLen-3])
	if len(doc.T) != 2 || doc.Nodes[0].V[1] != uint64(historyLen+4) {
		t.Errorf("since: %d points, %v", len(doc.T), doc.Nodes[0].V)
	}
	doc, _ = h.query("e1", "straum_commit", 0, 15*time.Minute, 0)
	if len(doc.T) != 451 || doc.RangeMS != 900000 {
		t.Errorf("15m: %d points, range %d", len(doc.T), doc.RangeMS)
	}

	// At 1 s, 1 h asks for more than the ring holds: it gets the 30 min.
	h = historyOf(t, config{nodes: oneNode.nodes, interval: time.Second}, rounds)
	doc, _ = h.query("e1", "straum_commit", 0, time.Hour, 0)
	if len(doc.T) != historyLen {
		t.Errorf("1 s: %d points", len(doc.T))
	}
}

func TestHistoryQueryErrors(t *testing.T) {
	h := historyOf(t, oneNode, []commitAt{{0, 1, 1, true}})
	if _, err := h.query("e1", "straum_bogus", -1, time.Hour, 0); err == nil || !strings.Contains(err.Error(), "commit_rate") {
		t.Errorf("unknown series: %v", err)
	}
	if _, err := h.query("e1", "straum_commit", 7, time.Hour, 0); err == nil {
		t.Error("an unconfigured node should be an error")
	}
	many := strings.Repeat("straum_commit,", maxSeries) + "straum_commit"
	for _, q := range [][4]string{{"", "", "", ""}, {many, "", "", ""}, {"straum_commit", "x", "", ""}, {"straum_commit", "", "6h", ""}, {"straum_commit", "", "", "x"}} {
		if _, _, _, _, err := parseHistoryQuery(q[0], q[1], q[2], q[3]); err == nil {
			t.Errorf("%q should be refused", q)
		}
	}
}

// The example of docs/state-json.md, History: the gap series over the two
// rounds of the state example.
func TestGoldenHistory(t *testing.T) {
	h := newHistory(threeNodes.nodes)
	t0 := time.Date(2026, 10, 21, 11, 59, 58, 3e6, time.UTC)
	t1 := t0.Add(2000 * time.Millisecond)
	s := nextState(state{Epoch: "9f2c4e1ab07d5e33", Round: 1799}, metrics.Report{GeneratedAt: t0, Nodes: []metrics.Node{
		observed("10.0.0.1:9100", t0, values(0, 1, 3, 112)),
		observed("10.0.0.2:9100", t0, values(1, 0, 3, 110)),
		observed("10.0.0.3:9100", t0, values(2, 0, 3, 112)),
	}}, threeNodes)
	h.add(s)
	m := values(1, 0, 3, 116)
	m["straum_log_end"] = 120
	h.add(nextState(s, metrics.Report{GeneratedAt: t1, Nodes: []metrics.Node{
		observed("10.0.0.1:9100", t1, values(0, 1, 3, 120)),
		observed("10.0.0.2:9100", t1, m),
		unknown("10.0.0.3:9100"),
	}}, threeNodes))
	var got bytes.Buffer
	for _, series := range []string{"commit_rate", "log_end_minus_commit"} {
		doc, err := h.query("9f2c4e1ab07d5e33", series, -1, 15*time.Minute, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Fprintf(&got, "%s\n", b)
	}
	const path = "testdata/history.golden.json"
	if *update {
		os.WriteFile(path, got.Bytes(), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the history JSON changed; if intended, run make golden and check docs/state-json.md\ngot:\n%s", got.Bytes())
	}
}

func TestHistoryEndpoint(t *testing.T) {
	h := newHub("e1")
	url := serveHub(t, h)
	if code, _, _ := get(t, url+"/api/v1/history?series=straum_commit"); code != 503 {
		t.Errorf("no history kept: %d", code)
	}
	h.history = historyOf(t, oneNode, []commitAt{{0, 1, 1, true}})
	if code, body, _ := get(t, url+"/api/v1/history"); code != 400 || !strings.Contains(body, "series") {
		t.Errorf("no series: %d %s", code, body)
	}
	if code, body, _ := get(t, url+"/api/v1/history?series=straum_commit,straum_bogus"); code != 400 || !strings.Contains(body, "series") {
		t.Errorf("one unknown series in a list: %d %s", code, body)
	}
	code, body, hdr := get(t, url+"/api/v1/history?series=straum_commit,commit_rate&node=0&range=1h")
	var res struct{ Histories []historyDoc }
	if code != 200 || hdr.Get("Content-Type") != "application/json" || json.Unmarshal([]byte(body), &res) != nil || len(res.Histories) != 2 {
		t.Fatalf("%d %s", code, body)
	}
	for i, doc := range res.Histories {
		if doc.SchemaVersion != 1 || doc.Epoch != "e1" || doc.Series != []string{"straum_commit", "commit_rate"}[i] || len(doc.Nodes) != 1 || len(doc.T) != 1 {
			t.Errorf("%d: %+v", i, doc)
		}
	}
}

func TestNodeCap(t *testing.T) {
	var spec []string
	for i := range maxNodes + 1 {
		spec = append(spec, fmt.Sprintf("%d=127.0.0.1:%d", i, 9100+i))
	}
	if _, err := parseConfig([]string{"--node", strings.Join(spec, ",")}, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "at most 15") {
		t.Errorf("16 nodes: %v", err)
	}
	if _, err := parseConfig([]string{"--node", strings.Join(spec[:maxNodes], ",")}, nil, io.Discard); err != nil {
		t.Errorf("15 nodes: %v", err)
	}
}

// One request's series are read under one lock: they cover the same rounds.
func TestHistoryQueryAll(t *testing.T) {
	h := historyOf(t, oneNode, []commitAt{{0, 1, 1, true}, {2000, 3, 1, true}, {4000, 5, 1, true}})
	docs, err := h.queryAll("e1", []string{"straum_commit", "commit_rate"}, -1, time.Hour, 0)
	if err != nil || len(docs) != 2 || !slices.Equal(docs[0].T, docs[1].T) || len(docs[0].T) != 3 {
		t.Fatalf("%v %+v", err, docs)
	}
	if _, err := h.queryAll("e1", []string{"straum_commit", "straum_bogus"}, -1, time.Hour, 0); err == nil {
		t.Error("an unknown series in the list should be an error")
	}
}

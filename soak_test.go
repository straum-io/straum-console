//go:build soak

package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSoak runs the console binary (make soak builds it) against 5 fixture
// nodes whose positions advance and whose leader moves, with 64 streams
// reading, for SOAK_DURATION (default 1h). It samples RSS, CPU time and the
// heap in use every 10 s, writes them to SOAK_OUT (CSV) if set, and checks
// plan §14: RSS at most 40 MB, CPU at most 1 % of a core on average, and the
// heap flat (the last quarter's median within 20 % of the second's).
func TestSoak(t *testing.T) {
	duration := time.Hour
	if v := os.Getenv("SOAK_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		duration = d
	}
	const nodeCount, streams = 5, 64

	// Fixture nodes: the core's own render, with positions that advance and
	// a leader that moves every 30 s, so rounds and events keep coming.
	base, err := os.ReadFile("testdata/node0.prom")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var spec []string
	for i := range nodeCount {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s := int(time.Since(start).Seconds())
			pos := 10 + s*7
			leading := 0
			if (s/30)%nodeCount == i {
				leading = 1
			}
			body := string(base)
			for k, v := range map[string]int{
				"straum_node": i, "straum_leading": leading, "straum_slot": 1 + s/30,
				"straum_log_end": pos, "straum_log_durable": pos, "straum_commit": pos - i, "straum_applied": pos - i,
				"straum_lag": i % 2, "straum_outbox_entries": s % 7,
			} {
				body = regexp.MustCompile(`(?m)^`+k+` \d+$`).ReplaceAllString(body, fmt.Sprintf("%s %d", k, v))
			}
			io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		spec = append(spec, fmt.Sprintf("%d=%s", i, strings.TrimPrefix(srv.URL, "http://")))
	}

	listen, debug := freeAddr(t), freeAddr(t)
	cmd := exec.Command("bin/straum-console", "--node", strings.Join(spec, ","), "--interval", "2s", "--listen", listen, "--debug-listen", debug)
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitReady(t, "http://"+listen)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var read atomic.Int64
	var open atomic.Int32 // streams still reading
	for range streams {
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+listen+"/api/v1/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("stream: %v %v", err, resp)
		}
		open.Add(1)
		go func() {
			defer open.Add(-1)
			buf := make([]byte, 32<<10)
			for {
				n, err := resp.Body.Read(buf)
				read.Add(int64(n))
				if err != nil {
					return
				}
			}
		}()
	}

	var out *csv.Writer
	if p := os.Getenv("SOAK_OUT"); p != "" {
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		out = csv.NewWriter(f)
		defer out.Flush()
		out.Write([]string{"elapsed_s", "rss_kib", "cpu_s", "heap_inuse_bytes", "stream_bytes"})
	}
	var rss, heap []int64
	var cpu float64
	began := time.Now()
	for time.Since(began) < duration {
		time.Sleep(10 * time.Second)
		r, c := psSample(t, cmd.Process.Pid)
		h := heapInuse(t, "http://"+debug)
		rss, heap, cpu = append(rss, r), append(heap, h), c
		if out != nil {
			out.Write([]string{fmt.Sprint(int(time.Since(began).Seconds())), fmt.Sprint(r), fmt.Sprintf("%.2f", c), fmt.Sprint(h), fmt.Sprint(read.Load())})
			out.Flush()
		}
	}

	elapsed := time.Since(began).Seconds()
	maxRSS := slices.Max(rss)
	cpuShare := 100 * cpu / elapsed
	t.Logf("%d samples over %.0f s: max RSS %d KiB, CPU %.2f %% of a core, %d stream bytes read",
		len(rss), elapsed, maxRSS, cpuShare, read.Load())
	// Growth needs a series: quarters of at least two samples.
	if len(heap) >= 8 {
		q := len(heap) / 4
		early, late := median(heap[q:2*q]), median(heap[3*q:])
		t.Logf("heap in use (median): %d bytes in the second quarter, %d in the last", early, late)
		if float64(late) > 1.2*float64(early) {
			t.Errorf("heap grew: %d → %d bytes", early, late)
		}
	} else {
		t.Logf("too short to judge heap growth: %d samples", len(heap))
	}
	if maxRSS*1024 > 40e6 {
		t.Errorf("RSS %d KiB over the 40 MB budget", maxRSS)
	}
	if n := open.Load(); n != streams {
		t.Errorf("%d of %d streams were still open at the end", n, streams)
	}
	if cpuShare > 1 {
		t.Errorf("CPU %.2f %% over the 1 %% budget", cpuShare)
	}
	if read.Load() == 0 {
		t.Error("the streams read nothing")
	}
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	for range 100 {
		if resp, err := http.Get(url + "/readyz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the console did not become ready")
}

// psSample returns the process's resident set (KiB) and its CPU time so far.
func psSample(t *testing.T, pid int) (int64, float64) {
	t.Helper()
	b, err := exec.Command("ps", "-o", "rss=,time=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		t.Fatalf("ps: %q", b)
	}
	r, _ := strconv.ParseInt(f[0], 10, 64)
	return r, cpuSeconds(f[1])
}

// cpuSeconds reads ps's TIME: [[dd-]hh:]mm:ss[.cc].
func cpuSeconds(s string) float64 {
	days := 0.0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		v, _ := strconv.ParseFloat(d, 64)
		days, s = v, rest
	}
	total := 0.0
	for _, part := range strings.Split(s, ":") {
		v, _ := strconv.ParseFloat(part, 64)
		total = total*60 + v
	}
	return days*86400 + total
}

// heapInuse reads HeapInuse from the debug listener's heap profile.
func heapInuse(t *testing.T, debug string) int64 {
	t.Helper()
	resp, err := http.Get(debug + "/debug/pprof/heap?debug=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "# HeapInuse = "); ok {
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	t.Fatal("no HeapInuse in the heap profile")
	return 0
}

func median(v []int64) int64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s[len(s)/2]
}

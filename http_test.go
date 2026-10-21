package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func serveHub(t *testing.T, h *hub) string {
	t.Helper()
	srv := httptest.NewServer(routes(h))
	t.Cleanup(func() { h.close(); srv.Close() })
	return srv.URL
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func ev(name string) event { return event{SchemaVersion: 1, Source: "console", Name: name} }

func TestStateAndHealth(t *testing.T) {
	h := newHub("e1")
	url := serveHub(t, h)
	if code, _, _ := get(t, url+"/api/v1/state"); code != 503 {
		t.Errorf("state before the first round: %d", code)
	}
	if code, _, _ := get(t, url+"/readyz"); code != 503 {
		t.Errorf("readyz before the first round: %d", code)
	}
	if code, body, _ := get(t, url+"/healthz"); code != 200 || body != "ok\n" {
		t.Errorf("healthz: %d %q", code, body)
	}
	h.publish(exampleState(), nil)
	code, body, hdr := get(t, url+"/api/v1/state")
	var s state
	if code != 200 || json.Unmarshal([]byte(body), &s) != nil || s.Round != 1801 || hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("state: %d %s", code, body)
	}
	if hdr.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(hdr.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("headers: %v", hdr)
	}
	if code, _, _ := get(t, url+"/readyz"); code != 200 {
		t.Errorf("readyz: %d", code)
	}
}

func TestOnlyGet(t *testing.T) {
	url := serveHub(t, newHub("e1"))
	for _, path := range []string{"/api/v1/state", "/api/v1/events", "/api/v1/stream", "/healthz"} {
		resp, err := http.Post(url+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, resp.StatusCode)
		}
	}
	if code, _, _ := get(t, url+"/api/v1/nope"); code != 404 {
		t.Errorf("unknown path: %d", code)
	}
}

func TestEventsQuery(t *testing.T) {
	h := newHub("e1")
	url := serveHub(t, h)
	h.publish(exampleState(), []event{ev("a"), ev("b"), ev("c")})
	code, body, _ := get(t, url+"/api/v1/events?after=1&limit=1")
	var got struct{ Events []event }
	if code != 200 || json.Unmarshal([]byte(body), &got) != nil || len(got.Events) != 1 || got.Events[0].ID != 2 || got.Events[0].Name != "b" || got.Events[0].Epoch != "e1" {
		t.Fatalf("%d %s", code, body)
	}
	if _, body, _ := get(t, url+"/api/v1/events?after=3"); !strings.Contains(body, `"events":[]`) {
		t.Errorf("no events: %s", body)
	}
	for _, q := range []string{"after=x", "after=-1", "limit=0", "limit=2001", "limit=x"} {
		if code, _, _ := get(t, url+"/api/v1/events?"+q); code != 400 {
			t.Errorf("%s: %d", q, code)
		}
	}
}

// sse reads frames from an open stream, each as its field lines.
type sse struct {
	resp *http.Response
	r    *bufio.Reader
}

func openStream(t *testing.T, url, lastEventID string) *sse {
	t.Helper()
	req, _ := http.NewRequest("GET", url+"/api/v1/stream", nil)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &sse{resp, bufio.NewReader(resp.Body)}
}

func (s *sse) next(t *testing.T) map[string]string {
	t.Helper()
	f := map[string]string{}
	done := make(chan error, 1)
	go func() {
		for {
			line, err := s.r.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				if len(f) > 0 {
					done <- nil
					return
				}
				continue
			}
			k, v, _ := strings.Cut(line, ": ")
			f[k] = v
		}
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stream ended: %v (frame so far %v)", err, f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame within 5s")
	}
	return f
}

func TestStream(t *testing.T) {
	h := newHub("e1")
	url := serveHub(t, h)
	h.publish(exampleState(), []event{ev("old")})

	s := openStream(t, url, "")
	if s.resp.StatusCode != 200 || s.resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%d %v", s.resp.StatusCode, s.resp.Header)
	}
	if f := s.next(t); f["retry"] != "2000" {
		t.Fatalf("first: %v", f)
	}
	if f := s.next(t); f["event"] != "state" || f["id"] != "" || !strings.Contains(f["data"], `"round":1801`) {
		t.Fatalf("state: %v", f)
	}
	// Live: the round's state, then its event, with an id that names the epoch.
	h.publish(exampleState(), []event{ev("new")})
	if f := s.next(t); f["event"] != "state" {
		t.Fatalf("live state: %v", f)
	}
	f := s.next(t)
	if f["event"] != "event" || f["id"] != "e1/2" || !strings.Contains(f["data"], `"name":"new"`) {
		t.Fatalf("live event: %v", f)
	}

	// A reconnect replays what came after its last id.
	r := openStream(t, url, "e1/1")
	r.next(t) // retry
	r.next(t) // state
	if f := r.next(t); f["id"] != "e1/2" {
		t.Fatalf("replay: %v", f)
	}

	// From another epoch (or malformed): a gap, then live.
	for _, id := range []string{"e0/7", "garbage"} {
		g := openStream(t, url, id)
		g.next(t)
		g.next(t)
		if f := g.next(t); f["event"] != "gap" || !strings.Contains(f["data"], `"reason":"console_restarted"`) {
			t.Fatalf("%s: %v", id, f)
		}
	}
}

func TestStreamEvicted(t *testing.T) {
	h := newHub("e1")
	h.events.size = 2
	url := serveHub(t, h)
	h.publish(exampleState(), []event{ev("1"), ev("2"), ev("3"), ev("4")})
	s := openStream(t, url, "e1/1")
	s.next(t)
	s.next(t)
	if f := s.next(t); f["event"] != "gap" || !strings.Contains(f["data"], `"reason":"evicted"`) || !strings.Contains(f["data"], `"since_id":1`) {
		t.Fatalf("gap: %v", f)
	}
	// What is still held follows the gap.
	if f := s.next(t); f["id"] != "e1/3" {
		t.Fatalf("after the gap: %v", f)
	}
}

func TestSlowStreamDropped(t *testing.T) {
	h := newHub("e1")
	h.queue = 2
	_, c, ok := h.subscribe("")
	if !ok {
		t.Fatal("subscribe")
	}
	// Nobody reads c: the third message overflows it, and the collector is
	// not held up.
	done := make(chan struct{})
	go func() {
		h.publish(exampleState(), []event{ev("a"), ev("b")})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on a slow stream")
	}
	n := 0
	for range c { // closed by the drop
		n++
	}
	if n != 2 || len(h.clients) != 0 {
		t.Errorf("queued %d, clients %d", n, len(h.clients))
	}
}

func TestStreamCap(t *testing.T) {
	h := newHub("e1")
	h.maxClient = 1
	url := serveHub(t, h)
	s := openStream(t, url, "")
	s.next(t)
	if code, _, _ := get(t, url+"/api/v1/stream"); code != 503 {
		t.Errorf("second stream: %d", code)
	}
	// Closing the hub ends the open stream and refuses new ones.
	h.close()
	ended := make(chan struct{})
	go func() { io.ReadAll(s.r); close(ended) }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end when the hub closed")
	}
	if code, _, _ := get(t, url+"/api/v1/stream"); code != 503 {
		t.Errorf("after close: %d", code)
	}
}

func TestHostMustBeLoopback(t *testing.T) {
	h := newHub("e1")
	url := serveHub(t, h)
	h.publish(exampleState(), nil)
	for host, want := range map[string]int{
		"evil.example":      421, // a rebinding page's own name
		"evil.example:8080": 421,
		"10.0.0.5:8080":     421,
		"localhost:8080":    200,
		"127.0.0.1:8080":    200,
		"[::1]:8080":        200,
		"localhost":         200,
	} {
		req, _ := http.NewRequest("GET", url+"/api/v1/state", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %s: %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestHeadStreamTakesNoSlot(t *testing.T) {
	h := newHub("e1")
	h.maxClient = 1
	url := serveHub(t, h)
	resp, err := http.Head(url + "/api/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || len(h.clients) != 0 {
		t.Fatalf("HEAD: %d, clients %d", resp.StatusCode, len(h.clients))
	}
	openStream(t, url, "").next(t) // the one slot is still free
}

func TestFrontend(t *testing.T) {
	url := serveHub(t, newHub("e1"))
	code, body, hdr := get(t, url+"/")
	if code != 200 || !strings.Contains(body, `<script type="module" src="/app.js">`) || hdr.Get("Cache-Control") != "no-cache" {
		t.Fatalf("/: %d %q %v", code, hdr.Get("Cache-Control"), body)
	}
	if !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("CSP on the page: %q", hdr.Get("Content-Security-Policy"))
	}
	if code, _, hdr := get(t, url+"/fonts/inter-v20.woff2"); code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
		t.Errorf("font: %d %q", code, hdr.Get("Cache-Control"))
	}
	if code, body, _ := get(t, url+"/licenses.txt"); code != 200 || !strings.Contains(body, "preact") {
		t.Errorf("licences: %d", code)
	}
	if code, _, _ := get(t, url+"/app.js"); code != 200 {
		t.Errorf("app.js: %d", code)
	}
}

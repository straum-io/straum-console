package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const aliceHash = "$argon2id$v=19$m=19456,t=2,p=1$2v0Y0ByCXIuem6HOv45dng$ueTcbjAQP/ap3joHB1gc0dyG8WUTrCRWON3YqPeFIKg"

func testAuth(t *testing.T, text string) *auth {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passwords")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := newAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// A hash made by the core's straum-server hash-password verifies here, and
// every answer, right, wrong or for an unknown user, costs one hash.
func TestCoreHash(t *testing.T) {
	a, err := newAuth("testdata/passwords")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, password string
		want           bool
	}{
		{"alice", "correct horse", true},
		{"alice", "correct horse ", false},
		{"bob", "correct horse", false},
		{"", "", false},
	} {
		before := a.checked.Load()
		ok, err := a.check(context.Background(), tc.user, tc.password)
		if err != nil || ok != tc.want || a.checked.Load() != before+1 {
			t.Errorf("%q/%q: %v %v, %d hashes", tc.user, tc.password, ok, err, a.checked.Load()-before)
		}
	}
}

func TestPasswordFile(t *testing.T) {
	for _, tc := range []struct{ name, text, err string }{
		{"comments and blanks", "# x\n\n  alice:" + aliceHash + "  \n", ""},
		{"no colon", "alice " + aliceHash, "line 1: want user:hash"},
		{"no user", ":" + aliceHash, "want user:hash"},
		{"other memory cost", "alice:" + strings.Replace(aliceHash, "m=19456", "m=65536", 1), "m=19456,t=2,p=1"},
		{"argon2i", "alice:" + strings.Replace(aliceHash, "argon2id", "argon2i", 1), "want an argon2id hash"},
		{"bad base64", "alice:" + strings.Replace(aliceHash, "2v0Y", "2v0*", 1), "base64"},
		{"no users", "# nobody\n", "no users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePasswords(tc.text)
			if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
				t.Errorf("err = %v, want %q", err, tc.err)
			}
		})
	}
}

func TestSource(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.1:5000":             "10.0.0.1",
		"[::ffff:10.0.0.1]:5000":    "10.0.0.1",
		"[2001:db8:1:2:3::1]:5000":  "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff::]:443": "2001:db8:1:2::/64",
	} {
		if got := source(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestRateLimit(t *testing.T) {
	a := testAuth(t, "alice:"+aliceHash)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	for i := range attemptsPerMinute {
		if !a.allow("10.0.0.1") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if a.allow("10.0.0.1") || !a.allow("10.0.0.2") {
		t.Fatal("the limit is per source")
	}
	now = now.Add(time.Minute)
	if !a.allow("10.0.0.1") {
		t.Fatal("a new minute, a new window")
	}
	// The table stays at maxSources: the oldest window goes.
	for i := range maxSources + 10 {
		a.allow(fmt.Sprint("src", i))
		now = now.Add(time.Millisecond)
	}
	if len(a.sources) != maxSources {
		t.Errorf("%d sources held", len(a.sources))
	}
	// A full line of sign-ins: the next is refused before it waits.
	a.inLine.Store(maxInLine)
	if _, err := a.check(context.Background(), "alice", "x"); !errors.Is(err, errBusy) {
		t.Errorf("full line: %v", err)
	}
}

func TestSessions(t *testing.T) {
	a := testAuth(t, "alice:"+aliceHash)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	req := func(token string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		return r
	}
	token := a.open("alice")
	if u, ok := a.user(req(token)); !ok || u != "alice" {
		t.Fatal("a fresh session")
	}
	if _, ok := a.user(req(token + "x")); ok {
		t.Fatal("an unknown token")
	}
	now = now.Add(sessionTTL)
	if _, ok := a.user(req(token)); ok {
		t.Fatal("an expired session")
	}
	for range maxSessions + 5 {
		a.open("alice")
	}
	if len(a.sessions) != maxSessions {
		t.Errorf("%d sessions held", len(a.sessions))
	}

	// A user who leaves the file loses the session; a file that does not
	// load keeps the users as they were.
	token = a.open("alice")
	os.WriteFile(a.path, []byte("not a password file"), 0o600)
	if a.reload() == nil {
		t.Fatal("a broken file should not load")
	}
	if _, ok := a.user(req(token)); !ok {
		t.Fatal("a failed reload ended a session")
	}
	os.WriteFile(a.path, []byte("bob:"+aliceHash), 0o600)
	if err := a.reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.user(req(token)); ok {
		t.Fatal("alice left the file and kept her session")
	}
}

// The whole flow over TLS, as a browser would: the API refuses without a
// session, a sign-in sets a Secure, HttpOnly, SameSite=Strict cookie, and
// signing out ends it.
func TestSignIn(t *testing.T) {
	h := newHub("e1")
	h.publish(exampleState(), nil)
	a := testAuth(t, "alice:"+aliceHash)
	srv := httptest.NewTLSServer(routes(h, a, []string{"console.example"}))
	t.Cleanup(func() { h.close(); srv.Close() })
	c := srv.Client()
	c.Jar, _ = cookiejar.New(nil)
	do := func(method, path string, form url.Values, hdr ...string) *http.Response {
		t.Helper()
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			if hdr[i] == "Host" {
				req.Host = hdr[i+1]
			} else {
				req.Header.Set(hdr[i], hdr[i+1])
			}
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for _, p := range []string{"/api/v1/state", "/api/v1/session", "/api/v1/events", "/api/v1/stream", "/api/v1/history?series=straum_commit"} {
		if code := do("GET", p, nil).StatusCode; code != 401 {
			t.Errorf("%s without a session: %d", p, code)
		}
	}
	for _, p := range []string{"/", "/healthz", "/readyz"} {
		if code := do("GET", p, nil).StatusCode; code != 200 {
			t.Errorf("%s is public: %d", p, code)
		}
	}
	if code := do("POST", "/login", url.Values{"user": {"alice"}, "password": {"wrong"}}).StatusCode; code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code := do("POST", "/login", url.Values{"user": {"alice"}, "password": {"correct horse"}}, "Sec-Fetch-Site", "cross-site").StatusCode; code != 403 {
		t.Errorf("a cross-site sign-in: %d", code)
	}
	resp := do("POST", "/login", url.Values{"user": {"alice"}, "password": {"correct horse"}})
	if resp.StatusCode != 204 {
		t.Fatalf("sign-in: %d", resp.StatusCode)
	}
	set := resp.Header.Get("Set-Cookie")
	for _, flag := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=/", "Max-Age=43200"} {
		if !strings.Contains(set, flag) {
			t.Errorf("cookie %q lacks %s", set, flag)
		}
	}
	if resp.Header.Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS over TLS")
	}
	if code := do("GET", "/api/v1/state", nil).StatusCode; code != 200 {
		t.Errorf("state signed in: %d", code)
	}
	if code := do("GET", "/healthz", nil, "Host", "Console.Example:8443").StatusCode; code != 200 {
		t.Errorf("a public host: %d", code)
	}
	if code := do("GET", "/healthz", nil, "Host", "evil.example").StatusCode; code != 421 {
		t.Errorf("another host: %d", code)
	}
	if code := do("POST", "/logout", nil).StatusCode; code != 204 {
		t.Errorf("sign-out: %d", code)
	}
	if code := do("GET", "/api/v1/state", nil).StatusCode; code != 401 {
		t.Errorf("state after sign-out: %d", code)
	}
	// The rate limit counts every attempt from a source: two above (the
	// cross-site one was refused before counting), so eight more pass and
	// the next is refused before any hash.
	for range attemptsPerMinute - 2 {
		do("POST", "/login", url.Values{"user": {"x"}, "password": {"y"}})
	}
	before := a.checked.Load()
	resp = do("POST", "/login", url.Values{"user": {"alice"}, "password": {"correct horse"}})
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "60" || a.checked.Load() != before {
		t.Errorf("past the limit: %d, Retry-After %q, %d hashes", resp.StatusCode, resp.Header.Get("Retry-After"), a.checked.Load()-before)
	}
}

// Without a password file the session document names no user.
func TestSessionWithoutSignIn(t *testing.T) {
	url := serveHub(t, newHub("e1"))
	if code, body, _ := get(t, url+"/api/v1/session"); code != 200 || body != "{\"schema_version\":1,\"user\":null}\n" {
		t.Errorf("%d %s", code, body)
	}
	if code, _, _ := get(t, url+"/login"); code != 405 && code != 404 {
		t.Errorf("no sign-in without a password file: %d", code)
	}
}

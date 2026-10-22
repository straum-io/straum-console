package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/argon2"
)

// Authentication for a console that leaves loopback (CON-D-8). The password
// file and its hash are the core's (straum docs/configuration.md, "Access
// rules"); the rest bounds what a login can cost.
const (
	// straum-server hash-password's parameters, the only ones accepted: a
	// refusal costs one hash with them, so a stored hash made with others
	// would let a known user name be told from an unknown one by its time.
	argonTime    = 2
	argonMemory  = 19456 // KiB: each check holds about 19 MiB while it runs
	argonThreads = 1

	sessionTTL  = 12 * time.Hour
	maxSessions = 1024
	cookieName  = "straum_console_session"

	maxInLine         = 64 // logins running or waiting for the one check slot
	attemptsPerMinute = 10 // per source
	maxSources        = 4096
)

var errBusy = errors.New("too many sign-ins at once; try again shortly")

// credential is one user's argon2id salt and key.
type credential struct{ salt, key []byte }

// decoy is what an unknown user is checked against, so the refusal takes as
// long as for a known one. The core uses the same string.
var decoy = func() credential {
	c, err := parsePHC("$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHRzb21lc2FsdA$4xSKQ1Cz5rHEBZH1TkIhNRxrN2hMEa0Wo5Gy5tZ5rRE")
	if err != nil {
		panic(err)
	}
	return c
}()

// parsePHC reads "$argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>", unpadded
// base64 as the PHC format has it, and refuses any other parameters.
func parsePHC(s string) (credential, error) {
	f := strings.Split(s, "$")
	want := fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads)
	if len(f) != 6 || f[0] != "" || f[1] != "argon2id" || f[2] != "v=19" || f[3] != want {
		return credential{}, fmt.Errorf("want an argon2id hash with %s, as straum-server hash-password prints it", want)
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(f[4])
	key, err2 := base64.RawStdEncoding.DecodeString(f[5])
	if err1 != nil || err2 != nil || len(salt) < 8 || len(key) < 16 {
		return credential{}, errors.New("the salt or hash is not valid base64 of the usual length")
	}
	return credential{salt, key}, nil
}

// parsePasswords reads the core's password file: one user:hash per line,
// # comments and blank lines ignored. A line that could never match is an
// error now rather than one failed login at a time.
func parsePasswords(text string) (map[string]credential, error) {
	users := map[string]credential{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, hash, ok := strings.Cut(line, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("line %d: want user:hash", i+1)
		}
		c, err := parsePHC(hash)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		users[name] = c
	}
	if len(users) == 0 {
		return nil, errors.New("no users: nobody could sign in")
	}
	return users, nil
}

type session struct {
	user    string
	expires time.Time
}

type window struct {
	start time.Time
	n     int
}

type auth struct {
	path    string
	users   atomic.Pointer[map[string]credential]
	turn    chan struct{} // the one check slot
	inLine  atomic.Int32
	checked atomic.Int64 // hashes computed, for the tests

	mu       sync.Mutex
	sessions map[string]session
	sources  map[string]window
	now      func() time.Time
}

// newAuth reads the password file at path.
func newAuth(path string) (*auth, error) {
	a := &auth{path: path, turn: make(chan struct{}, 1), sessions: map[string]session{}, sources: map[string]window{}, now: time.Now}
	return a, a.reload()
}

// reload re-reads the password file; on an error the users stay as they
// were. A session whose user is gone ends at its next request.
func (a *auth) reload() error {
	b, err := os.ReadFile(a.path)
	if err != nil {
		return fmt.Errorf("password file: %w", err)
	}
	users, err := parsePasswords(string(b))
	if err != nil {
		return fmt.Errorf("password file %s: %w", a.path, err)
	}
	a.users.Store(&users)
	return nil
}

// check verifies one password. Every answer costs exactly one hash; at most
// one runs at a time and maxInLine wait, beyond that errBusy.
func (a *auth) check(ctx context.Context, user, password string) (bool, error) {
	if a.inLine.Add(1) > maxInLine {
		a.inLine.Add(-1)
		return false, errBusy
	}
	defer a.inLine.Add(-1)
	select {
	case a.turn <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	// Each hash allocates about 19 MiB that the runtime would otherwise keep
	// for a while: returned before the next check may start, so sign-ins
	// do not take the console past its 40 MB budget.
	defer func() { debug.FreeOSMemory(); <-a.turn }()
	c, known := (*a.users.Load())[user]
	if !known {
		c = decoy
	}
	a.checked.Add(1)
	key := argon2.IDKey([]byte(password), c.salt, argonTime, argonMemory, argonThreads, uint32(len(c.key)))
	return subtle.ConstantTimeCompare(key, c.key) == 1 && known, nil
}

// source is who a rate limit counts against: an IPv4 address, or an IPv6
// /64, since one host commonly holds a whole one. Behind a proxy every
// client is the proxy.
func source(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// allow counts one sign-in attempt from src: attemptsPerMinute in a fixed
// window. The table holds maxSources; when full, expired windows go, then
// the oldest.
func (a *auth) allow(src string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	w, ok := a.sources[src]
	if !ok || now.Sub(w.start) >= time.Minute {
		if !ok && len(a.sources) >= maxSources {
			// ponytail: O(n) sweep per new source once full; fine at 4,096 and sign-in rates.
			oldest := ""
			for k, v := range a.sources {
				if now.Sub(v.start) >= time.Minute {
					delete(a.sources, k)
				} else if oldest == "" || v.start.Before(a.sources[oldest].start) {
					oldest = k
				}
			}
			if len(a.sources) >= maxSources {
				delete(a.sources, oldest)
			}
		}
		w = window{start: now}
	}
	if w.n >= attemptsPerMinute {
		a.sources[src] = w
		return false
	}
	w.n++
	a.sources[src] = w
	return true
}

// open starts a session for user and returns its token. At maxSessions the
// one closest to expiring goes.
func (a *auth) open(user string) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if len(a.sessions) >= maxSessions {
		first := ""
		for k, s := range a.sessions {
			if !now.Before(s.expires) {
				delete(a.sessions, k)
			} else if first == "" || s.expires.Before(a.sessions[first].expires) {
				first = k
			}
		}
		if len(a.sessions) >= maxSessions {
			delete(a.sessions, first)
		}
	}
	a.sessions[token] = session{user, now.Add(sessionTTL)}
	return token
}

// user is the signed-in user of r, if its session is live and the user is
// still in the password file.
func (a *auth) user(r *http.Request) (string, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return "", false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[c.Value]
	if !ok || !a.now().Before(s.expires) {
		delete(a.sessions, c.Value)
		return "", false
	}
	if _, known := (*a.users.Load())[s.user]; !known {
		delete(a.sessions, c.Value)
		return "", false
	}
	return s.user, true
}

func (a *auth) close(r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
}

func sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

// authRoutes adds /login, /logout and /api/v1/session to mux. With a nil a
// (no password file) only the session document exists, naming no user.
func authRoutes(mux *http.ServeMux, a *auth) {
	mux.HandleFunc("GET /api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]any{"schema_version": 1, "user": nil}
		if a != nil {
			u, ok := a.user(r)
			if !ok {
				jsonError(w, http.StatusUnauthorized, "sign in")
				return
			}
			doc["user"] = u
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(doc)
	})
	if a == nil {
		return
	}
	csrf := http.NewCrossOriginProtection()
	mux.Handle("POST /login", csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allow(source(r.RemoteAddr)) {
			w.Header().Set("Retry-After", "60")
			jsonError(w, http.StatusTooManyRequests, fmt.Sprintf("more than %d sign-in attempts a minute from this address", attemptsPerMinute))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		if err := r.ParseForm(); err != nil {
			jsonError(w, http.StatusBadRequest, "want a form with user and password")
			return
		}
		user := r.PostForm.Get("user")
		ok, err := a.check(r.Context(), user, r.PostForm.Get("password"))
		if errors.Is(err, errBusy) {
			w.Header().Set("Retry-After", "5")
			jsonError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		if err != nil || !ok {
			jsonError(w, http.StatusUnauthorized, "wrong user name or password")
			return
		}
		http.SetCookie(w, sessionCookie(a.open(user), int(sessionTTL.Seconds())))
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.Handle("POST /logout", csrf.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.close(r)
		http.SetCookie(w, sessionCookie("", -1))
		w.WriteHeader(http.StatusNoContent)
	})))
}

// signedIn refuses an /api/v1 request without a live session, except the
// session document, which answers for itself.
func signedIn(a *auth, next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/v1/session" {
			if _, ok := a.user(r); !ok {
				jsonError(w, http.StatusUnauthorized, "sign in")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

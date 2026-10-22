package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"
)

// checkListen allows a loopback IP only: the console has no authentication,
// so nothing of it may be reachable from another host (CON-D-2 A, D-138).
// A host name is refused too, since it could resolve beyond loopback.
func checkListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || port == "" || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen %q: want a loopback address such as 127.0.0.1:8080; the console has no authentication, so it never listens beyond loopback (open it with ssh -L)", addr)
	}
	return nil
}

// loopbackHost accepts localhost or a loopback IP, with or without a port.
func loopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.Trim(hostport, "[]")
	}
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

const writeTimeout = 10 * time.Second

// routes serves /api/v1 of docs/state-json.md and the frontend at /. A
// method pattern makes the mux answer 405 to anything but GET and HEAD.
func routes(h *hub) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n")) // the console runs; it says nothing about the cluster
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if h.latest() == nil {
			http.Error(w, "no round has closed yet", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		s := h.latest()
		if s == nil {
			jsonError(w, http.StatusServiceUnavailable, "no round has closed yet")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(s)
	})
	mux.HandleFunc("GET /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		after, limit := uint64(0), 2000
		var err error
		if v := r.URL.Query().Get("after"); v != "" {
			if after, err = strconv.ParseUint(v, 10, 64); err != nil {
				jsonError(w, http.StatusBadRequest, "after: want an event id")
				return
			}
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 2000 {
				jsonError(w, http.StatusBadRequest, "limit: want 1 to 2000")
				return
			}
		}
		evs := h.after(after, limit)
		if evs == nil {
			evs = []event{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]event{"events": evs})
	})
	mux.HandleFunc("GET /api/v1/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		series, node, span, since, err := parseHistoryQuery(q.Get("series"), q.Get("node"), q.Get("range"), q.Get("since"))
		if err == nil && h.history == nil {
			jsonError(w, http.StatusServiceUnavailable, "no history is kept")
			return
		}
		var docs []historyDoc
		if err == nil {
			docs, err = h.history.queryAll(h.epoch, series, node, span, since)
		}
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]historyDoc{"histories": docs})
	})
	mux.HandleFunc("GET /api/v1/stream", func(w http.ResponseWriter, r *http.Request) { stream(w, r, h) })
	mux.Handle("GET /", static())
	return headers(mux)
}

func stream(w http.ResponseWriter, r *http.Request, h *hub) {
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/event-stream")
		return // takes no stream slot
	}
	first, c, ok := h.subscribe(r.Header.Get("Last-Event-ID"))
	if !ok {
		jsonError(w, http.StatusServiceUnavailable, "too many streams open")
		return
	}
	defer h.unsubscribe(c)
	rc := http.NewResponseController(w)
	// The request is read; without this the server's ReadTimeout would end
	// a healthy stream.
	rc.SetReadDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(b []byte) bool {
		rc.SetWriteDeadline(time.Now().Add(writeTimeout))
		_, err := w.Write(b)
		return err == nil && rc.Flush() == nil
	}
	if !send(first) {
		return
	}
	for {
		select {
		case msg, open := <-c:
			if !open || !send(msg) {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// headers sets the security headers and refuses a request whose Host is not
// loopback: a page in the operator's browser could otherwise point a name it
// controls at 127.0.0.1 (DNS rebinding) and read the API as same-origin.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			jsonError(w, http.StatusMisdirectedRequest, "Host must be localhost or a loopback address")
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// debugRoutes is net/http/pprof on its own mux, behind the same Host check
// as the API. Profiles can take longer than a request timeout, so its
// server sets none but the header read.
func debugRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return headers(mux)
}

func newServer(h *hub) *http.Server {
	return &http.Server{
		Handler:           routes(h),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      writeTimeout, // the stream moves its own deadline per write
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
}

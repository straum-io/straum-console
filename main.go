// straum-console is a read-only web console for a Straum cluster. It polls
// the nodes' metrics endpoints and serves the /api/v1 of docs/state-json.md,
// on loopback, or beyond it with TLS and sign-in (CON-D-8).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
)

// version and commit are set at build time (-ldflags -X main.version=…).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr, nil))
}

// certificate is the served TLS certificate, replaced whole on reload.
type certificate struct {
	p atomic.Pointer[tls.Certificate]
}

func (c *certificate) load(certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("TLS: %w", err)
	}
	c.p.Store(&cert)
	return nil
}

func (c *certificate) get(*tls.ClientHelloInfo) (*tls.Certificate, error) { return c.p.Load(), nil }

// run serves until ctx ends. ready, if not nil, gets the address listened
// on once the listener is open.
func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer, ready chan<- string) int {
	cfg, err := parseConfig(args, environ, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "straum-console:", err)
		return 2
	}
	if cfg.version {
		fmt.Fprintf(stdout, "straum-console %s (commit %s, %s)\n", version, commit, runtime.Version())
		return 0
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	var a *auth
	if cfg.passwordFile != "" {
		if a, err = newAuth(cfg.passwordFile); err != nil {
			fmt.Fprintln(stderr, "straum-console:", err)
			return 2
		}
	}
	var cert certificate
	if cfg.tlsCert != "" {
		if err := cert.load(cfg.tlsCert, cfg.tlsKey); err != nil {
			fmt.Fprintln(stderr, "straum-console:", err)
			return 2
		}
	}
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		fmt.Fprintln(stderr, "straum-console:", err)
		return 1
	}
	c := newCollector(cfg, log)
	srv := newServer(c.hub, a, cfg.publicHosts)
	served := make(chan error, 1)
	if cfg.tlsCert != "" {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: cert.get}
		go func() { served <- srv.ServeTLS(ln, "", "") }()
	} else {
		go func() { served <- srv.Serve(ln) }()
	}
	// SIGHUP re-reads the password file and the certificate; a file that
	// does not load leaves the old one in place.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	go func() {
		for range hup {
			if a != nil {
				if err := a.reload(); err != nil {
					log.Error("reload kept the old password file", "error", err)
				} else {
					log.Info("password file reloaded")
				}
			}
			if cfg.tlsCert != "" {
				if err := cert.load(cfg.tlsCert, cfg.tlsKey); err != nil {
					log.Error("reload kept the old certificate", "error", err)
				} else {
					log.Info("certificate reloaded")
				}
			}
		}
	}()
	log.Info("serving", "version", version, "listen", ln.Addr().String(), "tls", cfg.tlsCert != "", "sign_in", a != nil, "nodes", len(cfg.nodes), "interval", cfg.interval.String())
	if cfg.debugListen != "" {
		dln, err := net.Listen("tcp", cfg.debugListen)
		if err != nil {
			fmt.Fprintln(stderr, "straum-console:", err)
			return 1
		}
		debug := &http.Server{Handler: debugRoutes(), ReadHeaderTimeout: 5 * time.Second}
		go debug.Serve(dln)
		defer debug.Close()
		log.Info("debug listener", "listen", dln.Addr().String())
	}
	if ready != nil {
		ready <- ln.Addr().String()
	}

	polled := make(chan struct{})
	go func() { c.run(ctx); close(polled) }()
	select {
	case <-ctx.Done():
	case err := <-served:
		log.Error("serving stopped", "error", err)
		return 1
	}
	<-polled
	c.hub.close() // ends every stream, so Shutdown has nothing long to wait for
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		// A stream stuck in a write outlived the wait: cut it. Stopping on
		// a signal is still a clean exit.
		log.Warn("shutdown cut connections short", "error", err)
		srv.Close()
	}
	return 0
}

// straum-console is a read-only web console for a Straum cluster. It polls
// the nodes' metrics endpoints and serves the /api/v1 of docs/state-json.md
// on loopback.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stderr, nil))
}

// run serves until ctx ends. ready, if not nil, gets the address listened
// on once the listener is open.
func run(ctx context.Context, args, environ []string, stderr io.Writer, ready chan<- string) int {
	cfg, err := parseConfig(args, environ, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "straum-console:", err)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		fmt.Fprintln(stderr, "straum-console:", err)
		return 1
	}
	c := newCollector(cfg, log)
	srv := newServer(c.hub)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.Info("serving", "listen", ln.Addr().String(), "nodes", len(cfg.nodes), "interval", cfg.interval.String())
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

// straum-console is a read-only web console for a Straum cluster. This
// version polls the nodes' metrics endpoints and logs what changes; the
// HTTP API of docs/state-json.md comes next.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Environ(), os.Stderr))
}

func run(ctx context.Context, args, environ []string, stderr io.Writer) int {
	cfg, err := parseConfig(args, environ, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "straum-console:", err)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, nil))
	log.Info("polling", "nodes", len(cfg.nodes), "interval", cfg.interval.String())
	newCollector(cfg, log).run(ctx)
	return 0
}

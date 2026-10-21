package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/straum-io/straum-cli/metrics"
)

// config is what the console was told: flags first, then STRAUM_CONSOLE_*
// (CON-D-3 A). These variables never belong in a straum server's
// environment, which refuses any unknown STRAUM_* name.
type config struct {
	nodes    []node
	interval time.Duration
}

// node is one configured metrics endpoint and the id it is expected to
// report as straum_node.
type node struct {
	id   int
	addr string
}

const (
	envNodes    = "STRAUM_CONSOLE_NODES"
	envInterval = "STRAUM_CONSOLE_INTERVAL_MS"

	minInterval     = time.Second
	maxInterval     = time.Minute
	defaultInterval = 2 * time.Second
)

// nodeFlag collects every --node, repeated or comma-separated.
type nodeFlag []string

func (f *nodeFlag) String() string     { return strings.Join(*f, ",") }
func (f *nodeFlag) Set(v string) error { *f = append(*f, v); return nil }

// parseConfig reads args and environ. An unknown STRAUM_CONSOLE_* variable,
// a malformed value, or a node id or address given twice is an error:
// nothing is corrected silently.
func parseConfig(args, environ []string, stderr io.Writer) (config, error) {
	env := map[string]string{}
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "STRAUM_CONSOLE_") {
			continue
		}
		if k != envNodes && k != envInterval {
			return config{}, fmt.Errorf("unknown variable %s", k)
		}
		env[k] = v
	}

	fs := flag.NewFlagSet("straum-console", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var nodes nodeFlag
	fs.Var(&nodes, "node", "a node as id=host:port, its metrics address (repeatable or comma-separated; else "+envNodes+")")
	interval := fs.Duration("interval", defaultInterval, "time between polls, 1s to 1m (else "+envInterval+")")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var cfg config
	spec := strings.Join(nodes, ",")
	if !set["node"] {
		spec = env[envNodes]
	}
	if spec == "" {
		return config{}, errors.New("no nodes: give --node or " + envNodes)
	}
	var err error
	if cfg.nodes, err = parseNodes(spec); err != nil {
		return config{}, err
	}

	cfg.interval = *interval
	if v, ok := env[envInterval]; ok && !set["interval"] {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return config{}, fmt.Errorf("%s=%q: want milliseconds", envInterval, v)
		}
		// Checked before converting, so a huge value cannot wrap into range.
		if ms < minInterval.Milliseconds() || ms > maxInterval.Milliseconds() {
			return config{}, fmt.Errorf("%s=%d: want 1s to 1m", envInterval, ms)
		}
		cfg.interval = time.Duration(ms) * time.Millisecond
	}
	if cfg.interval < minInterval || cfg.interval > maxInterval {
		return config{}, fmt.Errorf("interval %s: want 1s to 1m", cfg.interval)
	}
	return cfg, nil
}

// parseNodes reads "id=host:port,…", the shape of STRAUM_CLUSTER_PEERS.
func parseNodes(spec string) ([]node, error) {
	var nodes []node
	ids, addrs := map[int]bool{}, map[string]bool{}
	for _, item := range strings.Split(spec, ",") {
		idText, addr, ok := strings.Cut(strings.TrimSpace(item), "=")
		id, err := strconv.Atoi(idText)
		if !ok || err != nil || id < 0 {
			return nil, fmt.Errorf("node %q: want id=host:port", item)
		}
		if err := metrics.CheckAddress(addr); err != nil {
			return nil, err
		}
		if ids[id] || addrs[addr] {
			return nil, fmt.Errorf("node %q: id or address given twice", item)
		}
		ids[id], addrs[addr] = true, true
		nodes = append(nodes, node{id, addr})
	}
	return nodes, nil
}

package main

import (
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	two := []node{{0, "127.0.0.1:9100"}, {1, "127.0.0.1:9101"}}
	for _, tc := range []struct {
		name     string
		args     []string
		env      []string
		nodes    []node
		interval time.Duration
		err      string
	}{
		{name: "flags", args: []string{"--node", "0=127.0.0.1:9100", "--node", "1=127.0.0.1:9101", "--interval", "5s"}, nodes: two, interval: 5 * time.Second},
		{name: "comma-separated flag", args: []string{"--node", "0=127.0.0.1:9100,1=127.0.0.1:9101"}, nodes: two, interval: 2 * time.Second},
		{name: "env", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100,1=127.0.0.1:9101", "STRAUM_CONSOLE_INTERVAL_MS=1500"}, nodes: two, interval: 1500 * time.Millisecond},
		{name: "flag beats env", args: []string{"--node", "0=127.0.0.1:9100,1=127.0.0.1:9101", "--interval", "3s"}, env: []string{"STRAUM_CONSOLE_NODES=7=10.0.0.1:1", "STRAUM_CONSOLE_INTERVAL_MS=9000"}, nodes: two, interval: 3 * time.Second},
		{name: "other variables ignored", env: []string{"STRAUM_CLI_NODES=x", "PATH=/bin", "STRAUM_CONSOLE_NODES=0=127.0.0.1:9100"}, nodes: two[:1], interval: 2 * time.Second},
		{name: "unknown variable", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_NODE=x"}, err: "unknown variable STRAUM_CONSOLE_NODE"},
		{name: "no nodes", err: "no nodes"},
		{name: "no id", args: []string{"--node", "127.0.0.1:9100"}, err: "want id=host:port"},
		{name: "negative id", args: []string{"--node", "-1=127.0.0.1:9100"}, err: "want id=host:port"},
		{name: "scheme", args: []string{"--node", "0=http://127.0.0.1:9100"}, err: "want host:port"},
		{name: "duplicate id", args: []string{"--node", "0=127.0.0.1:9100,0=127.0.0.1:9101"}, err: "given twice"},
		{name: "duplicate address", args: []string{"--node", "0=127.0.0.1:9100,1=127.0.0.1:9100"}, err: "given twice"},
		{name: "interval too short", args: []string{"--node", "0=127.0.0.1:9100", "--interval", "500ms"}, err: "want 1s to 1m"},
		{name: "interval too long", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_INTERVAL_MS=61000"}, err: "want 1s to 1m"},
		{name: "interval overflows", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_INTERVAL_MS=18446744073710"}, err: "want 1s to 1m"},
		{name: "interval not a number", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_INTERVAL_MS=2s"}, err: "want milliseconds"},
		{name: "stray argument", args: []string{"--node", "0=127.0.0.1:9100", "extra"}, err: "unexpected argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := parseConfig(tc.args, tc.env, io.Discard)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cfg.nodes, tc.nodes) || cfg.interval != tc.interval {
				t.Fatalf("got %+v", cfg)
			}
		})
	}
}

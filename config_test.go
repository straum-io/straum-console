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
		listen   string
		err      string
	}{
		{name: "flags", args: []string{"--node", "0=127.0.0.1:9100", "--node", "1=127.0.0.1:9101", "--interval", "5s"}, nodes: two, interval: 5 * time.Second, listen: "127.0.0.1:8080"},
		{name: "comma-separated flag", args: []string{"--node", "0=127.0.0.1:9100,1=127.0.0.1:9101", "--listen", "[::1]:9000"}, nodes: two, interval: 2 * time.Second, listen: "[::1]:9000"},
		{name: "env", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100,1=127.0.0.1:9101", "STRAUM_CONSOLE_INTERVAL_MS=1500", "STRAUM_CONSOLE_LISTEN=127.0.0.2:8081"}, nodes: two, interval: 1500 * time.Millisecond, listen: "127.0.0.2:8081"},
		{name: "flag beats env", args: []string{"--node", "0=127.0.0.1:9100,1=127.0.0.1:9101", "--interval", "3s"}, env: []string{"STRAUM_CONSOLE_NODES=7=10.0.0.1:1", "STRAUM_CONSOLE_INTERVAL_MS=9000"}, nodes: two, interval: 3 * time.Second, listen: "127.0.0.1:8080"},
		{name: "other variables ignored", env: []string{"STRAUM_CLI_NODES=x", "PATH=/bin", "STRAUM_CONSOLE_NODES=0=127.0.0.1:9100"}, nodes: two[:1], interval: 2 * time.Second, listen: "127.0.0.1:8080"},
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
		{name: "listen beyond loopback", args: []string{"--node", "0=127.0.0.1:9100", "--listen", "0.0.0.0:8080"}, err: "loopback"},
		{name: "listen on every interface", args: []string{"--node", "0=127.0.0.1:9100", "--listen", ":8080"}, err: "loopback"},
		{name: "listen by host name", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_LISTEN=localhost:8080"}, err: "loopback"},
		{name: "listen on a LAN address", env: []string{"STRAUM_CONSOLE_NODES=0=127.0.0.1:9100", "STRAUM_CONSOLE_LISTEN=10.0.0.5:8080"}, err: "loopback"},
		{name: "debug listener beyond loopback", args: []string{"--node", "0=127.0.0.1:9100", "--debug-listen", "0.0.0.0:6060"}, err: "debug listen"},
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
			if !slices.Equal(cfg.nodes, tc.nodes) || cfg.interval != tc.interval || cfg.listen != tc.listen {
				t.Fatalf("got %+v", cfg)
			}
		})
	}
}

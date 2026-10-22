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
	listen   string
	// debugListen serves net/http/pprof when set: loopback only, off by default.
	debugListen string
	// Beyond loopback (CON-D-8): TLS, a password file and the host names
	// the Host check accepts. TLS alone is allowed on loopback; the password
	// file needs TLS, so the session cookie is always Secure.
	tlsCert, tlsKey string
	passwordFile    string
	publicHosts     []string
	version         bool
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
	envListen   = "STRAUM_CONSOLE_LISTEN"
	envDebug    = "STRAUM_CONSOLE_DEBUG_LISTEN"
	envTLSCert  = "STRAUM_CONSOLE_TLS_CERT"
	envTLSKey   = "STRAUM_CONSOLE_TLS_KEY"
	envPassword = "STRAUM_CONSOLE_PASSWORD_FILE"
	envPublic   = "STRAUM_CONSOLE_PUBLIC_HOSTS"

	defaultListen = "127.0.0.1:8080"
	maxNodes      = 15

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
		switch k {
		case envNodes, envInterval, envListen, envDebug, envTLSCert, envTLSKey, envPassword, envPublic:
		default:
			return config{}, fmt.Errorf("unknown variable %s", k)
		}
		env[k] = v
	}

	fs := flag.NewFlagSet("straum-console", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var nodes nodeFlag
	fs.Var(&nodes, "node", "a node as id=host:port, its metrics address (repeatable or comma-separated; else "+envNodes+")")
	interval := fs.Duration("interval", defaultInterval, "time between polls, 1s to 1m (else "+envInterval+")")
	listen := fs.String("listen", defaultListen, "where to serve /api/v1: a loopback address, or beyond loopback with TLS, a password file and public hosts (else "+envListen+")")
	debugListen := fs.String("debug-listen", "", "where to serve net/http/pprof, a loopback address only; off when empty (else "+envDebug+")")
	tlsCert := fs.String("tls-cert", "", "a PEM certificate chain: serve HTTPS (with --tls-key; else "+envTLSCert+")")
	tlsKey := fs.String("tls-key", "", "the PEM private key of --tls-cert (else "+envTLSKey+")")
	passwordFile := fs.String("password-file", "", "user:hash lines as straum-server hash-password prints them: sign-in required; needs TLS (else "+envPassword+")")
	publicHosts := fs.String("public-host", "", "host names, comma-separated, that browsers use to reach the console beyond loopback; needs TLS and a password file (else "+envPublic+")")
	version := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *version {
		return config{version: true}, nil
	}

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

	pick := func(flagValue, name, envName string) string {
		if v, ok := env[envName]; ok && !set[name] {
			return v
		}
		return flagValue
	}
	cfg.listen = pick(*listen, "listen", envListen)
	cfg.debugListen = pick(*debugListen, "debug-listen", envDebug)
	cfg.tlsCert = pick(*tlsCert, "tls-cert", envTLSCert)
	cfg.tlsKey = pick(*tlsKey, "tls-key", envTLSKey)
	cfg.passwordFile = pick(*passwordFile, "password-file", envPassword)
	for _, h := range strings.Split(pick(*publicHosts, "public-host", envPublic), ",") {
		if h = strings.TrimSpace(h); h != "" {
			if strings.ContainsAny(h, ":/[] ") {
				return config{}, fmt.Errorf("public host %q: want a host name, without port or scheme", h)
			}
			cfg.publicHosts = append(cfg.publicHosts, h)
		}
	}
	tls := cfg.tlsCert != "" || cfg.tlsKey != ""
	switch {
	case (cfg.tlsCert == "") != (cfg.tlsKey == ""):
		return config{}, errors.New("--tls-cert and --tls-key go together")
	case cfg.passwordFile != "" && !tls:
		return config{}, errors.New("--password-file needs --tls-cert and --tls-key: a password and its session must not cross the network in clear")
	case len(cfg.publicHosts) > 0 && cfg.passwordFile == "":
		return config{}, errors.New("--public-host needs --password-file and TLS: beyond loopback the console requires sign-in")
	}
	if err := checkListen(cfg.listen, len(cfg.publicHosts) > 0); err != nil {
		return config{}, err
	}
	if cfg.debugListen != "" {
		if err := checkListen(cfg.debugListen, false); err != nil {
			return config{}, fmt.Errorf("debug %w", err)
		}
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
	// The history's memory is sized for this many (CON-D-7 A, plan §14).
	if len(nodes) > maxNodes {
		return nil, fmt.Errorf("%d nodes: at most %d", len(nodes), maxNodes)
	}
	return nodes, nil
}

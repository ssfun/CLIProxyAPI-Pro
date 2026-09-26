package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultListenAddress = "127.0.0.1:8318"

type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	if d == nil {
		return fmt.Errorf("duration target is nil")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value, err)
	}
	d.Duration = parsed
	return nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || strings.TrimSpace(node.Value) == "" {
		return nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(node.Value))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

type HealthCheckConfig struct {
	Enabled            bool     `yaml:"enabled" json:"enabled"`
	Interval           Duration `yaml:"interval" json:"interval"`
	Timeout            Duration `yaml:"timeout" json:"timeout"`
	IsolationThreshold int      `yaml:"isolation-threshold" json:"isolation-threshold"`
	IsolationDuration  Duration `yaml:"isolation-duration" json:"isolation-duration"`
	ProbeAddress       string   `yaml:"probe-address" json:"probe-address"`
	TestURL            string   `yaml:"test-url" json:"test-url"`
}

type NodeConfig struct {
	ID      string `yaml:"id" json:"id"`
	Label   string `yaml:"label" json:"label"`
	URL     string `yaml:"url" json:"url"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Weight  int64  `yaml:"weight" json:"weight"`
	Order   int    `yaml:"order" json:"order"`
}

type Config struct {
	Enabled             bool              `yaml:"enabled" json:"enabled"`
	TakeoverEnabled     bool              `yaml:"takeover-enabled" json:"takeover-enabled"`
	Listen              string            `yaml:"listen" json:"listen"`
	Strategy            string            `yaml:"strategy" json:"strategy"`
	DialTimeout         Duration          `yaml:"dial-timeout" json:"dial-timeout"`
	MaxFailoverAttempts int               `yaml:"max-failover-attempts" json:"max-failover-attempts"`
	FailOpen            bool              `yaml:"fail-open" json:"fail-open"`
	HealthCheck         HealthCheckConfig `yaml:"health-check" json:"health-check"`
	Nodes               []NodeConfig      `yaml:"nodes" json:"nodes"`
}

func Default() Config {
	return Config{
		Listen:              DefaultListenAddress,
		Strategy:            "round-robin",
		DialTimeout:         Duration{Duration: 8 * time.Second},
		MaxFailoverAttempts: 3,
		HealthCheck: HealthCheckConfig{
			Enabled:            true,
			Interval:           Duration{Duration: 30 * time.Second},
			Timeout:            Duration{Duration: 8 * time.Second},
			IsolationThreshold: 3,
			IsolationDuration:  Duration{Duration: 5 * time.Minute},
			ProbeAddress:       "www.gstatic.com:443",
			TestURL:            "https://ipwho.is/",
		},
	}
}

func Parse(data []byte) (Config, error) {
	cfg := Default()
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("decode proxy pool config: %w", err)
		}
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Marshal(cfg Config) ([]byte, error) {
	if err := cfg.NormalizeAndValidate(); err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}

func (cfg *Config) NormalizeAndValidate() error {
	if cfg == nil {
		return fmt.Errorf("proxy pool config is nil")
	}
	cfg.Listen = strings.TrimSpace(cfg.Listen)
	if cfg.Listen == "" {
		cfg.Listen = DefaultListenAddress
	}
	host, port, errSplit := net.SplitHostPort(cfg.Listen)
	if errSplit != nil {
		return fmt.Errorf("invalid listen address %q: %w", cfg.Listen, errSplit)
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a numeric loopback IP")
	}
	portNumber, errPort := strconv.Atoi(port)
	if errPort != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("listen address has invalid port %q", port)
	}

	cfg.Strategy = strings.ToLower(strings.TrimSpace(cfg.Strategy))
	switch cfg.Strategy {
	case "round-robin", "weighted", "least-connections":
	default:
		return fmt.Errorf("unsupported strategy %q", cfg.Strategy)
	}
	if cfg.DialTimeout.Duration <= 0 {
		cfg.DialTimeout.Duration = 8 * time.Second
	}
	if cfg.MaxFailoverAttempts <= 0 {
		cfg.MaxFailoverAttempts = 3
	}
	if cfg.HealthCheck.Interval.Duration <= 0 {
		cfg.HealthCheck.Interval.Duration = 30 * time.Second
	}
	if cfg.HealthCheck.Timeout.Duration <= 0 {
		cfg.HealthCheck.Timeout.Duration = 8 * time.Second
	}
	if cfg.HealthCheck.IsolationThreshold <= 0 {
		cfg.HealthCheck.IsolationThreshold = 3
	}
	if cfg.HealthCheck.IsolationDuration.Duration <= 0 {
		cfg.HealthCheck.IsolationDuration.Duration = 5 * time.Minute
	}
	cfg.HealthCheck.ProbeAddress = strings.TrimSpace(cfg.HealthCheck.ProbeAddress)
	if cfg.HealthCheck.ProbeAddress == "" {
		cfg.HealthCheck.ProbeAddress = "www.gstatic.com:443"
	}
	if _, _, errProbe := net.SplitHostPort(cfg.HealthCheck.ProbeAddress); errProbe != nil {
		return fmt.Errorf("invalid health-check probe-address %q: %w", cfg.HealthCheck.ProbeAddress, errProbe)
	}
	cfg.HealthCheck.TestURL = strings.TrimSpace(cfg.HealthCheck.TestURL)
	if cfg.HealthCheck.TestURL == "" {
		cfg.HealthCheck.TestURL = "https://ipwho.is/"
	}
	parsedTestURL, errTestURL := url.Parse(cfg.HealthCheck.TestURL)
	if errTestURL != nil || parsedTestURL.Host == "" || (parsedTestURL.Scheme != "http" && parsedTestURL.Scheme != "https") {
		return fmt.Errorf("invalid health-check test-url %q", cfg.HealthCheck.TestURL)
	}

	listenHost, listenPort, _ := net.SplitHostPort(canonicalHostPort(cfg.Listen))
	listenHostPort := canonicalHostPort(cfg.Listen)
	seenIDs := make(map[string]struct{}, len(cfg.Nodes))
	seenURLs := make(map[string]struct{}, len(cfg.Nodes))
	for index := range cfg.Nodes {
		node := &cfg.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		node.Label = strings.TrimSpace(node.Label)
		node.URL = strings.TrimSpace(node.URL)
		if node.ID == "" {
			return fmt.Errorf("nodes[%d].id is required", index)
		}
		if _, exists := seenIDs[node.ID]; exists {
			return fmt.Errorf("duplicate proxy node id %q", node.ID)
		}
		seenIDs[node.ID] = struct{}{}
		parsed, errURL := url.Parse(node.URL)
		if errURL != nil || parsed.Host == "" {
			return fmt.Errorf("nodes[%d].url is invalid", index)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("nodes[%d].url uses unsupported scheme %q", index, parsed.Scheme)
		}
		if canonicalURL(node.URL) == "" {
			return fmt.Errorf("nodes[%d].url is invalid", index)
		}
		if _, exists := seenURLs[canonicalURL(node.URL)]; exists {
			return fmt.Errorf("duplicate proxy node url at nodes[%d]", index)
		}
		seenURLs[canonicalURL(node.URL)] = struct{}{}
		if canonicalHostPort(parsed.Host) == listenHostPort || proxyURLTargetsObviousLoopback(parsed, listenHost, listenPort) {
			return fmt.Errorf("nodes[%d].url points to the local proxy pool listener", index)
		}
		if node.Weight <= 0 {
			node.Weight = 1
		}
	}
	sort.SliceStable(cfg.Nodes, func(i, j int) bool {
		if cfg.Nodes[i].Order == cfg.Nodes[j].Order {
			return cfg.Nodes[i].ID < cfg.Nodes[j].ID
		}
		return cfg.Nodes[i].Order < cfg.Nodes[j].Order
	})
	return nil
}

func proxyURLTargetsObviousLoopback(parsed *url.URL, listenHost, listenPort string) bool {
	if parsed == nil || effectiveProxyPort(parsed) != listenPort {
		return false
	}
	listenerHost := strings.TrimSpace(listenHost)
	listenerIP := net.ParseIP(listenerHost)
	if !strings.EqualFold(listenerHost, "localhost") &&
		(listenerIP == nil || (!listenerIP.IsLoopback() && !listenerIP.IsUnspecified())) {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	return strings.EqualFold(host, "localhost") || isLoopbackIP(host)
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

// ResolvesToLocalListener performs the authoritative runtime recursion
// check. DNS aliases are resolved immediately before dialing so a hostname or
// a later DNS change cannot turn a pool node into the pool's own listener.
func ResolvesToLocalListener(ctx context.Context, rawProxyURL, listen string) (bool, error) {
	parsed, errParse := url.Parse(strings.TrimSpace(rawProxyURL))
	if errParse != nil || parsed.Host == "" {
		return false, errParse
	}
	listenHost, listenPort, errListen := net.SplitHostPort(canonicalHostPort(listen))
	if errListen != nil || effectiveProxyPort(parsed) != listenPort {
		return false, errListen
	}
	if canonicalHostPort(parsed.Host) == canonicalHostPort(listen) ||
		proxyURLTargetsObviousLoopback(parsed, listenHost, listenPort) {
		return true, nil
	}
	addresses, errLookup := net.DefaultResolver.LookupIPAddr(ctx, strings.TrimSpace(parsed.Hostname()))
	if errLookup != nil {
		return false, errLookup
	}
	for _, address := range addresses {
		if address.IP != nil && address.IP.IsLoopback() {
			return true, nil
		}
	}
	return false, nil
}

func canonicalURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = canonicalHostPort(u.Host)
	return u.String()
}

func canonicalHostPort(raw string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	if number, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(number)
	}
	return net.JoinHostPort(strings.ToLower(strings.TrimSpace(host)), port)
}

// Match the actual dialers' default ports when checking for recursive hops.
func effectiveProxyPort(parsed *url.URL) string {
	port := parsed.Port()
	if port == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		case "socks5", "socks5h":
			port = "1080"
		}
	}
	if number, err := strconv.Atoi(port); err == nil {
		return strconv.Itoa(number)
	}
	return port
}

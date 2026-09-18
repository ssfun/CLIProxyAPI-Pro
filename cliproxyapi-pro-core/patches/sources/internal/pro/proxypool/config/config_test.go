package config

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseAppliesDefaultsAndSortsNodes(t *testing.T) {
	cfg, err := Parse([]byte(`
enabled: true
nodes:
  - id: second
    url: http://127.0.0.1:19002
    enabled: true
    order: 20
  - id: first
    url: socks5://127.0.0.1:19001
    enabled: true
    order: 10
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Listen != DefaultListenAddress || cfg.Strategy != "round-robin" {
		t.Fatalf("defaults = listen:%q strategy:%q", cfg.Listen, cfg.Strategy)
	}
	if cfg.DialTimeout.Duration != 8*time.Second {
		t.Fatalf("dial timeout = %v", cfg.DialTimeout.Duration)
	}
	if len(cfg.Nodes) != 2 || cfg.Nodes[0].ID != "first" || cfg.Nodes[1].ID != "second" {
		t.Fatalf("sorted nodes = %+v", cfg.Nodes)
	}
}

func TestParseRejectsNonLoopbackListener(t *testing.T) {
	_, err := Parse([]byte("listen: 0.0.0.0:8318\n"))
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("Parse() error = %v, want loopback rejection", err)
	}
}

func TestParseRejectsProxyLoop(t *testing.T) {
	_, err := Parse([]byte(`
listen: 127.0.0.1:8318
nodes:
  - id: loop
    url: socks5://127.0.0.1:8318
    enabled: true
`))
	if err == nil || !strings.Contains(err.Error(), "local proxy pool listener") {
		t.Fatalf("Parse() error = %v, want proxy loop rejection", err)
	}
}

func TestParseRejectsProxyLoopAliases(t *testing.T) {
	for _, proxyURL := range []string{
		"socks5://localhost:8318",
		"socks5://127.0.0.2:8318",
		"socks5://[::1]:8318",
	} {
		_, err := Parse([]byte("listen: 127.0.0.1:8318\nnodes:\n  - id: loop\n    url: " + proxyURL + "\n    enabled: true\n"))
		if err == nil || !strings.Contains(err.Error(), "local proxy pool listener") {
			t.Fatalf("Parse(%q) error = %v", proxyURL, err)
		}
	}
}

func TestResolvesLocalhostToLoopbackListener(t *testing.T) {
	recursive, err := ResolvesToLocalListener(context.Background(), "socks5://localhost:8318", "127.0.0.1:8318")
	if err != nil {
		t.Fatal(err)
	}
	if !recursive {
		t.Fatal("localhost proxy was not recognized as recursive")
	}
}

func TestParseRejectsDuplicateNodeID(t *testing.T) {
	_, err := Parse([]byte(`
nodes:
  - id: duplicate
    url: http://127.0.0.1:19001
  - id: duplicate
    url: http://127.0.0.1:19002
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate proxy node id") {
		t.Fatalf("Parse() error = %v, want duplicate id rejection", err)
	}
}

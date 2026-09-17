package observability

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigForPathResolvesSharedDatabasePrecedence(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", "")
	t.Setenv("USAGE_DATA_DIR", "")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if got, want := LoadConfigForPath(configPath).DBPath, filepath.Join(filepath.Dir(configPath), "usage", "usage.sqlite"); got != want {
		t.Fatalf("config-relative DB path = %q, want %q", got, want)
	}
	if got := LoadConfigForPath("").DBPath; got != "/CLIProxyAPI/usage/usage.sqlite" {
		t.Fatalf("legacy default DB path = %q", got)
	}

	t.Setenv("USAGE_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	if got, want := LoadConfigForPath(configPath).DBPath, filepath.Join(os.Getenv("USAGE_DATA_DIR"), "usage.sqlite"); got != want {
		t.Fatalf("data-dir DB path = %q, want %q", got, want)
	}
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "explicit.sqlite"))
	if got, want := LoadConfigForPath(configPath).DBPath, os.Getenv("USAGE_DB_PATH"); got != want {
		t.Fatalf("explicit DB path = %q, want %q", got, want)
	}
}

func TestResolveDataDirForPathPrecedence(t *testing.T) {
	t.Setenv("USAGE_DATA_DIR", "")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if got, want := ResolveDataDirForPath(configPath), filepath.Join(filepath.Dir(configPath), "usage"); got != want {
		t.Fatalf("config-relative data dir = %q, want %q", got, want)
	}
	if got := ResolveDataDirForPath(""); got != "/CLIProxyAPI/usage" {
		t.Fatalf("legacy data dir = %q", got)
	}

	explicit := filepath.Join(t.TempDir(), "data")
	t.Setenv("USAGE_DATA_DIR", explicit)
	if got := ResolveDataDirForPath(configPath); got != explicit {
		t.Fatalf("explicit data dir = %q, want %q", got, explicit)
	}
}

func TestLoadConfigForPathLoadsPersistenceQueueBounds(t *testing.T) {
	t.Setenv("USAGE_QUEUE_RETENTION_SECONDS", "7200")
	t.Setenv("USAGE_QUEUE_MAX_ITEMS", "12345")
	t.Setenv("USAGE_QUEUE_MAX_BYTES", "987654")
	t.Setenv("USAGE_QUERY_LIMIT", "75000")

	cfg := LoadConfigForPath(filepath.Join(t.TempDir(), "config.yaml"))
	if cfg.PersistenceQueueRetentionSeconds != 7200 ||
		cfg.PersistenceQueueMaxItems != 12345 ||
		cfg.PersistenceQueueMaxBytes != 987654 {
		t.Fatalf("persistence queue config = %+v", cfg)
	}
	if cfg.QueryLimit != 75000 {
		t.Fatalf("query limit = %d, want configurable maximum 75000", cfg.QueryLimit)
	}
}

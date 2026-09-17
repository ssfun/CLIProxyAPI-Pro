package observability

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled                          bool
	DBPath                           string
	BatchSize                        int
	PollInterval                     time.Duration
	QueryLimit                       int
	PersistenceQueueRetentionSeconds int
	PersistenceQueueMaxItems         int
	PersistenceQueueMaxBytes         int64
}

const usageEventsPageLimit = 5000
const usageEventsSentinelLimit = usageEventsPageLimit + 1

func LoadConfig() Config {
	return LoadConfigForPath("")
}

// ResolveDataDirForPath keeps the historical Docker default while giving SDK
// and native deployments a writable default beside config.yaml. Explicit
// USAGE_DATA_DIR always remains authoritative.
func ResolveDataDirForPath(configFilePath string) string {
	if dataDir := strings.TrimSpace(os.Getenv("USAGE_DATA_DIR")); dataDir != "" {
		return dataDir
	}
	configFilePath = strings.TrimSpace(configFilePath)
	if configFilePath != "" {
		return filepath.Join(filepath.Dir(configFilePath), "usage")
	}
	return "/CLIProxyAPI/usage"
}

// LoadConfigForPath keeps the historical Docker default while giving SDK and
// native deployments a writable default beside config.yaml. Explicit
// USAGE_DB_PATH and USAGE_DATA_DIR always remain authoritative.
func LoadConfigForPath(configFilePath string) Config {
	dataDir := ResolveDataDirForPath(configFilePath)
	return Config{
		Enabled:                          envBool("USAGE_SERVICE_ENABLED", true),
		DBPath:                           env("USAGE_DB_PATH", filepath.Join(dataDir, "usage.sqlite")),
		BatchSize:                        envInt("USAGE_BATCH_SIZE", 100),
		PollInterval:                     time.Duration(envInt("USAGE_POLL_INTERVAL_MS", 500)) * time.Millisecond,
		QueryLimit:                       envInt("USAGE_QUERY_LIMIT", 50000),
		PersistenceQueueRetentionSeconds: envInt("USAGE_QUEUE_RETENTION_SECONDS", 3600),
		PersistenceQueueMaxItems:         envInt("USAGE_QUEUE_MAX_ITEMS", 100000),
		PersistenceQueueMaxBytes:         int64(envInt("USAGE_QUEUE_MAX_BYTES", 256*1024*1024)),
	}
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

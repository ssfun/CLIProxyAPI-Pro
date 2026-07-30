package embeddedusage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Enabled      bool
	DBPath       string
	BatchSize    int
	PollInterval time.Duration
	QueryLimit   int
}

const usageEventsPageLimit = 5000
const usageEventsSentinelLimit = usageEventsPageLimit + 1

func LoadConfig() Config {
	dataDir := env("USAGE_DATA_DIR", "/CLIProxyAPI/usage")
	return Config{
		Enabled:      envBool("USAGE_SERVICE_ENABLED", true),
		DBPath:       env("USAGE_DB_PATH", filepath.Join(dataDir, "usage.sqlite")),
		BatchSize:    envInt("USAGE_BATCH_SIZE", 100),
		PollInterval: time.Duration(envInt("USAGE_POLL_INTERVAL_MS", 500)) * time.Millisecond,
		QueryLimit:   envInt("USAGE_QUERY_LIMIT", 50000),
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

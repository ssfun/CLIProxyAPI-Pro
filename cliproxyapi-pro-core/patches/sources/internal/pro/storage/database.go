// Package storage owns the shared Pro SQLite connection and lifecycle leases.
package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"modernc.org/sqlite"
)

const timezoneBucketFunction = "pro_tz_bucket_start_ms"

var timezoneLocations sync.Map

func init() {
	sqlite.MustRegisterDeterministicScalarFunction(
		timezoneBucketFunction,
		3,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if len(args) != 3 {
				return nil, errors.New("timezone bucket requires timestamp, interval, and timezone")
			}
			timestampMS, ok := args[0].(int64)
			if !ok {
				return nil, errors.New("timezone bucket timestamp is invalid")
			}
			intervalMS, ok := args[1].(int64)
			if !ok || intervalMS <= 0 {
				return nil, errors.New("timezone bucket interval is invalid")
			}
			timezone, ok := args[2].(string)
			if !ok || strings.TrimSpace(timezone) == "" {
				return nil, errors.New("timezone bucket timezone is invalid")
			}
			locationValue, ok := timezoneLocations.Load(timezone)
			if !ok {
				location, err := time.LoadLocation(timezone)
				if err != nil {
					return nil, err
				}
				locationValue, _ = timezoneLocations.LoadOrStore(timezone, location)
			}
			location := locationValue.(*time.Location)
			localTime := time.UnixMilli(timestampMS).In(location)
			if intervalMS == int64(24*time.Hour/time.Millisecond) {
				return time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, location).UnixMilli(), nil
			}
			_, offsetSeconds := localTime.Zone()
			offsetMS := int64(offsetSeconds) * int64(time.Second/time.Millisecond)
			return ((timestampMS + offsetMS) / intervalMS * intervalMS) - offsetMS, nil
		},
	)
}

type Database struct {
	mu     sync.RWMutex
	shared *sharedDatabase
	closed bool
}

type sharedDatabase struct {
	db   *sql.DB
	path string
	refs int
}

var sharedDatabases = struct {
	sync.Mutex
	items map[string]*sharedDatabase
}{items: make(map[string]*sharedDatabase)}

func OpenSQLite(path string) (*Database, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = filepath.Clean(absolutePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sharedDatabases.Lock()
	if existing := sharedDatabases.items[path]; existing != nil {
		existing.refs++
		sharedDatabases.Unlock()
		return &Database{shared: existing}, nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		sharedDatabases.Unlock()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`pragma foreign_keys = ON`,
		`pragma busy_timeout = 5000`,
	} {
		if _, err = db.Exec(statement); err != nil {
			_ = db.Close()
			sharedDatabases.Unlock()
			return nil, err
		}
	}
	var foreignKeys int
	if err = db.QueryRow(`pragma foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		_ = db.Close()
		sharedDatabases.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("sqlite foreign key enforcement is unavailable")
	}
	shared := &sharedDatabase{db: db, path: path, refs: 1}
	sharedDatabases.items[path] = shared
	sharedDatabases.Unlock()
	return &Database{shared: shared}, nil
}

func (d *Database) SQL() *sql.DB {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed || d.shared == nil {
		return nil
	}
	return d.shared.db
}

func (d *Database) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	if d.closed || d.shared == nil {
		d.mu.Unlock()
		return nil
	}
	shared := d.shared
	d.closed = true
	d.shared = nil
	d.mu.Unlock()
	sharedDatabases.Lock()
	shared.refs--
	if shared.refs > 0 {
		sharedDatabases.Unlock()
		return nil
	}
	delete(sharedDatabases.items, shared.path)
	sharedDatabases.Unlock()
	return shared.db.Close()
}

// SameConnection reports whether two lifecycle leases share the same SQLite
// connection. It is intentionally diagnostic-only; callers must not infer
// domain ownership from it.
func (d *Database) SameConnection(other *Database) bool {
	if d == nil || other == nil {
		return false
	}
	d.mu.RLock()
	left := d.shared
	leftClosed := d.closed
	d.mu.RUnlock()
	other.mu.RLock()
	right := other.shared
	rightClosed := other.closed
	other.mu.RUnlock()
	return !leftClosed && !rightClosed && left != nil && left == right
}

// RetryBusy centralizes the retry contract used by state and quota writes.
func RetryBusy(ctx context.Context, operation func() error) error {
	if operation == nil {
		return nil
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		if err = operation(); err == nil || !IsBusy(err) {
			return err
		}
		delay := time.Duration(attempt+1) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return err
}

func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "sqlite_locked")
}

package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestEmbeddedIANATimezoneDataIsAvailable(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	_, winterOffset := time.Date(2026, 1, 1, 12, 0, 0, 0, location).Zone()
	_, summerOffset := time.Date(2026, 7, 1, 12, 0, 0, 0, location).Zone()
	if winterOffset == summerOffset {
		t.Fatalf("timezone data has no DST transition: winter=%d summer=%d", winterOffset, summerOffset)
	}
}

func TestDatabaseOwnsLifecycle(t *testing.T) {
	database, err := OpenSQLite(filepath.Join(t.TempDir(), "nested", "pro.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if database.SQL() != nil {
		t.Fatal("SQL() remained available after Close")
	}
}

func TestApplySchemaIsIdempotentForAdditiveMigrations(t *testing.T) {
	database, err := OpenSQLite(filepath.Join(t.TempDir(), "pro.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer database.Close()
	schema := Schema{
		Create: []string{`create table if not exists sample (id integer primary key)`},
		Alter:  []string{`alter table sample add column value text`},
		Seed:   []string{`insert or ignore into sample(id, value) values (1, 'ok')`},
	}
	if err := ApplySchema(context.Background(), database.SQL(), schema); err != nil {
		t.Fatalf("first ApplySchema() error = %v", err)
	}
	if err := ApplySchema(context.Background(), database.SQL(), schema); err != nil {
		t.Fatalf("second ApplySchema() error = %v", err)
	}
}

func TestOpenSQLiteSharesConnectionAcrossLifecycleLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pro.db")
	first, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.SameConnection(second) {
		t.Fatal("same path did not reuse the shared SQLite connection")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if second.SQL() == nil {
		t.Fatal("closing one lease closed the connection owned by another module")
	}
	if _, err := second.SQL().Exec(`create table shared_lifecycle (id integer primary key)`); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSQLiteEnablesForeignKeys(t *testing.T) {
	database, err := OpenSQLite(filepath.Join(t.TempDir(), "pro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var enabled int
	if err := database.SQL().QueryRow(`pragma foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", enabled)
	}
}

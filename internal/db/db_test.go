package db

import (
	"testing"
)

func openMemory(t *testing.T) *DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(':memory:'): %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func TestOpen_Ping(t *testing.T) {
	db := openMemory(t)
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestOpen_WALMode(t *testing.T) {
	// WAL mode is not supported for :memory: databases; SQLite silently
	// keeps them in "memory" mode. Use a temp file to verify the PRAGMA.
	path := t.TempDir() + "/wal_test.db"
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}
}

func TestOpen_ForeignKeys(t *testing.T) {
	db := openMemory(t)

	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if enabled != 1 {
		t.Errorf("foreign_keys = %d, want 1", enabled)
	}
}

func TestClose(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// testMigrations uses high version numbers to avoid conflicting with
// the real Migrations (versions 1-3) that Open() runs automatically.
var testMigrations = []Migration{
	{
		Version:     101,
		Description: "create test_items table",
		SQL: `CREATE TABLE test_items (
			id   INTEGER PRIMARY KEY,
			name TEXT NOT NULL
		)`,
	},
	{
		Version:     102,
		Description: "create test_tags table",
		SQL: `CREATE TABLE test_tags (
			id      INTEGER PRIMARY KEY,
			item_id INTEGER NOT NULL REFERENCES test_items(id),
			tag     TEXT    NOT NULL
		)`,
	},
}

func TestRunMigrations_TablesExist(t *testing.T) {
	db := openMemory(t)

	if err := RunMigrations(db, testMigrations); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	for _, tableName := range []string{"schema_migrations", "test_items", "test_tags"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tableName,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", tableName, err)
		}
	}
}

func TestRunMigrations_Idempotent(t *testing.T) {
	db := openMemory(t)

	if err := RunMigrations(db, testMigrations); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// Running again must not error
	if err := RunMigrations(db, testMigrations); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	// Open() auto-applies Migrations (versions 1-4), plus testMigrations (101, 102) = 6 rows total
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 6 {
		t.Errorf("schema_migrations rows = %d, want 6", count)
	}
}

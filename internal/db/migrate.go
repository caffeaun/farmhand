package db

import (
	"fmt"
)

// Migration represents a single database schema migration.
type Migration struct {
	Version     int
	Description string
	SQL         string
}

// RunMigrations applies any unapplied migrations in order.
// Each migration runs inside its own transaction.
// The schema_migrations table is created if it does not exist.
func RunMigrations(db *DB, migrations []Migration) error {
	// Ensure the tracking table exists before we begin
	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version     INTEGER PRIMARY KEY,
		description TEXT    NOT NULL,
		applied_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`
	if _, err := db.Exec(createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		applied, err := isMigrationApplied(db, m.Version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		if err := applyMigration(db, m); err != nil {
			return err
		}
	}

	return nil
}

// isMigrationApplied reports whether the given version has already been applied.
func isMigrationApplied(db *DB, version int) (bool, error) {
	var count int
	err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	return count > 0, nil
}

// applyMigration runs a single migration inside a transaction and records it.
func applyMigration(db *DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction for migration %d: %w", m.Version, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback on failure is intentional

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Description, err)
	}

	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, description) VALUES (?, ?)",
		m.Version, m.Description,
	); err != nil {
		return fmt.Errorf("record migration %d: %w", m.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.Version, err)
	}

	return nil
}

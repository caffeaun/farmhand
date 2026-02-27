// Package db provides database connection and migration utilities.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // register sqlite driver
)

// DB wraps *sql.DB for FarmHand database operations.
type DB struct {
	*sql.DB
	path string
}

// Open opens (or creates) a SQLite database at the given path.
// It enables WAL mode and foreign key support.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	// Enable foreign key constraints
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Verify connectivity
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{DB: sqlDB, path: path}

	if err := RunMigrations(db, Migrations); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.DB.Close()
}

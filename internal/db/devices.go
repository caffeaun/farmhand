package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Device represents a device record in the database.
type Device struct {
	ID           string    `json:"id"`
	Platform     string    `json:"platform"`
	Model        string    `json:"model"`
	OSVersion    string    `json:"os_version"`
	Status       string    `json:"status"`
	BatteryLevel int       `json:"battery_level"`
	Tags         []string  `json:"tags"`
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
}

// DeviceFilter defines criteria for filtering devices.
type DeviceFilter struct {
	Platform string
	Status   string
	// Tags uses AND semantics: device must have ALL specified tags.
	Tags []string
}

// DeviceRepository handles device CRUD operations.
type DeviceRepository struct {
	db *DB
}

// NewDeviceRepository creates a new device repository.
func NewDeviceRepository(db *DB) *DeviceRepository {
	return &DeviceRepository{db: db}
}

// Upsert creates or updates a device record (INSERT OR REPLACE).
func (r *DeviceRepository) Upsert(d Device) error {
	const query = `INSERT OR REPLACE INTO devices
		(id, platform, model, os_version, status, battery_level, tags, last_seen, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := r.db.Exec(query,
		d.ID,
		d.Platform,
		d.Model,
		d.OSVersion,
		d.Status,
		d.BatteryLevel,
		tagsToString(d.Tags),
		d.LastSeen,
		d.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert device %s: %w", d.ID, err)
	}
	return nil
}

// FindByID retrieves a device by ID. Returns ErrNotFound if not found.
func (r *DeviceRepository) FindByID(id string) (Device, error) {
	const query = `SELECT id, platform, model, os_version, status, battery_level, tags, last_seen, created_at
		FROM devices WHERE id = ?`

	row := r.db.QueryRow(query, id)
	d, err := scanDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrNotFound
		}
		return Device{}, fmt.Errorf("find device %s: %w", id, err)
	}
	return d, nil
}

// FindAll retrieves devices matching the filter.
// Tags filter uses AND semantics — device must have ALL specified tags.
// Tag filtering is done in Go after fetching (MVP limitation for <1000 devices).
func (r *DeviceRepository) FindAll(filter DeviceFilter) ([]Device, error) {
	query := `SELECT id, platform, model, os_version, status, battery_level, tags, last_seen, created_at
		FROM devices WHERE 1=1`
	args := make([]any, 0)

	if filter.Platform != "" {
		query += " AND platform = ?"
		args = append(args, filter.Platform)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("find all devices: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var devices []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}

	// Apply tag filter in Go (AND semantics: device must have all specified tags).
	if len(filter.Tags) > 0 {
		devices = filterByTags(devices, filter.Tags)
	}

	return devices, nil
}

// UpdateStatus changes the status of a device and updates last_seen.
func (r *DeviceRepository) UpdateStatus(id, status string) error {
	const query = `UPDATE devices SET status = ?, last_seen = ? WHERE id = ?`
	result, err := r.db.Exec(query, status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update status for device %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for device %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateBatteryLevel updates the battery_level field for a device.
func (r *DeviceRepository) UpdateBatteryLevel(id string, level int) error {
	const query = `UPDATE devices SET battery_level = ? WHERE id = ?`
	result, err := r.db.Exec(query, level, id)
	if err != nil {
		return fmt.Errorf("update battery_level for device %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for device %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateLastSeen updates the last_seen timestamp for a device.
func (r *DeviceRepository) UpdateLastSeen(id string, t time.Time) error {
	const query = `UPDATE devices SET last_seen = ? WHERE id = ?`
	result, err := r.db.Exec(query, t, id)
	if err != nil {
		return fmt.Errorf("update last_seen for device %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for device %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a device by ID.
func (r *DeviceRepository) Delete(id string) error {
	const query = `DELETE FROM devices WHERE id = ?`
	result, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("delete device %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for device %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// scanner is the common interface for *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanDevice scans a device from a row or rows result.
func scanDevice(s scanner) (Device, error) {
	var d Device
	var tagsStr string
	var lastSeen sql.NullTime
	var createdAt sql.NullTime

	err := s.Scan(
		&d.ID,
		&d.Platform,
		&d.Model,
		&d.OSVersion,
		&d.Status,
		&d.BatteryLevel,
		&tagsStr,
		&lastSeen,
		&createdAt,
	)
	if err != nil {
		return Device{}, err
	}

	d.Tags = stringToTags(tagsStr)
	if lastSeen.Valid {
		d.LastSeen = lastSeen.Time
	}
	if createdAt.Valid {
		d.CreatedAt = createdAt.Time
	}

	return d, nil
}

// tagsToString joins tags as a comma-separated string.
func tagsToString(tags []string) string {
	return strings.Join(tags, ",")
}

// stringToTags splits a comma-separated tag string into a slice.
// Returns nil for empty strings.
func stringToTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// filterByTags returns devices that have ALL of the specified tags (AND semantics).
func filterByTags(devices []Device, tags []string) []Device {
	result := make([]Device, 0, len(devices))
	for _, d := range devices {
		if deviceHasAllTags(d, tags) {
			result = append(result, d)
		}
	}
	return result
}

// deviceHasAllTags returns true if the device has all specified tags.
func deviceHasAllTags(d Device, tags []string) bool {
	tagSet := make(map[string]struct{}, len(d.Tags))
	for _, t := range d.Tags {
		tagSet[t] = struct{}{}
	}
	for _, required := range tags {
		if _, ok := tagSet[required]; !ok {
			return false
		}
	}
	return true
}

package db

import (
	"testing"
)

// TestSchema_TablesCreated verifies that all three domain tables are created
// when Open() is called for the first time.
func TestSchema_TablesCreated(t *testing.T) {
	db := openMemory(t)

	for _, tableName := range []string{"devices", "jobs", "job_results"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tableName,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", tableName, err)
		}
	}
}

// TestSchema_IndexesExist verifies that all required indexes are present.
func TestSchema_IndexesExist(t *testing.T) {
	db := openMemory(t)

	indexes := []string{
		"idx_devices_status",
		"idx_jobs_status",
		"idx_job_results_job_id",
		"idx_job_results_device_id",
	}

	for _, idx := range indexes {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %q not found: %v", idx, err)
		}
	}
}

// TestSchema_Idempotency verifies that opening the same DB file twice does not error.
func TestSchema_Idempotency(t *testing.T) {
	path := t.TempDir() + "/idempotent.db"

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close() //nolint:errcheck

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { db2.Close() }) //nolint:errcheck

	// schema_migrations must have exactly 4 rows (not 8)
	var count int
	if err := db2.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 4 {
		t.Errorf("schema_migrations rows = %d, want 4", count)
	}
}

// TestSchema_ForeignKeyJobResults_OnDeleteCascade verifies that deleting a job
// also deletes its associated job_results rows.
func TestSchema_ForeignKeyJobResults_OnDeleteCascade(t *testing.T) {
	db := openMemory(t)

	// Insert a device
	_, err := db.Exec(
		`INSERT INTO devices (id, platform) VALUES ('dev-1', 'ios')`,
	)
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	// Insert a job
	_, err = db.Exec(
		`INSERT INTO jobs (id, test_command) VALUES ('job-1', 'xcodebuild test')`,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	// Insert a job_result linking device and job
	_, err = db.Exec(
		`INSERT INTO job_results (id, job_id, device_id) VALUES ('res-1', 'job-1', 'dev-1')`,
	)
	if err != nil {
		t.Fatalf("insert job_result: %v", err)
	}

	// Delete the job — cascade should remove the job_result
	_, err = db.Exec(`DELETE FROM jobs WHERE id='job-1'`)
	if err != nil {
		t.Fatalf("delete job: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM job_results WHERE id='res-1'").Scan(&count); err != nil {
		t.Fatalf("count job_results: %v", err)
	}
	if count != 0 {
		t.Errorf("job_result should have been cascade-deleted, got %d rows", count)
	}
}

// TestSchema_ForeignKeyJobResults_DeviceReference verifies that job_results
// cannot reference a non-existent device_id.
func TestSchema_ForeignKeyJobResults_DeviceReference(t *testing.T) {
	db := openMemory(t)

	// Insert a job without a device
	_, err := db.Exec(
		`INSERT INTO jobs (id, test_command) VALUES ('job-2', 'xcodebuild test')`,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	// Attempt to insert a job_result referencing a non-existent device
	_, err = db.Exec(
		`INSERT INTO job_results (id, job_id, device_id) VALUES ('res-2', 'job-2', 'nonexistent-device')`,
	)
	if err == nil {
		t.Error("expected FK violation when device_id does not exist, got nil")
	}
}

// TestSchema_DeviceFilter_JSONStorage verifies that device_filter stores a JSON string correctly.
func TestSchema_DeviceFilter_JSONStorage(t *testing.T) {
	db := openMemory(t)

	filter := `{"platform":"ios","min_os_version":"16.0"}`
	_, err := db.Exec(
		`INSERT INTO jobs (id, test_command, device_filter) VALUES ('job-3', 'run tests', ?)`,
		filter,
	)
	if err != nil {
		t.Fatalf("insert job with device_filter: %v", err)
	}

	var got string
	if err := db.QueryRow(`SELECT device_filter FROM jobs WHERE id='job-3'`).Scan(&got); err != nil {
		t.Fatalf("read device_filter: %v", err)
	}
	if got != filter {
		t.Errorf("device_filter = %q, want %q", got, filter)
	}
}

// TestSchema_Artifacts_JSONArrayStorage verifies that the artifacts column
// stores a JSON array string correctly.
func TestSchema_Artifacts_JSONArrayStorage(t *testing.T) {
	db := openMemory(t)

	_, err := db.Exec(
		`INSERT INTO devices (id, platform) VALUES ('dev-3', 'android')`,
	)
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO jobs (id, test_command) VALUES ('job-4', 'gradle test')`,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	artifacts := `[{"name":"report.xml","path":"/tmp/report.xml"},{"name":"coverage.html","path":"/tmp/coverage.html"}]`
	_, err = db.Exec(
		`INSERT INTO job_results (id, job_id, device_id, artifacts) VALUES ('res-3', 'job-4', 'dev-3', ?)`,
		artifacts,
	)
	if err != nil {
		t.Fatalf("insert job_result with artifacts: %v", err)
	}

	var got string
	if err := db.QueryRow(`SELECT artifacts FROM job_results WHERE id='res-3'`).Scan(&got); err != nil {
		t.Fatalf("read artifacts: %v", err)
	}
	if got != artifacts {
		t.Errorf("artifacts = %q, want %q", got, artifacts)
	}
}

// TestSchema_InsertAndReadBack verifies a full round-trip insert and read for
// device, job, and job_result rows.
func TestSchema_InsertAndReadBack(t *testing.T) {
	db := openMemory(t)

	// Insert device
	_, err := db.Exec(
		`INSERT INTO devices (id, platform, model, os_version, status, battery_level, tags)
         VALUES ('dev-rt', 'ios', 'iPhone 15', '17.0', 'online', 85, 'ci,fast')`,
	)
	if err != nil {
		t.Fatalf("insert device: %v", err)
	}

	var platform, model, status, tags string
	var battery int
	err = db.QueryRow(
		`SELECT platform, model, status, battery_level, tags FROM devices WHERE id='dev-rt'`,
	).Scan(&platform, &model, &status, &battery, &tags)
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if platform != "ios" || model != "iPhone 15" || status != "online" || battery != 85 || tags != "ci,fast" {
		t.Errorf("device mismatch: platform=%q model=%q status=%q battery=%d tags=%q",
			platform, model, status, battery, tags)
	}

	// Insert job
	_, err = db.Exec(
		`INSERT INTO jobs (id, test_command, strategy, timeout_minutes)
         VALUES ('job-rt', 'xcodebuild -scheme App test', 'fan-out', 45)`,
	)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}

	var testCmd, strategy string
	var timeout int
	err = db.QueryRow(
		`SELECT test_command, strategy, timeout_minutes FROM jobs WHERE id='job-rt'`,
	).Scan(&testCmd, &strategy, &timeout)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if testCmd != "xcodebuild -scheme App test" || strategy != "fan-out" || timeout != 45 {
		t.Errorf("job mismatch: test_command=%q strategy=%q timeout=%d", testCmd, strategy, timeout)
	}

	// Insert job_result
	_, err = db.Exec(
		`INSERT INTO job_results (id, job_id, device_id, status, exit_code, duration_seconds)
         VALUES ('res-rt', 'job-rt', 'dev-rt', 'passed', 0, 120)`,
	)
	if err != nil {
		t.Fatalf("insert job_result: %v", err)
	}

	var resultStatus string
	var exitCode, duration int
	err = db.QueryRow(
		`SELECT status, exit_code, duration_seconds FROM job_results WHERE id='res-rt'`,
	).Scan(&resultStatus, &exitCode, &duration)
	if err != nil {
		t.Fatalf("read job_result: %v", err)
	}
	if resultStatus != "passed" || exitCode != 0 || duration != 120 {
		t.Errorf("job_result mismatch: status=%q exit_code=%d duration=%d",
			resultStatus, exitCode, duration)
	}
}

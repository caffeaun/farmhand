package db

import (
	"database/sql"
	"testing"
)

// TestRecovery_ResetsBusyDevices verifies that busy devices are reset to offline
// and online devices are unaffected.
func TestRecovery_ResetsBusyDevices(t *testing.T) {
	db := openMemory(t)

	_, err := db.Exec(`INSERT INTO devices (id, platform, status) VALUES ('dev-busy', 'ios', 'busy')`)
	if err != nil {
		t.Fatalf("insert busy device: %v", err)
	}
	_, err = db.Exec(`INSERT INTO devices (id, platform, status) VALUES ('dev-online', 'android', 'online')`)
	if err != nil {
		t.Fatalf("insert online device: %v", err)
	}

	result, err := RunRecovery(db)
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}

	if result.DevicesReset != 1 {
		t.Errorf("DevicesReset = %d, want 1", result.DevicesReset)
	}

	var busyStatus string
	if err := db.QueryRow(`SELECT status FROM devices WHERE id='dev-busy'`).Scan(&busyStatus); err != nil {
		t.Fatalf("read busy device status: %v", err)
	}
	if busyStatus != "offline" {
		t.Errorf("busy device status = %q, want %q", busyStatus, "offline")
	}

	var onlineStatus string
	if err := db.QueryRow(`SELECT status FROM devices WHERE id='dev-online'`).Scan(&onlineStatus); err != nil {
		t.Fatalf("read online device status: %v", err)
	}
	if onlineStatus != "online" {
		t.Errorf("online device status = %q, want %q", onlineStatus, "online")
	}
}

// TestRecovery_FailsRunningJobs verifies that running and preparing jobs are
// marked failed with a completed_at timestamp, and completed jobs are unaffected.
func TestRecovery_FailsRunningJobs(t *testing.T) {
	db := openMemory(t)

	_, err := db.Exec(`INSERT INTO jobs (id, test_command, status) VALUES ('job-running', 'run', 'running')`)
	if err != nil {
		t.Fatalf("insert running job: %v", err)
	}
	_, err = db.Exec(`INSERT INTO jobs (id, test_command, status) VALUES ('job-preparing', 'run', 'preparing')`)
	if err != nil {
		t.Fatalf("insert preparing job: %v", err)
	}
	_, err = db.Exec(`INSERT INTO jobs (id, test_command, status) VALUES ('job-completed', 'run', 'completed')`)
	if err != nil {
		t.Fatalf("insert completed job: %v", err)
	}

	result, err := RunRecovery(db)
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}

	if result.JobsFailed != 2 {
		t.Errorf("JobsFailed = %d, want 2", result.JobsFailed)
	}

	for _, id := range []string{"job-running", "job-preparing"} {
		var status string
		var completedAt sql.NullString
		if err := db.QueryRow(`SELECT status, completed_at FROM jobs WHERE id=?`, id).Scan(&status, &completedAt); err != nil {
			t.Fatalf("read job %q: %v", id, err)
		}
		if status != "failed" {
			t.Errorf("job %q status = %q, want %q", id, status, "failed")
		}
		if !completedAt.Valid || completedAt.String == "" {
			t.Errorf("job %q completed_at should be set", id)
		}
	}

	var completedStatus string
	if err := db.QueryRow(`SELECT status FROM jobs WHERE id='job-completed'`).Scan(&completedStatus); err != nil {
		t.Fatalf("read completed job: %v", err)
	}
	if completedStatus != "completed" {
		t.Errorf("completed job status = %q, want %q", completedStatus, "completed")
	}
}

// TestRecovery_NoStaleState verifies that recovery with only clean state
// returns zero counts and causes no changes.
func TestRecovery_NoStaleState(t *testing.T) {
	db := openMemory(t)

	_, err := db.Exec(`INSERT INTO devices (id, platform, status) VALUES ('dev-1', 'ios', 'online')`)
	if err != nil {
		t.Fatalf("insert online device: %v", err)
	}
	_, err = db.Exec(`INSERT INTO jobs (id, test_command, status) VALUES ('job-1', 'run', 'completed')`)
	if err != nil {
		t.Fatalf("insert completed job: %v", err)
	}

	result, err := RunRecovery(db)
	if err != nil {
		t.Fatalf("RunRecovery: %v", err)
	}

	if result.DevicesReset != 0 {
		t.Errorf("DevicesReset = %d, want 0", result.DevicesReset)
	}
	if result.JobsFailed != 0 {
		t.Errorf("JobsFailed = %d, want 0", result.JobsFailed)
	}
}

// TestRecovery_EmptyDatabase verifies that recovery on an empty database
// succeeds and returns zero counts.
func TestRecovery_EmptyDatabase(t *testing.T) {
	db := openMemory(t)

	result, err := RunRecovery(db)
	if err != nil {
		t.Fatalf("RunRecovery on empty db: %v", err)
	}

	if result.DevicesReset != 0 {
		t.Errorf("DevicesReset = %d, want 0", result.DevicesReset)
	}
	if result.JobsFailed != 0 {
		t.Errorf("JobsFailed = %d, want 0", result.JobsFailed)
	}
}

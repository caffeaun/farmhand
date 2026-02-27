package db

import (
	"fmt"
	"time"
)

// RecoveryResult tracks what was recovered during startup.
type RecoveryResult struct {
	DevicesReset int
	JobsFailed   int
}

// RunRecovery resets stale state from a previous crash.
// Devices with status 'busy' are reset to 'offline'.
// Jobs with status 'running', 'preparing', or 'installing' are marked 'failed'.
// This must run BEFORE the device manager polling loop starts.
func RunRecovery(db *DB) (*RecoveryResult, error) {
	result := &RecoveryResult{}

	// Reset busy devices to offline
	res, err := db.Exec(
		"UPDATE devices SET status = 'offline', last_seen = ? WHERE status = 'busy'",
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("recovery: reset busy devices: %w", err)
	}
	rows, _ := res.RowsAffected()
	result.DevicesReset = int(rows)

	// Mark running/preparing/installing jobs as failed
	res, err = db.Exec(
		"UPDATE jobs SET status = 'failed', completed_at = ? WHERE status IN ('running', 'preparing', 'installing')",
		time.Now().UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("recovery: fail stale jobs: %w", err)
	}
	rows, _ = res.RowsAffected()
	result.JobsFailed = int(rows)

	return result, nil
}

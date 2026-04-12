package db

// Migrations contains all database schema migrations in order.
var Migrations = []Migration{
	{
		Version:     1,
		Description: "Create devices table",
		SQL: `CREATE TABLE IF NOT EXISTS devices (
            id TEXT PRIMARY KEY,
            platform TEXT NOT NULL,
            model TEXT NOT NULL DEFAULT '',
            os_version TEXT NOT NULL DEFAULT '',
            status TEXT NOT NULL DEFAULT 'offline',
            battery_level INTEGER NOT NULL DEFAULT -1,
            tags TEXT NOT NULL DEFAULT '',
            last_seen DATETIME,
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
        CREATE INDEX IF NOT EXISTS idx_devices_status ON devices(status);`,
	},
	{
		Version:     2,
		Description: "Create jobs table",
		SQL: `CREATE TABLE IF NOT EXISTS jobs (
            id TEXT PRIMARY KEY,
            status TEXT NOT NULL DEFAULT 'queued',
            strategy TEXT NOT NULL DEFAULT 'fan-out',
            test_command TEXT NOT NULL,
            device_filter TEXT NOT NULL DEFAULT '{}',
            artifact_path TEXT NOT NULL DEFAULT '',
            timeout_minutes INTEGER NOT NULL DEFAULT 30,
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            started_at DATETIME,
            completed_at DATETIME
        );
        CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);`,
	},
	{
		Version:     3,
		Description: "Create job_results table",
		SQL: `CREATE TABLE IF NOT EXISTS job_results (
            id TEXT PRIMARY KEY,
            job_id TEXT NOT NULL,
            device_id TEXT NOT NULL,
            status TEXT NOT NULL DEFAULT 'pending',
            exit_code INTEGER NOT NULL DEFAULT -1,
            duration_seconds INTEGER NOT NULL DEFAULT 0,
            log_path TEXT NOT NULL DEFAULT '',
            artifacts TEXT NOT NULL DEFAULT '[]',
            created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
            FOREIGN KEY (device_id) REFERENCES devices(id)
        );
        CREATE INDEX IF NOT EXISTS idx_job_results_job_id ON job_results(job_id);
        CREATE INDEX IF NOT EXISTS idx_job_results_device_id ON job_results(device_id);`,
	},
	{
		Version:     4,
		Description: "Add error_message to job_results",
		SQL:         `ALTER TABLE job_results ADD COLUMN error_message TEXT NOT NULL DEFAULT ''`,
	},
	{
		Version:     5,
		Description: "Add install_command to jobs",
		SQL:         `ALTER TABLE jobs ADD COLUMN install_command TEXT NOT NULL DEFAULT ''`,
	},
	{
		Version:     6,
		Description: "Add hardware_id to devices",
		SQL:         `ALTER TABLE devices ADD COLUMN hardware_id TEXT NOT NULL DEFAULT ''`,
	},
}

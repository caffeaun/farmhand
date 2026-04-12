package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Job represents a job record in the database.
type Job struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`          // queued, preparing, installing, running, completed, failed, cancelled
	Strategy       string     `json:"strategy"`        // fan-out (only supported in MVP)
	TestCommand    string     `json:"test_command"`
	InstallCommand string     `json:"install_command"`
	DeviceFilter   string     `json:"device_filter"`   // JSON string
	ArtifactPath   string     `json:"artifact_path"`
	TimeoutMinutes int        `json:"timeout_minutes"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// JobResult represents a per-device execution result.
type JobResult struct {
	ID              string    `json:"id"`
	JobID           string    `json:"job_id"`
	DeviceID        string    `json:"device_id"`
	Status          string    `json:"status"`          // pending, passed, failed, error, skipped
	ExitCode        int       `json:"exit_code"`
	DurationSeconds int       `json:"duration_seconds"`
	LogPath         string    `json:"log_path"`
	Artifacts       string    `json:"artifacts"`       // JSON array string
	ErrorMessage    string    `json:"error_message"`
	CreatedAt       time.Time `json:"created_at"`
}

// JobFilter defines criteria for filtering jobs.
type JobFilter struct {
	Status string
	Limit  int // max results, 0 = no limit
}

// JobRepository handles job CRUD operations.
type JobRepository struct {
	db *DB
}

// NewJobRepository creates a new job repository.
func NewJobRepository(db *DB) *JobRepository {
	return &JobRepository{db: db}
}

// Create inserts a new job with a generated UUID and created_at timestamp.
func (r *JobRepository) Create(j *Job) error {
	j.ID = uuid.New().String()
	j.CreatedAt = time.Now().UTC()

	const query = `INSERT INTO jobs
		(id, status, strategy, test_command, install_command, device_filter, artifact_path, timeout_minutes, created_at, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := r.db.Exec(query,
		j.ID,
		j.Status,
		j.Strategy,
		j.TestCommand,
		j.InstallCommand,
		j.DeviceFilter,
		j.ArtifactPath,
		j.TimeoutMinutes,
		j.CreatedAt,
		j.StartedAt,
		j.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("create job: %w", err)
	}
	return nil
}

// FindByID retrieves a job by ID. Returns ErrNotFound if not found.
func (r *JobRepository) FindByID(id string) (Job, error) {
	const query = `SELECT id, status, strategy, test_command, install_command, device_filter, artifact_path, timeout_minutes, created_at, started_at, completed_at
		FROM jobs WHERE id = ?`

	row := r.db.QueryRow(query, id)
	j, err := scanJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("find job %s: %w", id, err)
	}
	return j, nil
}

// FindAll returns jobs matching the filter, sorted by created_at DESC.
// If filter.Limit > 0, limits the number of results returned.
func (r *JobRepository) FindAll(filter JobFilter) ([]Job, error) {
	query := `SELECT id, status, strategy, test_command, install_command, device_filter, artifact_path, timeout_minutes, created_at, started_at, completed_at
		FROM jobs WHERE 1=1`
	args := make([]any, 0)

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("find all jobs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}

	return jobs, nil
}

// UpdateStatus changes the status of a job.
func (r *JobRepository) UpdateStatus(id, status string) error {
	const query = `UPDATE jobs SET status = ? WHERE id = ?`
	result, err := r.db.Exec(query, status, id)
	if err != nil {
		return fmt.Errorf("update status for job %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for job %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStarted sets started_at and status to 'running'.
func (r *JobRepository) SetStarted(id string, t time.Time) error {
	const query = `UPDATE jobs SET started_at = ?, status = 'running' WHERE id = ?`
	result, err := r.db.Exec(query, t, id)
	if err != nil {
		return fmt.Errorf("set started for job %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for job %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCompleted sets completed_at and the final status.
// Note: exit_code is NOT on the jobs table — it lives on job_results only.
func (r *JobRepository) SetCompleted(id string, t time.Time, status string) error {
	const query = `UPDATE jobs SET completed_at = ?, status = ? WHERE id = ?`
	result, err := r.db.Exec(query, t, status, id)
	if err != nil {
		return fmt.Errorf("set completed for job %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for job %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a job by ID. Job results are cascade-deleted via FK constraint.
func (r *JobRepository) Delete(id string) error {
	const query = `DELETE FROM jobs WHERE id = ?`
	result, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("delete job %s: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for job %s: %w", id, err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// scanJob scans a job from a row or rows result.
func scanJob(s scanner) (Job, error) {
	var j Job
	var startedAt sql.NullTime
	var completedAt sql.NullTime
	var createdAt sql.NullTime

	err := s.Scan(
		&j.ID,
		&j.Status,
		&j.Strategy,
		&j.TestCommand,
		&j.InstallCommand,
		&j.DeviceFilter,
		&j.ArtifactPath,
		&j.TimeoutMinutes,
		&createdAt,
		&startedAt,
		&completedAt,
	)
	if err != nil {
		return Job{}, err
	}

	if createdAt.Valid {
		j.CreatedAt = createdAt.Time
	}
	if startedAt.Valid {
		j.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		j.CompletedAt = &completedAt.Time
	}

	return j, nil
}

// JobResultRepository handles job result CRUD operations.
type JobResultRepository struct {
	db *DB
}

// NewJobResultRepository creates a new job result repository.
func NewJobResultRepository(db *DB) *JobResultRepository {
	return &JobResultRepository{db: db}
}

// Create inserts a new job result with a generated UUID and created_at timestamp.
func (r *JobResultRepository) Create(jr *JobResult) error {
	jr.ID = uuid.New().String()
	jr.CreatedAt = time.Now().UTC()

	const query = `INSERT INTO job_results
		(id, job_id, device_id, status, exit_code, duration_seconds, log_path, artifacts, error_message, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := r.db.Exec(query,
		jr.ID,
		jr.JobID,
		jr.DeviceID,
		jr.Status,
		jr.ExitCode,
		jr.DurationSeconds,
		jr.LogPath,
		jr.Artifacts,
		jr.ErrorMessage,
		jr.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create job result: %w", err)
	}
	return nil
}

// FindByJobID returns all results for a given job.
func (r *JobResultRepository) FindByJobID(jobID string) ([]JobResult, error) {
	const query = `SELECT id, job_id, device_id, status, exit_code, duration_seconds, log_path, artifacts, error_message, created_at
		FROM job_results WHERE job_id = ?`

	rows, err := r.db.Query(query, jobID)
	if err != nil {
		return nil, fmt.Errorf("find results for job %s: %w", jobID, err)
	}
	defer rows.Close() //nolint:errcheck

	var results []JobResult
	for rows.Next() {
		jr, err := scanJobResult(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job result: %w", err)
		}
		results = append(results, jr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate job results: %w", err)
	}

	return results, nil
}

// FindByID retrieves a single job result by ID. Returns ErrNotFound if not found.
func (r *JobResultRepository) FindByID(id string) (JobResult, error) {
	const query = `SELECT id, job_id, device_id, status, exit_code, duration_seconds, log_path, artifacts, error_message, created_at
		FROM job_results WHERE id = ?`

	row := r.db.QueryRow(query, id)
	jr, err := scanJobResult(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobResult{}, ErrNotFound
		}
		return JobResult{}, fmt.Errorf("find job result %s: %w", id, err)
	}
	return jr, nil
}

// scanJobResult scans a job result from a row or rows result.
// Column order must match the SELECT list: id, job_id, device_id, status,
// exit_code, duration_seconds, log_path, artifacts, error_message, created_at.
func scanJobResult(s scanner) (JobResult, error) {
	var jr JobResult
	var createdAt sql.NullTime

	err := s.Scan(
		&jr.ID,
		&jr.JobID,
		&jr.DeviceID,
		&jr.Status,
		&jr.ExitCode,
		&jr.DurationSeconds,
		&jr.LogPath,
		&jr.Artifacts,
		&jr.ErrorMessage,
		&createdAt,
	)
	if err != nil {
		return JobResult{}, err
	}

	if createdAt.Valid {
		jr.CreatedAt = createdAt.Time
	}

	return jr, nil
}

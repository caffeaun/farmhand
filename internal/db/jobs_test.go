package db

import (
	"errors"
	"testing"
	"time"
)

// newTestJob returns a Job with sensible defaults for testing.
func newTestJob() Job {
	return Job{
		Status:         "queued",
		Strategy:       "fan-out",
		TestCommand:    "go test ./...",
		DeviceFilter:   "{}",
		ArtifactPath:   "/tmp/artifacts",
		TimeoutMinutes: 30,
	}
}

// insertTestDevice inserts a minimal device for FK constraints on job_results.
func insertTestDevice(t *testing.T, db *DB, id string) {
	t.Helper()
	d := newTestDevice(id, "android")
	repo := NewDeviceRepository(db)
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("insert test device %s: %v", id, err)
	}
}

func TestJobCreate(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j := newTestJob()
	if err := repo.Create(&j); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if j.ID == "" {
		t.Error("ID should be set after Create")
	}
	if j.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set after Create")
	}
}

func TestJobFindByID_Found(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j := newTestJob()
	if err := repo.Create(&j); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.FindByID(j.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != j.ID {
		t.Errorf("ID = %q, want %q", got.ID, j.ID)
	}
	if got.Status != j.Status {
		t.Errorf("Status = %q, want %q", got.Status, j.Status)
	}
	if got.TestCommand != j.TestCommand {
		t.Errorf("TestCommand = %q, want %q", got.TestCommand, j.TestCommand)
	}
}

func TestJobFindByID_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	_, err := repo.FindByID("nonexistent-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestJobFindAll_NoFilter(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	// Insert 3 jobs with slight time separation to ensure ordering is deterministic.
	for i := 0; i < 3; i++ {
		j := newTestJob()
		if err := repo.Create(&j); err != nil {
			t.Fatalf("Create job %d: %v", i, err)
		}
	}

	got, err := repo.FindAll(JobFilter{})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestJobFindAll_StatusFilter(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j1 := newTestJob()
	j1.Status = "queued"
	j2 := newTestJob()
	j2.Status = "completed"
	j3 := newTestJob()
	j3.Status = "queued"

	for _, j := range []*Job{&j1, &j2, &j3} {
		if err := repo.Create(j); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := repo.FindAll(JobFilter{Status: "queued"})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	for _, j := range got {
		if j.Status != "queued" {
			t.Errorf("Status = %q, want queued", j.Status)
		}
	}
}

func TestJobFindAll_Limit(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	for i := 0; i < 5; i++ {
		j := newTestJob()
		if err := repo.Create(&j); err != nil {
			t.Fatalf("Create job %d: %v", i, err)
		}
	}

	got, err := repo.FindAll(JobFilter{Limit: 3})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestJobFindAll_OrderedByCreatedAtDesc(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	// Insert jobs with explicit created_at values to test ordering.
	// We manually insert rather than using Create to control timestamps.
	times := []time.Time{
		time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 3, 10, 0, 0, 0, time.UTC),
		time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC),
	}

	const insertQuery = `INSERT INTO jobs
		(id, status, strategy, test_command, device_filter, artifact_path, timeout_minutes, created_at)
		VALUES (?, 'queued', 'fan-out', 'go test ./...', '{}', '', 30, ?)`

	ids := []string{"job-ord-a", "job-ord-b", "job-ord-c"}
	for i, id := range ids {
		if _, err := db.Exec(insertQuery, id, times[i]); err != nil {
			t.Fatalf("insert job %s: %v", id, err)
		}
	}

	got, err := repo.FindAll(JobFilter{})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}

	// Expect DESC order: job-ord-b (Jan 3), job-ord-c (Jan 2), job-ord-a (Jan 1)
	wantOrder := []string{"job-ord-b", "job-ord-c", "job-ord-a"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("jobs[%d].ID = %q, want %q", i, got[i].ID, want)
		}
	}
}

func TestJobSetStarted(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j := newTestJob()
	if err := repo.Create(&j); err != nil {
		t.Fatalf("Create: %v", err)
	}

	startTime := time.Now().UTC().Truncate(time.Second)
	if err := repo.SetStarted(j.ID, startTime); err != nil {
		t.Fatalf("SetStarted: %v", err)
	}

	got, err := repo.FindByID(j.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Fatal("StartedAt should not be nil")
	}
	if !got.StartedAt.Equal(startTime) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, startTime)
	}
}

func TestJobSetCompleted(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j := newTestJob()
	if err := repo.Create(&j); err != nil {
		t.Fatalf("Create: %v", err)
	}

	completedTime := time.Now().UTC().Truncate(time.Second)
	if err := repo.SetCompleted(j.ID, completedTime, "completed"); err != nil {
		t.Fatalf("SetCompleted: %v", err)
	}

	got, err := repo.FindByID(j.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("Status = %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Fatal("CompletedAt should not be nil")
	}
	if !got.CompletedAt.Equal(completedTime) {
		t.Errorf("CompletedAt = %v, want %v", got.CompletedAt, completedTime)
	}
}

func TestJobUpdateStatus(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	j := newTestJob()
	if err := repo.Create(&j); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdateStatus(j.ID, "failed"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := repo.FindByID(j.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "failed" {
		t.Errorf("Status = %q, want failed", got.Status)
	}
}

func TestJobUpdateStatus_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewJobRepository(db)

	err := repo.UpdateStatus("nonexistent-id", "failed")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestJobDelete_CascadesToResults(t *testing.T) {
	db := openMemory(t)
	jobRepo := NewJobRepository(db)
	resultRepo := NewJobResultRepository(db)

	// Create a device for FK constraint.
	insertTestDevice(t, db, "dev-cascade-1")

	// Create a job.
	j := newTestJob()
	if err := jobRepo.Create(&j); err != nil {
		t.Fatalf("Create job: %v", err)
	}

	// Create a job result linked to the job.
	jr := JobResult{
		JobID:    j.ID,
		DeviceID: "dev-cascade-1",
		Status:   "pending",
		Artifacts: "[]",
	}
	if err := resultRepo.Create(&jr); err != nil {
		t.Fatalf("Create job result: %v", err)
	}

	// Verify result exists.
	results, err := resultRepo.FindByJobID(j.ID)
	if err != nil {
		t.Fatalf("FindByJobID before delete: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	// Delete the job — cascade should remove results.
	if err := jobRepo.Delete(j.ID); err != nil {
		t.Fatalf("Delete job: %v", err)
	}

	// Job should be gone.
	_, err = jobRepo.FindByID(j.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByID after delete: err = %v, want ErrNotFound", err)
	}

	// Results should also be gone via CASCADE.
	results, err = resultRepo.FindByJobID(j.ID)
	if err != nil {
		t.Fatalf("FindByJobID after delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after cascade delete, got %d", len(results))
	}
}

func TestJobResultCreate(t *testing.T) {
	db := openMemory(t)
	jobRepo := NewJobRepository(db)
	resultRepo := NewJobResultRepository(db)

	insertTestDevice(t, db, "dev-result-1")

	j := newTestJob()
	if err := jobRepo.Create(&j); err != nil {
		t.Fatalf("Create job: %v", err)
	}

	jr := JobResult{
		JobID:           j.ID,
		DeviceID:        "dev-result-1",
		Status:          "pending",
		ExitCode:        0,
		DurationSeconds: 0,
		LogPath:         "/var/log/job.log",
		Artifacts:       "[]",
	}
	if err := resultRepo.Create(&jr); err != nil {
		t.Fatalf("Create job result: %v", err)
	}

	if jr.ID == "" {
		t.Error("ID should be set after Create")
	}
	if jr.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set after Create")
	}
	if jr.JobID != j.ID {
		t.Errorf("JobID = %q, want %q", jr.JobID, j.ID)
	}
	if jr.DeviceID != "dev-result-1" {
		t.Errorf("DeviceID = %q, want dev-result-1", jr.DeviceID)
	}
}

func TestJobResultFindByJobID(t *testing.T) {
	db := openMemory(t)
	jobRepo := NewJobRepository(db)
	resultRepo := NewJobResultRepository(db)

	insertTestDevice(t, db, "dev-fjid-1")
	insertTestDevice(t, db, "dev-fjid-2")

	j := newTestJob()
	if err := jobRepo.Create(&j); err != nil {
		t.Fatalf("Create job: %v", err)
	}

	// Create a second job to ensure FindByJobID only returns results for the right job.
	j2 := newTestJob()
	if err := jobRepo.Create(&j2); err != nil {
		t.Fatalf("Create job2: %v", err)
	}

	jr1 := JobResult{JobID: j.ID, DeviceID: "dev-fjid-1", Status: "passed", Artifacts: "[]"}
	jr2 := JobResult{JobID: j.ID, DeviceID: "dev-fjid-2", Status: "failed", Artifacts: "[]"}
	jr3 := JobResult{JobID: j2.ID, DeviceID: "dev-fjid-1", Status: "passed", Artifacts: "[]"}

	for _, jr := range []*JobResult{&jr1, &jr2, &jr3} {
		if err := resultRepo.Create(jr); err != nil {
			t.Fatalf("Create job result: %v", err)
		}
	}

	got, err := resultRepo.FindByJobID(j.ID)
	if err != nil {
		t.Fatalf("FindByJobID: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.JobID != j.ID {
			t.Errorf("JobID = %q, want %q", r.JobID, j.ID)
		}
	}
}

func TestJobResultFindByID_NotFound(t *testing.T) {
	db := openMemory(t)
	resultRepo := NewJobResultRepository(db)

	_, err := resultRepo.FindByID("nonexistent-result-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

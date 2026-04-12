package job

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// fakeDeviceManager implements the deviceManager interface for tests.
// It delegates directly to a DeviceRepository so tests can pre-seed devices
// without needing to start a real Manager (which requires ADB/iOS bridges).
type fakeDeviceManager struct {
	repo *db.DeviceRepository
}

func (f *fakeDeviceManager) List(filter db.DeviceFilter) ([]db.Device, error) {
	return f.repo.FindAll(filter)
}

// openTestDB opens a file-backed SQLite DB in t.TempDir().
// File-backed so WAL mode and foreign keys work as in production.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() }) //nolint:errcheck
	return database
}

// makeOnlineDevice returns a db.Device with status "online".
func makeOnlineDevice(id, platform string, tags ...string) db.Device {
	now := time.Now().UTC()
	return db.Device{
		ID:        id,
		Platform:  platform,
		Model:     "TestModel",
		OSVersion: "1.0",
		Status:    "online",
		Tags:      tags,
		LastSeen:  now,
		CreatedAt: now,
	}
}

// seedDevices inserts the given devices into the repository.
func seedDevices(t *testing.T, repo *db.DeviceRepository, devices ...db.Device) {
	t.Helper()
	for _, d := range devices {
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("seed device %s: %v", d.ID, err)
		}
	}
}

// makeTestJob returns a db.Job with the given strategy and device_filter JSON.
// The job is created in the repository and its ID is populated.
func makeTestJob(t *testing.T, repo *db.JobRepository, strategy, deviceFilter string) db.Job {
	t.Helper()
	j := db.Job{
		Status:         "queued",
		Strategy:       strategy,
		TestCommand:    "go test ./...",
		DeviceFilter:   deviceFilter,
		ArtifactPath:   "/tmp/artifacts",
		TimeoutMinutes: 30,
	}
	if err := repo.Create(&j); err != nil {
		t.Fatalf("create test job: %v", err)
	}
	return j
}

// newTestScheduler builds a Scheduler backed by the given DB for unit tests.
func newTestScheduler(t *testing.T, database *db.DB, bus *events.Bus) (*Scheduler, *db.DeviceRepository, *db.JobRepository) {
	t.Helper()
	devRepo := db.NewDeviceRepository(database)
	jobRepo := db.NewJobRepository(database)
	mgr := &fakeDeviceManager{repo: devRepo}
	s := NewScheduler(mgr, jobRepo, devRepo, bus, zerolog.Nop())
	return s, devRepo, jobRepo
}

func TestSchedule_FanOut_AllDevices(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("dev-1", "android"),
		makeOnlineDevice("dev-2", "ios"),
		makeOnlineDevice("dev-3", "android"),
	)

	job := makeTestJob(t, jobRepo, "fan-out", "{}")

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 3 {
		t.Errorf("len(executions) = %d, want 3", len(executions))
	}
	for _, e := range executions {
		if e.JobID != job.ID {
			t.Errorf("Execution.JobID = %q, want %q", e.JobID, job.ID)
		}
		if e.TestCommand != job.TestCommand {
			t.Errorf("Execution.TestCommand = %q, want %q", e.TestCommand, job.TestCommand)
		}
	}
}

func TestSchedule_PlatformFilter(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("android-1", "android"),
		makeOnlineDevice("android-2", "android"),
		makeOnlineDevice("ios-1", "ios"),
	)

	filter, _ := json.Marshal(DeviceFilter{Platform: "android"})
	job := makeTestJob(t, jobRepo, "fan-out", string(filter))

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 2 {
		t.Errorf("len(executions) = %d, want 2", len(executions))
	}
	for _, e := range executions {
		if e.DevicePlatform != "android" {
			t.Errorf("Execution.DevicePlatform = %q, want android", e.DevicePlatform)
		}
	}
}

func TestSchedule_TagsFilter(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("tagged-1", "android", "ci", "smoke"),
		makeOnlineDevice("tagged-2", "android", "ci"),
		makeOnlineDevice("tagged-3", "android", "smoke"),
		makeOnlineDevice("tagged-4", "ios", "ci", "smoke"),
	)

	// Only devices with BOTH "ci" and "smoke" tags should be selected (AND semantics).
	filter, _ := json.Marshal(DeviceFilter{Tags: []string{"ci", "smoke"}})
	job := makeTestJob(t, jobRepo, "fan-out", string(filter))

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 2 {
		t.Errorf("len(executions) = %d, want 2 (only tagged-1 and tagged-4 have both tags)", len(executions))
	}
	ids := make(map[string]bool)
	for _, e := range executions {
		ids[e.DeviceID] = true
	}
	for _, want := range []string{"tagged-1", "tagged-4"} {
		if !ids[want] {
			t.Errorf("expected device %q in executions", want)
		}
	}
}

func TestSchedule_DeviceIDsFilter(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("pick-1", "android"),
		makeOnlineDevice("pick-2", "android"),
		makeOnlineDevice("pick-3", "android"),
	)

	filter, _ := json.Marshal(DeviceFilter{DeviceIDs: []string{"pick-1", "pick-3"}})
	job := makeTestJob(t, jobRepo, "fan-out", string(filter))

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 2 {
		t.Errorf("len(executions) = %d, want 2", len(executions))
	}
	ids := make(map[string]bool)
	for _, e := range executions {
		ids[e.DeviceID] = true
	}
	if !ids["pick-1"] || !ids["pick-3"] {
		t.Errorf("expected pick-1 and pick-3 in executions, got %v", ids)
	}
	if ids["pick-2"] {
		t.Error("pick-2 should not be in executions")
	}
}

func TestSchedule_MaxDevices(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("max-1", "android"),
		makeOnlineDevice("max-2", "android"),
		makeOnlineDevice("max-3", "android"),
		makeOnlineDevice("max-4", "android"),
	)

	filter, _ := json.Marshal(DeviceFilter{MaxDevices: 2})
	job := makeTestJob(t, jobRepo, "fan-out", string(filter))

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 2 {
		t.Errorf("len(executions) = %d, want 2 (MaxDevices capped)", len(executions))
	}
}

func TestSchedule_NoMatchingDevices(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	// Only android devices available, filter requests ios.
	seedDevices(t, devRepo,
		makeOnlineDevice("android-only", "android"),
	)

	filter, _ := json.Marshal(DeviceFilter{Platform: "ios"})
	job := makeTestJob(t, jobRepo, "fan-out", string(filter))

	_, err := s.Schedule(job)
	if err == nil {
		t.Fatal("expected error when no devices match, got nil")
	}
	// Error should be descriptive.
	if err.Error() != "no online devices match the filter" {
		t.Errorf("err = %q, want descriptive error", err.Error())
	}
}

func TestSchedule_UnsupportedStrategy_Shard(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, _, jobRepo := newTestScheduler(t, database, bus)

	job := makeTestJob(t, jobRepo, "shard", "{}")

	_, err := s.Schedule(job)
	if err == nil {
		t.Fatal("expected error for shard strategy, got nil")
	}
	wantErr := "unsupported strategy: shard"
	if err.Error() != wantErr {
		t.Errorf("err = %q, want %q", err.Error(), wantErr)
	}
}

func TestSchedule_UnsupportedStrategy_Targeted(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, _, jobRepo := newTestScheduler(t, database, bus)

	job := makeTestJob(t, jobRepo, "targeted", "{}")

	_, err := s.Schedule(job)
	if err == nil {
		t.Fatal("expected error for targeted strategy, got nil")
	}
	wantErr := "unsupported strategy: targeted"
	if err.Error() != wantErr {
		t.Errorf("err = %q, want %q", err.Error(), wantErr)
	}
}

func TestSchedule_DevicesMarkedBusy(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("busy-1", "android"),
		makeOnlineDevice("busy-2", "android"),
	)

	job := makeTestJob(t, jobRepo, "fan-out", "{}")

	executions, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(executions) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(executions))
	}

	// Verify both devices are now 'busy' in the DB.
	for _, e := range executions {
		d, err := devRepo.FindByID(e.DeviceID)
		if err != nil {
			t.Fatalf("FindByID %s: %v", e.DeviceID, err)
		}
		if d.Status != "busy" {
			t.Errorf("device %s status = %q, want busy", e.DeviceID, d.Status)
		}
	}
}

func TestSchedule_JobStartedEvent(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("evt-dev-1", "android"),
	)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	job := makeTestJob(t, jobRepo, "fan-out", "{}")

	_, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	select {
	case ev := <-sub:
		if ev.Type != events.JobStarted {
			t.Errorf("event type = %q, want %q", ev.Type, events.JobStarted)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for JobStarted event")
	}
}

func TestSchedule_JobStatusSetToRunning(t *testing.T) {
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	s, devRepo, jobRepo := newTestScheduler(t, database, bus)

	seedDevices(t, devRepo,
		makeOnlineDevice("status-dev-1", "android"),
	)

	job := makeTestJob(t, jobRepo, "fan-out", "{}")

	_, err := s.Schedule(job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	updated, err := jobRepo.FindByID(job.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if updated.Status != "running" {
		t.Errorf("job status = %q, want running", updated.Status)
	}
	if updated.StartedAt == nil {
		t.Error("job StartedAt should be set after scheduling")
	}
}

func TestSchedule_ConcurrentSafety(t *testing.T) {
	// This test verifies no data races occur when Schedule is called concurrently.
	// Run with: go test -race ./internal/job/...
	database := openTestDB(t)
	bus := events.New()
	defer bus.Close()

	devRepo := db.NewDeviceRepository(database)
	jobRepo := db.NewJobRepository(database)
	mgr := &fakeDeviceManager{repo: devRepo}
	s := NewScheduler(mgr, jobRepo, devRepo, bus, zerolog.Nop())

	// Seed enough devices for both goroutines to succeed independently.
	seedDevices(t, devRepo,
		makeOnlineDevice("race-dev-1", "android"),
		makeOnlineDevice("race-dev-2", "android"),
		makeOnlineDevice("race-dev-3", "android"),
		makeOnlineDevice("race-dev-4", "android"),
	)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job := db.Job{
				Status:         "queued",
				Strategy:       "fan-out",
				TestCommand:    "go test ./...",
				DeviceFilter:   `{"max_devices": 2}`,
				ArtifactPath:   "/tmp/artifacts",
				TimeoutMinutes: 30,
			}
			if err := jobRepo.Create(&job); err != nil {
				errs[idx] = err
				return
			}
			_, errs[idx] = s.Schedule(job)
		}(i)
	}

	wg.Wait()

	// At least one goroutine should succeed; the other might get "no online devices"
	// if the first grabbed all. Both outcomes are valid — we just check for no panics/races.
	successCount := 0
	for _, err := range errs {
		if err == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Errorf("expected at least one Schedule to succeed, both failed: %v, %v", errs[0], errs[1])
	}
}

// TestSchedule_InstallCommand_CopiedToExecutions verifies that
// InstallCommand is propagated from the job to each Execution.
func TestSchedule_InstallCommand_CopiedToExecutions(t *testing.T) {
	database := openTestDB(t)
	deviceRepo := db.NewDeviceRepository(database)
	jobRepo := db.NewJobRepository(database)
	bus := events.New()
	defer bus.Close()

	// Seed two online devices.
	if err := deviceRepo.Upsert(makeOnlineDevice("dev-a", "android")); err != nil {
		t.Fatalf("upsert dev-a: %v", err)
	}
	if err := deviceRepo.Upsert(makeOnlineDevice("dev-b", "android")); err != nil {
		t.Fatalf("upsert dev-b: %v", err)
	}

	s := NewScheduler(&fakeDeviceManager{repo: deviceRepo}, jobRepo, deviceRepo, bus,
		zerolog.New(zerolog.NewTestWriter(t)))

	j := db.Job{
		Status:         "queued",
		Strategy:       "fan-out",
		TestCommand:    "echo test",
		InstallCommand: "adb install -r /tmp/app.apk",
		DeviceFilter:   "{}",
		TimeoutMinutes: 10,
	}
	if err := jobRepo.Create(&j); err != nil {
		t.Fatalf("create job: %v", err)
	}

	executions, err := s.Schedule(j)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	if len(executions) != 2 {
		t.Fatalf("expected 2 executions, got %d", len(executions))
	}

	for _, ex := range executions {
		if ex.InstallCommand != "adb install -r /tmp/app.apk" {
			t.Errorf("execution for %s: InstallCommand = %q, want %q",
				ex.DeviceID, ex.InstallCommand, "adb install -r /tmp/app.apk")
		}
	}
}

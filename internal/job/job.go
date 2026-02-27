package job

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// deviceManager is the subset of *device.Manager methods used by Scheduler.
// Defined here (consumer side) so tests can inject a fake without touching device.Manager.
type deviceManager interface {
	List(filter db.DeviceFilter) ([]db.Device, error)
}

// Scheduler handles job scheduling and device matching.
// It selects target devices for a job using the fan-out strategy and creates
// one Execution per matching device.
type Scheduler struct {
	deviceMgr  deviceManager
	jobRepo    *db.JobRepository
	deviceRepo *db.DeviceRepository
	bus        *events.Bus
	logger     zerolog.Logger
	mu         sync.Mutex // prevents TOCTOU race on concurrent Schedule() calls
}

// NewScheduler creates a new Scheduler.
// deviceMgr is typically *device.Manager; it satisfies the deviceManager interface.
func NewScheduler(
	deviceMgr deviceManager,
	jobRepo *db.JobRepository,
	deviceRepo *db.DeviceRepository,
	bus *events.Bus,
	logger zerolog.Logger,
) *Scheduler {
	return &Scheduler{
		deviceMgr:  deviceMgr,
		jobRepo:    jobRepo,
		deviceRepo: deviceRepo,
		bus:        bus,
		logger:     logger,
	}
}

// Schedule selects target devices and creates Executions for a job.
// Only the fan-out strategy is supported in MVP.
// Returns an error if the strategy is 'shard' or 'targeted'.
// Returns an error if no devices match the filter.
// Marks selected devices as 'busy'.
// Publishes a JobStarted event.
func (s *Scheduler) Schedule(job db.Job) ([]*Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate strategy — only "fan-out" or "" (default) supported.
	if job.Strategy != "" && job.Strategy != "fan-out" {
		return nil, fmt.Errorf("unsupported strategy: %s", job.Strategy)
	}

	// Parse device_filter from job.DeviceFilter (JSON string).
	var filter DeviceFilter
	if job.DeviceFilter != "" && job.DeviceFilter != "{}" {
		if err := json.Unmarshal([]byte(job.DeviceFilter), &filter); err != nil {
			return nil, fmt.Errorf("parse device_filter: %w", err)
		}
	}

	// Get online devices from the manager using platform and tag filters.
	dbFilter := db.DeviceFilter{
		Platform: filter.Platform,
		Status:   "online",
		Tags:     filter.Tags,
	}
	devices, err := s.deviceMgr.List(dbFilter)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	// If DeviceIDs specified, further filter to only those IDs.
	if len(filter.DeviceIDs) > 0 {
		idSet := make(map[string]struct{}, len(filter.DeviceIDs))
		for _, id := range filter.DeviceIDs {
			idSet[id] = struct{}{}
		}
		filtered := make([]db.Device, 0, len(devices))
		for _, d := range devices {
			if _, ok := idSet[d.ID]; ok {
				filtered = append(filtered, d)
			}
		}
		devices = filtered
	}

	// If MaxDevices > 0, cap the list.
	if filter.MaxDevices > 0 && len(devices) > filter.MaxDevices {
		devices = devices[:filter.MaxDevices]
	}

	// No matching devices — return a descriptive error.
	if len(devices) == 0 {
		return nil, fmt.Errorf("no online devices match the filter")
	}

	// Mark each device as 'busy' and build one Execution per device.
	executions := make([]*Execution, 0, len(devices))
	for _, d := range devices {
		if err := s.deviceRepo.UpdateStatus(d.ID, "busy"); err != nil {
			return nil, fmt.Errorf("mark device %s busy: %w", d.ID, err)
		}
		executions = append(executions, &Execution{
			JobID:          job.ID,
			DeviceID:       d.ID,
			DeviceSerial:   d.ID, // serial == DB ID for ADB/xcrun devices
			DevicePlatform: d.Platform,
			TestCommand:    job.TestCommand,
			TimeoutMinutes: job.TimeoutMinutes,
		})
	}

	// Update job status to 'running'.
	if err := s.jobRepo.SetStarted(job.ID, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("set job %s started: %w", job.ID, err)
	}

	// Publish JobStarted event.
	s.bus.Publish(events.Event{
		Type:      events.JobStarted,
		Payload:   job,
		Timestamp: time.Now().UTC(),
	})

	s.logger.Info().
		Str("job_id", job.ID).
		Int("device_count", len(executions)).
		Msg("job scheduled")

	return executions, nil
}

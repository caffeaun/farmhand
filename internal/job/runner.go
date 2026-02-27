package job

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/caffeaun/farmhand/internal/notify"
	"github.com/rs/zerolog"
)

// executor is the consumer-side interface for running a single execution.
type executor interface {
	Run(ctx context.Context, exec Execution, outputCh chan<- string) ExecResult
}

// jobRepo is the consumer-side interface for job persistence.
type jobRepo interface {
	SetCompleted(id string, t time.Time, status string) error
}

// jobResultRepo is the consumer-side interface for job result persistence.
type jobResultRepo interface {
	Create(jr *db.JobResult) error
}

// deviceRepo is the consumer-side interface for device status updates.
type deviceRepo interface {
	UpdateStatus(id, status string) error
}

// artifactCollector is the consumer-side interface for collecting artifacts.
type artifactCollector interface {
	Collect(jobID, deviceID, sourceDir string) ([]Artifact, error)
}

// notifier is the consumer-side interface for sending webhook notifications.
type notifier interface {
	Send(event notify.WebhookEvent)
}

// eventBus is the consumer-side interface for publishing internal events.
type eventBus interface {
	Publish(event events.Event)
}

// Runner wires together Executor, ArtifactCollector, and repositories to
// persist results, reset device status, and fire notifications after each
// per-device execution completes.
type Runner struct {
	exec      executor
	jobRepo   jobRepo
	resultRepo jobResultRepo
	deviceRepo deviceRepo
	artifacts  artifactCollector
	notifier   notifier
	bus        eventBus
	logger     zerolog.Logger
}

// NewRunner creates a Runner with the given dependencies.
func NewRunner(
	exec executor,
	jobRepo jobRepo,
	resultRepo jobResultRepo,
	deviceRepo deviceRepo,
	artifacts artifactCollector,
	notifier notifier,
	bus eventBus,
	logger zerolog.Logger,
) *Runner {
	return &Runner{
		exec:       exec,
		jobRepo:    jobRepo,
		resultRepo: resultRepo,
		deviceRepo: deviceRepo,
		artifacts:  artifacts,
		notifier:   notifier,
		bus:        bus,
		logger:     logger,
	}
}

// Run executes all per-device executions concurrently, persists results,
// resets device status, fires notifications, and determines the overall job
// status once all goroutines complete.
//
// Each goroutine has a deferred recover() that ensures the device is released
// and the result is marked 'error' even if a panic occurs.
func (r *Runner) Run(ctx context.Context, job db.Job, executions []*Execution) {
	var (
		mu      sync.Mutex
		results []db.JobResult
		wg      sync.WaitGroup
	)

	for _, exec := range executions {
		wg.Add(1)
		go func(ex *Execution) {
			defer wg.Done()
			result := r.runOne(ctx, job, ex)

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(exec)
	}

	wg.Wait()

	// Determine overall job status: all passed -> 'completed', any failed/error -> 'failed'.
	finalStatus := "completed"
	for _, res := range results {
		if res.Status == "failed" || res.Status == "error" {
			finalStatus = "failed"
			break
		}
	}

	if err := r.jobRepo.SetCompleted(job.ID, time.Now().UTC(), finalStatus); err != nil {
		r.logger.Error().
			Err(err).
			Str("job_id", job.ID).
			Str("status", finalStatus).
			Msg("failed to set job completed")
	}

	r.logger.Info().
		Str("job_id", job.ID).
		Str("status", finalStatus).
		Int("result_count", len(results)).
		Msg("job finished")
}

// runOne runs a single execution and handles all side-effects:
// result persistence, artifact collection, device reset, notification, and
// event publishing. A deferred recover() ensures the device is always released.
func (r *Runner) runOne(ctx context.Context, job db.Job, ex *Execution) (result db.JobResult) {
	// Prepare a fallback result for the panic recovery path.
	result = db.JobResult{
		JobID:    ex.JobID,
		DeviceID: ex.DeviceID,
		Status:   "error",
		ExitCode: -1,
	}

	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Error().
				Str("job_id", ex.JobID).
				Str("device_id", ex.DeviceID).
				Interface("panic", rec).
				Msg("executor goroutine panic recovered")

			// Ensure device is released regardless of panic.
			r.releaseDevice(ex.DeviceID)

			// Persist the error result.
			result.Status = "error"
			result.ExitCode = -1
			if err := r.resultRepo.Create(&result); err != nil {
				r.logger.Error().
					Err(err).
					Str("job_id", ex.JobID).
					Str("device_id", ex.DeviceID).
					Msg("failed to persist panic result")
			}
		}
	}()

	outputCh := make(chan string, 64)
	// Drain outputCh in a goroutine to avoid blocking the executor.
	go func() {
		for range outputCh {
			// Lines are already captured to the log file by the executor.
			// We discard them here unless a consumer is attached.
		}
	}()

	execResult := r.exec.Run(ctx, *ex, outputCh)
	close(outputCh)

	// Determine per-device result status.
	resultStatus := "passed"
	if execResult.ExitCode != 0 || execResult.Error != nil {
		resultStatus = "failed"
	}

	// Collect artifacts from job's artifact_path.
	var artifactsJSON string
	if job.ArtifactPath != "" {
		collected, err := r.artifacts.Collect(ex.JobID, ex.DeviceID, job.ArtifactPath)
		if err != nil {
			r.logger.Warn().
				Err(err).
				Str("job_id", ex.JobID).
				Str("device_id", ex.DeviceID).
				Msg("artifact collection failed")
		} else if len(collected) > 0 {
			data, err := json.Marshal(collected)
			if err != nil {
				r.logger.Warn().
					Err(err).
					Str("job_id", ex.JobID).
					Str("device_id", ex.DeviceID).
					Msg("failed to serialise artifacts")
			} else {
				artifactsJSON = string(data)
			}
		}
	}

	result = db.JobResult{
		JobID:           ex.JobID,
		DeviceID:        ex.DeviceID,
		Status:          resultStatus,
		ExitCode:        execResult.ExitCode,
		DurationSeconds: int(execResult.Duration.Seconds()),
		LogPath:         execResult.LogPath,
		Artifacts:       artifactsJSON,
	}

	// Persist the result.
	if err := r.resultRepo.Create(&result); err != nil {
		r.logger.Error().
			Err(err).
			Str("job_id", ex.JobID).
			Str("device_id", ex.DeviceID).
			Msg("failed to persist job result")
	}

	// Release the device back to 'online'.
	r.releaseDevice(ex.DeviceID)

	// Fire webhook notification (fire-and-forget; failure must not block).
	webhookType := notify.EventJobCompleted
	if resultStatus == "failed" {
		webhookType = notify.EventJobFailed
	}
	r.notifier.Send(notify.WebhookEvent{
		Type:      webhookType,
		Payload:   result,
		Timestamp: time.Now().UTC(),
	})

	// Publish internal event.
	eventType := events.JobCompleted
	if resultStatus == "failed" {
		eventType = events.JobFailed
	}
	r.bus.Publish(events.Event{
		Type:      eventType,
		Payload:   result,
		Timestamp: time.Now().UTC(),
	})

	r.logger.Info().
		Str("job_id", ex.JobID).
		Str("device_id", ex.DeviceID).
		Str("status", resultStatus).
		Int("exit_code", execResult.ExitCode).
		Msg("execution result persisted")

	return result
}

// releaseDevice sets a device's status back to 'online'.
// Errors are logged but do not propagate so that result persistence is not blocked.
func (r *Runner) releaseDevice(deviceID string) {
	if err := r.deviceRepo.UpdateStatus(deviceID, "online"); err != nil {
		r.logger.Error().
			Err(err).
			Str("device_id", deviceID).
			Msg(fmt.Sprintf("failed to reset device %s to online", deviceID))
	}
}

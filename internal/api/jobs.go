package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/job"
)

// jobRepoAPI is the consumer-side interface for db.JobRepository.
type jobRepoAPI interface {
	Create(j *db.Job) error
	FindByID(id string) (db.Job, error)
	FindAll(filter db.JobFilter) ([]db.Job, error)
	UpdateStatus(id, status string) error
	Delete(id string) error
}

// jobResultRepoAPI is the consumer-side interface for db.JobResultRepository.
type jobResultRepoAPI interface {
	FindByJobID(jobID string) ([]db.JobResult, error)
}

// jobSchedulerAPI is the consumer-side interface for job.Scheduler.
type jobSchedulerAPI interface {
	Schedule(j db.Job) ([]*job.Execution, error)
}

// jobRunnerAPI is the consumer-side interface for job.Runner.
type jobRunnerAPI interface {
	Run(ctx context.Context, j db.Job, executions []*job.Execution)
}

// CreateJobRequest is the JSON body accepted by POST /api/v1/jobs.
type CreateJobRequest struct {
	// TestCommand is required — the shell command to run on each device.
	TestCommand    string          `json:"test_command" binding:"required"`
	Strategy       string          `json:"strategy"`
	DeviceFilter   json.RawMessage `json:"device_filter"`
	ArtifactPath   string          `json:"artifact_path"`
	TimeoutMinutes int             `json:"timeout_minutes"`
}

// jobWithResults is the response shape for GET /api/v1/jobs/:id.
type jobWithResults struct {
	db.Job
	Results []db.JobResult `json:"results"`
}

// RegisterJobRoutes registers job CRUD endpoints on the given router group.
func RegisterJobRoutes(rg *gin.RouterGroup, jobRepo jobRepoAPI, resultRepo jobResultRepoAPI, scheduler jobSchedulerAPI, runner jobRunnerAPI) {
	rg.POST("/jobs", createJob(jobRepo, scheduler, runner))
	rg.GET("/jobs", listJobs(jobRepo))
	rg.GET("/jobs/:id", getJob(jobRepo, resultRepo))
	rg.DELETE("/jobs/:id", deleteJob(jobRepo))
}

// createJob returns a handler that creates a new job, schedules it, and
// launches the runner in a goroutine.
func createJob(jobRepo jobRepoAPI, scheduler jobSchedulerAPI, runner jobRunnerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateJobRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "validation failed",
				"fields": gin.H{"test_command": "test_command is required"},
			})
			return
		}

		// Validate strategy — only "" (default fan-out) and "fan-out" are supported.
		if req.Strategy != "" && req.Strategy != "fan-out" {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "unsupported strategy: " + req.Strategy,
			})
			return
		}

		// Build the job record. DeviceFilter is stored as a JSON string.
		deviceFilter := ""
		if len(req.DeviceFilter) > 0 {
			deviceFilter = string(req.DeviceFilter)
		}

		j := db.Job{
			Status:         "queued",
			Strategy:       req.Strategy,
			TestCommand:    req.TestCommand,
			DeviceFilter:   deviceFilter,
			ArtifactPath:   req.ArtifactPath,
			TimeoutMinutes: req.TimeoutMinutes,
		}

		if err := jobRepo.Create(&j); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job"})
			return
		}

		// Schedule the job — if scheduling fails, mark job as failed.
		executions, err := scheduler.Schedule(j)
		if err != nil {
			_ = jobRepo.UpdateStatus(j.ID, "failed") //nolint:errcheck // best-effort update
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Launch the runner as a fire-and-forget goroutine.
		go runner.Run(context.Background(), j, executions)

		c.JSON(http.StatusCreated, j)
	}
}

// listJobs returns a handler that lists jobs with optional ?status= filter,
// sorted by created_at DESC, capped at 100 results.
func listJobs(jobRepo jobRepoAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter := db.JobFilter{
			Status: c.Query("status"),
			Limit:  100,
		}

		jobs, err := jobRepo.FindAll(filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list jobs"})
			return
		}

		// Return an empty array rather than null when there are no results.
		if jobs == nil {
			jobs = []db.Job{}
		}

		c.JSON(http.StatusOK, jobs)
	}
}

// getJob returns a handler that retrieves a single job with its nested results.
func getJob(jobRepo jobRepoAPI, resultRepo jobResultRepoAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		j, err := jobRepo.FindByID(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
			return
		}

		results, err := resultRepo.FindByJobID(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job results"})
			return
		}

		// Return an empty array rather than null when there are no results.
		if results == nil {
			results = []db.JobResult{}
		}

		c.JSON(http.StatusOK, jobWithResults{
			Job:     j,
			Results: results,
		})
	}
}

// deleteJob returns a handler that cancels a job by updating its status to
// "cancelled" and returns HTTP 204.
func deleteJob(jobRepo jobRepoAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		if err := jobRepo.UpdateStatus(id, "cancelled"); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel job"})
			return
		}

		c.Status(http.StatusNoContent)
	}
}

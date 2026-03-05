package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
)

// logJobRepoAPI is the consumer-side interface for job repository used by log endpoints.
type logJobRepoAPI interface {
	FindByID(id string) (db.Job, error)
}

// logJobResultRepoAPI is the consumer-side interface for job result repository used by log endpoints.
type logJobResultRepoAPI interface {
	FindByJobID(jobID string) ([]db.JobResult, error)
}

// logCollectorAPI is the consumer-side interface for job.LogCollector.
type logCollectorAPI interface {
	Read(jobID, deviceID string) (io.ReadCloser, error)
	Tail(ctx context.Context, jobID, deviceID string, ch chan<- string) error
}

// jobStatusResponse is the JSON shape returned by the status endpoint.
type jobStatusResponse struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// RegisterLogRoutes registers job log and status endpoints on the given router group.
// All routes are relative to the group prefix (typically /api/v1).
func RegisterLogRoutes(rg *gin.RouterGroup, jobRepo logJobRepoAPI, resultRepo logJobResultRepoAPI, collector logCollectorAPI) {
	rg.GET("/jobs/:id/status", jobStatus(jobRepo))
	rg.GET("/jobs/:id/logs", streamLogs(jobRepo, resultRepo, collector))
	rg.GET("/jobs/:id/logs/:device_id", deviceLogs(jobRepo, resultRepo, collector))
}

// jobStatus returns a gin.HandlerFunc that responds with a job's status fields.
// Returns HTTP 404 when the job does not exist.
func jobStatus(jobRepo logJobRepoAPI) gin.HandlerFunc {
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

		c.JSON(http.StatusOK, jobStatusResponse{
			ID:          j.ID,
			Status:      j.Status,
			CreatedAt:   j.CreatedAt,
			StartedAt:   j.StartedAt,
			CompletedAt: j.CompletedAt,
		})
	}
}

// streamLogs returns a gin.HandlerFunc that streams job logs as Server-Sent Events.
//
// SSE format:
//
//	data: <log line>\n\n          — for each log line
//	event: done\ndata: {}\n\n    — when streaming ends
//
// The endpoint sets required SSE headers (Content-Type, Cache-Control,
// Connection, X-Accel-Buffering) before writing any events. When the client
// disconnects, the tail goroutine is stopped via context cancellation — no
// goroutine leak occurs.
func streamLogs(jobRepo logJobRepoAPI, resultRepo logJobResultRepoAPI, collector logCollectorAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := c.Param("id")

		j, err := jobRepo.FindByID(jobID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
			return
		}

		// Set SSE headers before writing the body so they are flushed correctly.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// Derive a context from the request so we stop tailing when the client
		// disconnects. The cancel call in the deferred cleanup handles this.
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()

		results, _ := resultRepo.FindByJobID(jobID) //nolint:errcheck // errors surfaced via empty results

		w := c.Writer

		// sendDone writes the terminal SSE event and flushes the response.
		sendDone := func() {
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			w.Flush()
		}

		// If the job is already in a terminal state and there are no device
		// results yet, send the done event immediately so the client doesn't hang.
		if isJobTerminal(j.Status) && len(results) == 0 {
			sendDone()
			return
		}

		// Completed job with results: stream each device log via Read, then done.
		// Read is a full-file read; no tail goroutines needed for terminal jobs.
		if isJobTerminal(j.Status) && len(results) > 0 {
			for _, r := range results {
				rc, err := collector.Read(jobID, r.DeviceID)
				if err != nil {
					// Log file may not exist for some devices; skip gracefully.
					continue
				}
				scanner := bufio.NewScanner(rc)
				for scanner.Scan() {
					fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
					w.Flush()
				}
				rc.Close() //nolint:errcheck
			}
			sendDone()
			return
		}

		// Buffered channel so individual Tail goroutines can write without
		// blocking each other when the loop reads slowly.
		ch := make(chan string, 64)

		// WaitGroup tracks all Tail goroutines. When they all finish, the
		// coordinator goroutine closes ch so the streaming loop exits cleanly.
		var wg sync.WaitGroup

		// Start a Tail goroutine for each device that has already produced a
		// job result entry. Multiple goroutines write to the same channel; the
		// streaming loop below reads from it in FIFO order.
		for _, r := range results {
			wg.Add(1)
			go func(deviceID string) {
				defer wg.Done()
				_ = collector.Tail(ctx, jobID, deviceID, ch) //nolint:errcheck
			}(r.DeviceID)
		}

		// If no results exist yet but the job itself is not complete, start a
		// tail for a synthetic "combined" log path keyed to the job ID itself.
		// This covers the race window between job creation and first device result.
		if len(results) == 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = collector.Tail(ctx, jobID, jobID, ch) //nolint:errcheck
			}()
		}

		// Coordinator: close ch when all Tail goroutines have returned so the
		// streaming loop can exit via the !ok path.
		go func() {
			wg.Wait()
			close(ch)
		}()

		// Stream log lines from the channel until the context is cancelled
		// (client disconnected) or the channel is closed.
		for {
			select {
			case line, ok := <-ch:
				if !ok {
					sendDone()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				w.Flush()
			case <-ctx.Done():
				// Client disconnected; send done so any buffered data is flushed.
				sendDone()
				return
			}
		}
	}
}

// deviceLogs returns a gin.HandlerFunc that streams the log for a single device
// within a job as Server-Sent Events.
//
// The handler:
//  1. Looks up the job by :id — returns 404 if not found.
//  2. Looks up results for the job via resultRepo.FindByJobID, then iterates to
//     find the entry matching :device_id — returns 404 if not found.
//  3. For a terminal job: calls collector.Read, streams all lines as SSE data
//     events, then sends the done event and returns.
//  4. For a running job: calls collector.Tail, streams live lines, exits when
//     the context is cancelled (client disconnected).
func deviceLogs(jobRepo logJobRepoAPI, resultRepo logJobResultRepoAPI, collector logCollectorAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := c.Param("id")
		deviceID := c.Param("device_id")

		j, err := jobRepo.FindByID(jobID)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
			return
		}

		// Find the result row for this specific device.
		results, _ := resultRepo.FindByJobID(jobID) //nolint:errcheck
		found := false
		for _, r := range results {
			if r.DeviceID == deviceID {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "device log not found"})
			return
		}

		// Set SSE headers.
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()

		w := c.Writer

		sendDone := func() {
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			w.Flush()
		}

		// Terminal job: read the whole log file then send done.
		if isJobTerminal(j.Status) {
			rc, err := collector.Read(jobID, deviceID)
			if err == nil {
				scanner := bufio.NewScanner(rc)
				for scanner.Scan() {
					fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
					w.Flush()
				}
				rc.Close() //nolint:errcheck
			}
			sendDone()
			return
		}

		// Running job: tail live lines until context is cancelled.
		ch := make(chan string, 64)
		go func() {
			_ = collector.Tail(ctx, jobID, deviceID, ch) //nolint:errcheck
			close(ch)
		}()

		for {
			select {
			case line, ok := <-ch:
				if !ok {
					sendDone()
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", line)
				w.Flush()
			case <-ctx.Done():
				sendDone()
				return
			}
		}
	}
}

// isJobTerminal reports whether the given job status represents a finished job
// (i.e., one that will produce no further log output).
func isJobTerminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}


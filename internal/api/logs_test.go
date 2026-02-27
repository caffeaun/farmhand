package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// Fakes
// ----------------------------------------------------------------------------

// fakeLogJobRepo is a simple in-memory stub for logJobRepoAPI.
type fakeLogJobRepo struct {
	jobs map[string]db.Job
}

func newFakeLogJobRepo(jobs ...db.Job) *fakeLogJobRepo {
	r := &fakeLogJobRepo{jobs: make(map[string]db.Job)}
	for _, j := range jobs {
		r.jobs[j.ID] = j
	}
	return r
}

func (f *fakeLogJobRepo) FindByID(id string) (db.Job, error) {
	j, ok := f.jobs[id]
	if !ok {
		return db.Job{}, db.ErrNotFound
	}
	return j, nil
}

// fakeLogResultRepo is a simple in-memory stub for logJobResultRepoAPI.
type fakeLogResultRepo struct {
	results map[string][]db.JobResult
}

func newFakeLogResultRepo() *fakeLogResultRepo {
	return &fakeLogResultRepo{results: make(map[string][]db.JobResult)}
}

func (f *fakeLogResultRepo) add(result db.JobResult) {
	f.results[result.JobID] = append(f.results[result.JobID], result)
}

func (f *fakeLogResultRepo) FindByJobID(jobID string) ([]db.JobResult, error) {
	return f.results[jobID], nil
}

// fakeLogCollector sends canned lines to the channel then stops.
// It blocks until the context is cancelled or all lines have been sent.
type fakeLogCollector struct {
	lines []string
}

func (f *fakeLogCollector) Tail(ctx context.Context, jobID, deviceID string, ch chan<- string) error {
	for _, line := range f.lines {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ch <- line:
		}
	}
	// After sending all lines, wait for cancellation so that the streaming
	// loop keeps running (simulates a live tail rather than an abrupt close).
	<-ctx.Done()
	return ctx.Err()
}

// blockingLogCollector never sends anything and blocks until context is done.
type blockingLogCollector struct{}

func (b *blockingLogCollector) Tail(ctx context.Context, jobID, deviceID string, ch chan<- string) error {
	<-ctx.Done()
	return ctx.Err()
}

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// newLogTestRouter creates a gin engine in test mode with the log routes
// registered under /api/v1 without auth so tests stay simple.
func newLogTestRouter(jobRepo logJobRepoAPI, resultRepo logJobResultRepoAPI, collector logCollectorAPI) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/api/v1")
	RegisterLogRoutes(v1, jobRepo, resultRepo, collector)
	return r
}

// makeJob is a convenience constructor for test jobs.
func makeJob(id, status string) db.Job {
	return db.Job{
		ID:        id,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
}

// makeJobWithTimes builds a job that has started and completed timestamps.
func makeJobWithTimes(id, status string) db.Job {
	now := time.Now().UTC()
	started := now.Add(-5 * time.Minute)
	completed := now
	return db.Job{
		ID:          id,
		Status:      status,
		CreatedAt:   now.Add(-10 * time.Minute),
		StartedAt:   &started,
		CompletedAt: &completed,
	}
}

// ----------------------------------------------------------------------------
// jobStatus tests
// ----------------------------------------------------------------------------

// TestJobStatus_Found verifies that GET /api/v1/jobs/:id/status returns HTTP 200
// with the correct JSON fields when the job exists.
func TestJobStatus_Found(t *testing.T) {
	j := makeJobWithTimes("job-abc", "completed")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-abc/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "job-abc", body["id"])
	assert.Equal(t, "completed", body["status"])
	assert.NotEmpty(t, body["created_at"], "created_at must be present")
	assert.NotNil(t, body["started_at"], "started_at must be present for a completed job")
	assert.NotNil(t, body["completed_at"], "completed_at must be present for a completed job")
}

// TestJobStatus_Found_RunningJob verifies that started_at is present but
// completed_at is absent for a running job (omitempty semantics).
func TestJobStatus_Found_RunningJob(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-1 * time.Minute)
	j := db.Job{
		ID:        "job-running",
		Status:    "running",
		CreatedAt: now.Add(-2 * time.Minute),
		StartedAt: &started,
	}
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-running/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, "running", body["status"])
	assert.NotNil(t, body["started_at"])
	// completed_at should be absent due to omitempty.
	_, hasCompleted := body["completed_at"]
	assert.False(t, hasCompleted, "completed_at must not appear for a running job")
}

// TestJobStatus_NotFound verifies that GET /api/v1/jobs/:id/status returns
// HTTP 404 for an unknown job ID.
func TestJobStatus_NotFound(t *testing.T) {
	jobRepo := newFakeLogJobRepo()
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/does-not-exist/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job not found", body["error"])
}

// ----------------------------------------------------------------------------
// streamLogs header tests
// ----------------------------------------------------------------------------

// TestStreamLogs_NotFound verifies that GET /api/v1/jobs/:id/logs returns
// HTTP 404 for an unknown job ID.
func TestStreamLogs_NotFound(t *testing.T) {
	jobRepo := newFakeLogJobRepo()
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/does-not-exist/logs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job not found", body["error"])
}

// TestStreamLogs_Headers verifies that the SSE endpoint sets the required
// headers: Content-Type, Cache-Control, Connection, X-Accel-Buffering.
//
// We use a terminal job with no results so that the handler writes the done
// event and exits immediately, allowing httptest.NewRecorder to capture headers.
func TestStreamLogs_Headers(t *testing.T) {
	j := makeJob("job-done", "completed")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-done/logs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "keep-alive", rec.Header().Get("Connection"))
	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
}

// TestStreamLogs_TerminalJobNoResults verifies that the done SSE event is
// emitted immediately when the job is already complete and has no results.
func TestStreamLogs_TerminalJobNoResults(t *testing.T) {
	j := makeJob("job-terminal", "failed")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-terminal/logs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "event: done")
	assert.Contains(t, body, "data: {}")
}

// TestStreamLogs_SendsLines verifies that log lines emitted by the collector
// appear in the SSE response body as `data: <line>` events.
func TestStreamLogs_SendsLines(t *testing.T) {
	j := makeJob("job-streaming", "running")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	resultRepo.add(db.JobResult{
		ID:       "result-1",
		JobID:    "job-streaming",
		DeviceID: "device-1",
		Status:   "pending",
	})

	lines := []string{"line one", "line two", "line three"}
	collector := &fakeLogCollector{lines: lines}

	r := newLogTestRouter(jobRepo, resultRepo, collector)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-streaming/logs", nil).
		WithContext(ctx)
	rec := httptest.NewRecorder()

	// ServeHTTP blocks until context is cancelled; run synchronously with timeout.
	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, line := range lines {
		assert.Contains(t, body, fmt.Sprintf("data: %s", line))
	}
}

// TestStreamLogs_ClientDisconnect verifies that cancelling the request context
// stops the streaming handler (no goroutine leak).
func TestStreamLogs_ClientDisconnect(t *testing.T) {
	j := makeJob("job-disconnect", "running")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	resultRepo.add(db.JobResult{
		ID:       "result-disc",
		JobID:    "job-disconnect",
		DeviceID: "device-disc",
		Status:   "pending",
	})

	collector := &blockingLogCollector{}
	r := newLogTestRouter(jobRepo, resultRepo, collector)

	// Use a very short timeout as the cancel signal.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-disconnect/logs", nil).
		WithContext(ctx)
	rec := httptest.NewRecorder()

	// ServeHTTP should return once the context expires.
	r.ServeHTTP(rec, req)
	// If we got here, the handler exited — no goroutine leak.
}

// TestStreamLogs_SSEFormat verifies the exact SSE wire format:
//   - log lines are `data: <line>\n\n`
//   - done event is `event: done\ndata: {}\n\n`
func TestStreamLogs_SSEFormat(t *testing.T) {
	j := makeJob("job-format", "running")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	resultRepo.add(db.JobResult{
		ID:       "result-fmt",
		JobID:    "job-format",
		DeviceID: "device-fmt",
		Status:   "pending",
	})

	collector := &fakeLogCollector{lines: []string{"hello world"}}
	r := newLogTestRouter(jobRepo, resultRepo, collector)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-format/logs", nil).
		WithContext(ctx)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	assert.Contains(t, body, "data: hello world\n\n")
	assert.Contains(t, body, "event: done\ndata: {}\n\n")
}

// TestStreamLogs_AuthRequired verifies that the SSE endpoint is protected by
// auth middleware when wired through NewRouter with a token configured.
func TestStreamLogs_AuthRequired(t *testing.T) {
	const token = "secret-token"
	j := makeJob("job-auth", "running")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	// Wire through a test router with auth so the middleware is applied.
	r := gin.New()
	r.Use(authMiddleware(token))
	rg := r.Group("/api/v1")
	RegisterLogRoutes(rg, jobRepo, resultRepo, collector)

	// Request without token.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-auth/logs", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Request with correct token — should attempt SSE (200 or at least not 401).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-auth/logs", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	// Use a cancelable context so the handler doesn't block the test.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req2 = req2.WithContext(ctx)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)

	// The job is "running" with no results; after context timeout the handler
	// exits with a done event. The status code must NOT be 401.
	assert.NotEqual(t, http.StatusUnauthorized, rec2.Code)
}

// TestIsJobTerminal covers the isJobTerminal helper.
func TestIsJobTerminal(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"completed", true},
		{"failed", true},
		{"cancelled", true},
		{"running", false},
		{"queued", false},
		{"preparing", false},
		{"installing", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			assert.Equal(t, tt.want, isJobTerminal(tt.status))
		})
	}
}

// TestJobStatus_FieldSubset verifies that the status response contains only the
// documented subset of job fields (no test_command, strategy, etc.).
func TestJobStatus_FieldSubset(t *testing.T) {
	j := db.Job{
		ID:           "job-subset",
		Status:       "queued",
		Strategy:     "fan-out",
		TestCommand:  "go test ./...",
		DeviceFilter: `{"platform":"android"}`,
		CreatedAt:    time.Now().UTC(),
	}
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	collector := &blockingLogCollector{}

	r := newLogTestRouter(jobRepo, resultRepo, collector)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-subset/status", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	// Must-have fields.
	assert.Contains(t, body, "id")
	assert.Contains(t, body, "status")
	assert.Contains(t, body, "created_at")

	// Must-not-have fields (not part of the status subset).
	assert.NotContains(t, body, "strategy")
	assert.NotContains(t, body, "test_command")
	assert.NotContains(t, body, "device_filter")
	assert.NotContains(t, body, "artifact_path")
	assert.NotContains(t, body, "timeout_minutes")
}

// TestStreamLogs_MultilineSSE verifies that multiple log lines each produce
// a separate SSE data event (one per line).
func TestStreamLogs_MultilineSSE(t *testing.T) {
	j := makeJob("job-multi", "running")
	jobRepo := newFakeLogJobRepo(j)
	resultRepo := newFakeLogResultRepo()
	resultRepo.add(db.JobResult{
		ID:       "result-multi",
		JobID:    "job-multi",
		DeviceID: "device-multi",
		Status:   "pending",
	})

	wantLines := []string{"alpha", "bravo", "charlie"}
	collector := &fakeLogCollector{lines: wantLines}
	r := newLogTestRouter(jobRepo, resultRepo, collector)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/job-multi/logs", nil).
		WithContext(ctx)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	body := rec.Body.String()
	// Each line must appear as its own data event.
	scanner := bufio.NewScanner(strings.NewReader(body))
	dataEvents := 0
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") && scanner.Text() != "data: {}" {
			dataEvents++
		}
	}
	assert.Equal(t, len(wantLines), dataEvents,
		"expected %d data events, one per log line", len(wantLines))
}

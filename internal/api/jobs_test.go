package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----------------------------------------------------------------------------
// Fake implementations
// ----------------------------------------------------------------------------

// fakeJobRepo is a thread-safe in-memory job store.
type fakeJobRepo struct {
	mu   sync.Mutex
	jobs []db.Job
	err  error // if set, all mutating calls return this error
}

func (f *fakeJobRepo) Create(j *db.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if j.ID == "" {
		j.ID = "test-job-id"
	}
	f.jobs = append(f.jobs, *j)
	return nil
}

func (f *fakeJobRepo) FindByID(id string) (db.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return db.Job{}, f.err
	}
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return db.Job{}, db.ErrNotFound
}

func (f *fakeJobRepo) FindAll(filter db.JobFilter) ([]db.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []db.Job
	for _, j := range f.jobs {
		if filter.Status != "" && j.Status != filter.Status {
			continue
		}
		out = append(out, j)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeJobRepo) UpdateStatus(id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs[i].Status = status
			return nil
		}
	}
	return db.ErrNotFound
}

func (f *fakeJobRepo) Delete(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	for i, j := range f.jobs {
		if j.ID == id {
			f.jobs = append(f.jobs[:i], f.jobs[i+1:]...)
			return nil
		}
	}
	return db.ErrNotFound
}

// fakeJobResultRepo returns canned results.
type fakeJobResultRepo struct {
	results []db.JobResult
	err     error
}

func (f *fakeJobResultRepo) FindByJobID(_ string) ([]db.JobResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

// fakeScheduler returns canned executions or an error.
type fakeScheduler struct {
	executions []*job.Execution
	err        error
}

func (f *fakeScheduler) Schedule(_ db.Job) ([]*job.Execution, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.executions, nil
}

// fakeRunner records Run calls.
type fakeRunner struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeRunner) Run(_ context.Context, _ db.Job, _ []*job.Execution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
}

// runnerCalls returns the number of recorded Run calls.
func (f *fakeRunner) runnerCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeCanceller is a hand-written spy that satisfies jobCancellerAPI and
// records Register, Cancel, and Remove calls for assertions in tests.
type fakeCanceller struct {
	mu              sync.Mutex
	registered      map[string]context.CancelFunc
	cancelled       []string
	removed         []string
	effectiveCancels int // incremented when Cancel finds a present entry
}

func newFakeCanceller() *fakeCanceller {
	return &fakeCanceller{registered: make(map[string]context.CancelFunc)}
}

func (f *fakeCanceller) Register(jobID string, cancel context.CancelFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered[jobID] = cancel
}

func (f *fakeCanceller) Cancel(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, jobID)
	cancel, ok := f.registered[jobID]
	if ok {
		f.effectiveCancels++
		delete(f.registered, jobID)
		cancel()
	}
	return ok
}

func (f *fakeCanceller) Remove(jobID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, jobID)
	delete(f.registered, jobID)
}

// Has reports whether jobID is currently registered (not yet cancelled or removed).
func (f *fakeCanceller) Has(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.registered[jobID]
	return ok
}

// effectiveCancelCount returns the number of times Cancel was called and found
// a present entry (i.e. actually invoked the cancel func).
func (f *fakeCanceller) effectiveCancelCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.effectiveCancels
}

// wasRegistered reports whether jobID is currently (or was ever) registered.
// It checks the live map rather than a separate log so it reflects the state
// after a Remove or Cancel has cleaned up.
func (f *fakeCanceller) isRegistered(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.registered[jobID]
	return ok
}

// wasCancelled reports whether Cancel was ever called with jobID.
func (f *fakeCanceller) wasCancelled(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.cancelled {
		if id == jobID {
			return true
		}
	}
	return false
}

// wasRemoved reports whether Remove was ever called with jobID.
func (f *fakeCanceller) wasRemoved(jobID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.removed {
		if id == jobID {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// newJobsRouter builds a minimal Gin engine with job routes registered.
// Pass nil for canceller to test nil-safe behaviour.
func newJobsRouter(
	jobRepo jobRepoAPI,
	resultRepo jobResultRepoAPI,
	scheduler jobSchedulerAPI,
	runner jobRunnerAPI,
	canceller jobCancellerAPI,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	rg := r.Group("/api/v1")
	RegisterJobRoutes(rg, jobRepo, resultRepo, scheduler, runner, canceller)
	return r
}

// doJSONRequest performs a request with an optional JSON body.
func doJSONRequest(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// ----------------------------------------------------------------------------
// createJob tests
// ----------------------------------------------------------------------------

// TestCreateJob_Success verifies that a valid request creates a job and
// returns HTTP 201 with the job JSON (including an ID).
func TestCreateJob_Success(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	runner := &fakeRunner{}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "xcodebuild test",
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body.ID)
	assert.Equal(t, "queued", body.Status)
	assert.Equal(t, "xcodebuild test", body.TestCommand)

	// Runner is launched in a goroutine — give it a moment.
	assert.Eventually(t, func() bool {
		return runner.runnerCalls() == 1
	}, time.Second, 10*time.Millisecond)
}

// TestCreateJob_MissingCommand verifies that omitting test_command returns
// HTTP 422 with a field-level error message.
func TestCreateJob_MissingCommand(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"artifact_path": "/tmp/artifacts",
	})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
	// The response must include field-level information.
	assert.Contains(t, body, "fields")
}

// TestCreateJob_UnsupportedStrategy_Shard verifies that strategy "shard"
// returns HTTP 422 with an "unsupported strategy" error.
func TestCreateJob_UnsupportedStrategy_Shard(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "pytest",
		"strategy":     "shard",
	})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	errMsg, _ := body["error"].(string)
	assert.Contains(t, errMsg, "unsupported strategy")
}

// TestCreateJob_UnsupportedStrategy_Targeted verifies that strategy "targeted"
// also returns HTTP 422.
func TestCreateJob_UnsupportedStrategy_Targeted(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "pytest",
		"strategy":     "targeted",
	})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	errMsg, _ := body["error"].(string)
	assert.Contains(t, errMsg, "unsupported strategy")
}

// TestCreateJob_FanOutStrategy verifies that strategy "fan-out" is accepted.
func TestCreateJob_FanOutStrategy(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "pytest",
		"strategy":     "fan-out",
	})

	assert.Equal(t, http.StatusCreated, rec.Code)
}

// TestCreateJob_SchedulerError verifies that when the scheduler fails, the
// handler returns 500 and marks the job as "failed".
func TestCreateJob_SchedulerError(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{err: errors.New("no online devices match the filter")}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "pytest",
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Job should have been created then marked as "failed".
	require.Len(t, jobRepo.jobs, 1)
	assert.Equal(t, "failed", jobRepo.jobs[0].Status)
}

// ----------------------------------------------------------------------------
// listJobs tests
// ----------------------------------------------------------------------------

// TestListJobs verifies GET /api/v1/jobs returns an array of jobs.
func TestListJobs(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-1", Status: "completed", TestCommand: "pytest"},
			{ID: "job-2", Status: "running", TestCommand: "go test"},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body []jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 2)
}

// TestListJobs_EmptyArray verifies that an empty list returns [] not null.
func TestListJobs_EmptyArray(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs", nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]", string(bytes.TrimSpace(rec.Body.Bytes())))
}

// TestListJobs_FilterStatus verifies ?status=running returns only running jobs.
func TestListJobs_FilterStatus(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-1", Status: "running"},
			{ID: "job-2", Status: "completed"},
			{ID: "job-3", Status: "running"},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs?status=running", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body []jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 2)
	for _, j := range body {
		assert.Equal(t, "running", j.Status)
	}
}

// TestListJobs_MaxResults verifies that at most 100 results are returned.
func TestListJobs_MaxResults(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	// Seed 110 jobs.
	for i := range 110 {
		jobRepo.jobs = append(jobRepo.jobs, db.Job{
			ID:     fmt.Sprintf("job-%d", i),
			Status: "completed",
		})
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body []jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.LessOrEqual(t, len(body), 100)
}

// ----------------------------------------------------------------------------
// getJob tests
// ----------------------------------------------------------------------------

// TestGetJob_WithResults verifies GET /api/v1/jobs/:id returns the job with
// a nested results array.
func TestGetJob_WithResults(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-abc", Status: "completed", TestCommand: "pytest"},
		},
	}
	resultRepo := &fakeJobResultRepo{
		results: []db.JobResult{
			{ID: "result-1", JobID: "job-abc", DeviceID: "device-1", Status: "passed"},
			{ID: "result-2", JobID: "job-abc", DeviceID: "device-2", Status: "failed"},
		},
	}
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-abc", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body jobWithResultsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job-abc", body.ID)
	assert.Equal(t, "completed", body.Status)
	assert.Len(t, body.Results, 2)
}

// TestGetJob_EmptyResults verifies the results field is [] not null when
// there are no results.
func TestGetJob_EmptyResults(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-xyz", Status: "queued", TestCommand: "go test"},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-xyz", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Equal(t, json.RawMessage(`[]`), raw["results"])
}

// TestGetJob_NotFound verifies that GET /api/v1/jobs/:id with an unknown ID
// returns HTTP 404.
func TestGetJob_NotFound(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/does-not-exist", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
}

// ----------------------------------------------------------------------------
// device_filter serialization tests
// ----------------------------------------------------------------------------

// TestCreateJob_DeviceFilter_IsObject verifies that when device_filter is
// provided, the response serializes it as a JSON object not a quoted string.
func TestCreateJob_DeviceFilter_IsObject(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command":  "pytest",
		"device_filter": map[string]interface{}{"platform": "android"},
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	// device_filter must be a JSON object, not a quoted string.
	df, ok := raw["device_filter"]
	require.True(t, ok, "device_filter must be present in response")
	// A quoted string starts with '"'; an object starts with '{'.
	require.NotEqual(t, '"', df[0], "device_filter must be a JSON object, not a quoted string")
	assert.Equal(t, json.RawMessage(`{"platform":"android"}`), df)
}

// TestCreateJob_DeviceFilter_NullWhenEmpty verifies that when device_filter is
// omitted, the response serializes it as null.
func TestCreateJob_DeviceFilter_NullWhenEmpty(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "pytest",
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	df, ok := raw["device_filter"]
	require.True(t, ok, "device_filter must be present in response")
	assert.Equal(t, json.RawMessage("null"), df)
}

// TestListJobs_DeviceFilter_IsObject verifies that GET /api/v1/jobs returns
// device_filter as a JSON object on each item.
func TestListJobs_DeviceFilter_IsObject(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-1", Status: "queued", TestCommand: "pytest", DeviceFilter: `{"platform":"ios"}`},
			{ID: "job-2", Status: "queued", TestCommand: "go test", DeviceFilter: ""},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs", nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var body []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 2)

	// First job: device_filter must be an object.
	df0 := body[0]["device_filter"]
	require.NotEqual(t, '"', df0[0], "device_filter must be a JSON object, not a quoted string")
	assert.Equal(t, json.RawMessage(`{"platform":"ios"}`), df0)

	// Second job: device_filter must be null.
	assert.Equal(t, json.RawMessage("null"), body[1]["device_filter"])
}

// TestGetJob_DeviceFilter_IsObject verifies that GET /api/v1/jobs/:id returns
// device_filter as a JSON object.
func TestGetJob_DeviceFilter_IsObject(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-df", Status: "queued", TestCommand: "pytest", DeviceFilter: `{"platform":"android"}`},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-df", nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	df, ok := raw["device_filter"]
	require.True(t, ok, "device_filter must be present in response")
	require.NotEqual(t, '"', df[0], "device_filter must be a JSON object, not a quoted string")
	assert.Equal(t, json.RawMessage(`{"platform":"android"}`), df)
}

// TestGetJob_DeviceFilter_NullWhenEmpty verifies that GET /api/v1/jobs/:id
// returns null for device_filter when the stored value is empty.
func TestGetJob_DeviceFilter_NullWhenEmpty(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-nodf", Status: "queued", TestCommand: "go test", DeviceFilter: ""},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-nodf", nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	df, ok := raw["device_filter"]
	require.True(t, ok, "device_filter must be present in response")
	assert.Equal(t, json.RawMessage("null"), df)
}

// ----------------------------------------------------------------------------
// error_message serialization tests
// ----------------------------------------------------------------------------

// TestGetJob_ErrorMessage_PresentWhenFailed verifies that GET /api/v1/jobs/:id
// includes a non-empty error_message on a failed result.
func TestGetJob_ErrorMessage_PresentWhenFailed(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-fail", Status: "completed", TestCommand: "pytest"},
		},
	}
	resultRepo := &fakeJobResultRepo{
		results: []db.JobResult{
			{
				ID:           "result-fail",
				JobID:        "job-fail",
				DeviceID:     "device-1",
				Status:       "failed",
				ErrorMessage: "command exited with code 1",
			},
		},
	}
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-fail", nil)

	require.Equal(t, http.StatusOK, rec.Code)

	// Decode the raw JSON to inspect nested result fields.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	var results []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["results"], &results))
	require.Len(t, results, 1)

	errMsgRaw, ok := results[0]["error_message"]
	require.True(t, ok, "error_message field must be present in result object")

	var errMsg string
	require.NoError(t, json.Unmarshal(errMsgRaw, &errMsg))
	assert.Equal(t, "command exited with code 1", errMsg)
}

// TestGetJob_ErrorMessage_EmptyStringWhenPassed verifies that GET /api/v1/jobs/:id
// returns error_message as "" (empty string, not null or absent) for passed results.
func TestGetJob_ErrorMessage_EmptyStringWhenPassed(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-pass", Status: "completed", TestCommand: "pytest"},
		},
	}
	resultRepo := &fakeJobResultRepo{
		results: []db.JobResult{
			{
				ID:           "result-pass",
				JobID:        "job-pass",
				DeviceID:     "device-1",
				Status:       "passed",
				ErrorMessage: "",
			},
		},
	}
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-pass", nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	var results []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["results"], &results))
	require.Len(t, results, 1)

	errMsgRaw, ok := results[0]["error_message"]
	require.True(t, ok, "error_message field must be present even when empty")

	// Must be the empty string, not null and not absent.
	assert.Equal(t, json.RawMessage(`""`), errMsgRaw)
}

// ----------------------------------------------------------------------------
// deleteJob tests
// ----------------------------------------------------------------------------

// TestDeleteJob_Success verifies that DELETE /api/v1/jobs/:id on an existing
// job updates status to "cancelled" and returns HTTP 204.
func TestDeleteJob_Success(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-del", Status: "running"},
		},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-del", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "cancelled", jobRepo.jobs[0].Status)
}

// TestDeleteJob_NotFound verifies that DELETE /api/v1/jobs/:id with an unknown
// ID returns HTTP 404.
func TestDeleteJob_NotFound(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/unknown-id", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
}

// --------------------------------------------------------------------------
// install_command
// --------------------------------------------------------------------------

// TestCreateJob_WithInstallCommand verifies that install_command is accepted
// and returned in the response.
func TestCreateJob_WithInstallCommand(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test", DeviceID: "d1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command":    "adb shell am instrument",
		"install_command": "adb install -r /tmp/app.apk",
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "adb install -r /tmp/app.apk", body.InstallCommand)
	assert.Equal(t, "adb shell am instrument", body.TestCommand)
}

// TestCreateJob_WithoutInstallCommand verifies backward compatibility —
// install_command is omitted from response when empty.
func TestCreateJob_WithoutInstallCommand(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test", DeviceID: "d1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{}, newFakeCanceller())

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo hello",
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	// install_command should be omitted (omitempty) when empty
	_, exists := raw["install_command"]
	assert.False(t, exists, "install_command should be omitted when empty")
}

// TestGetJob_InstallCommand verifies that install_command is included in the
// GET /jobs/:id response.
func TestGetJob_InstallCommand(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	resultRepo := &fakeJobResultRepo{}

	// Create a job with install_command
	j := &db.Job{
		Status:         "queued",
		TestCommand:    "echo test",
		InstallCommand: "curl -o /tmp/app.apk https://example.com/app.apk",
	}
	_ = jobRepo.Create(j)

	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{}, newFakeCanceller())
	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/"+j.ID, nil)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "curl -o /tmp/app.apk https://example.com/app.apk", body["install_command"])
}

// ----------------------------------------------------------------------------
// canceller wiring tests
// ----------------------------------------------------------------------------

// TestCreateJob_RegistersCancelFunc verifies that createJob registers a cancel
// func with the canceller immediately before launching the runner goroutine.
func TestCreateJob_RegistersCancelFunc(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	canceller := newFakeCanceller()
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	runner := &fakeRunner{}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, canceller)

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo hi",
	})

	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.ID)

	// The goroutine runs the real fakeRunner which is synchronous — give it a
	// moment to finish and call Remove, which is the natural-completion cleanup.
	assert.Eventually(t, func() bool {
		return canceller.wasRemoved(body.ID)
	}, time.Second, 10*time.Millisecond, "goroutine should call canceller.Remove after run completes")
}

// TestDeleteJob_CallsCancellAfterStatusUpdate verifies that deleteJob calls
// UpdateStatus first and then signals the canceller (204 path only).
func TestDeleteJob_CallsCanceller(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-running", Status: "running"},
		},
	}
	canceller := newFakeCanceller()
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, canceller)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-running", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "cancelled", jobRepo.jobs[0].Status)
	assert.True(t, canceller.wasCancelled("job-running"), "canceller.Cancel should be called with the job ID")
}

// TestDeleteJob_CancellerNotCalledOnNotFound verifies that the canceller is
// not invoked when the job does not exist (404 path).
func TestDeleteJob_CancellerNotCalledOnNotFound(t *testing.T) {
	canceller := newFakeCanceller()
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, canceller)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/missing", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.False(t, canceller.wasCancelled("missing"), "canceller.Cancel should NOT be called on 404")
}

// TestDeleteJob_Returns204EvenWhenCancelReturnsFalse verifies that deleteJob
// returns 204 regardless of whether the canceller finds an in-flight entry.
func TestDeleteJob_Returns204EvenWhenCancelReturnsFalse(t *testing.T) {
	// The job exists in the repo but has no entry in the canceller (e.g. already
	// completed and the goroutine already called Remove).
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-terminal", Status: "completed"},
		},
	}
	canceller := newFakeCanceller() // no entry registered for "job-terminal"
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, canceller)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-terminal", nil)

	// Must still be 204 — the bool return of Cancel is discarded.
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// ----------------------------------------------------------------------------
// nil-canceller safety tests
// ----------------------------------------------------------------------------

// TestCreateJob_NilCanceller verifies that createJob does not panic when the
// canceller is nil and still returns HTTP 201.
func TestCreateJob_NilCanceller(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	runner := &fakeRunner{}
	// Pass nil explicitly — this exercises the nil-check guards in createJob.
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, nil)

	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo nil-safe",
	})

	assert.Equal(t, http.StatusCreated, rec.Code, "createJob should not panic with nil canceller")

	// Runner still runs.
	assert.Eventually(t, func() bool {
		return runner.runnerCalls() == 1
	}, time.Second, 10*time.Millisecond)
}

// TestDeleteJob_NilCanceller verifies that deleteJob does not panic when the
// canceller is nil and still returns HTTP 204.
func TestDeleteJob_NilCanceller(t *testing.T) {
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-nc", Status: "running"},
		},
	}
	// Pass nil explicitly — this exercises the nil-check guard in deleteJob.
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, nil)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-nc", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code, "deleteJob should not panic with nil canceller")
	assert.Equal(t, "cancelled", jobRepo.jobs[0].Status)
}

// ----------------------------------------------------------------------------
// blockingFakeRunner — runner variant that blocks until the gate is released.
// ----------------------------------------------------------------------------

// blockingFakeRunner is a fakeRunner that blocks inside Run until gate is
// closed (or its context is cancelled). It signals done when Run returns.
type blockingFakeRunner struct {
	gate chan struct{} // close to let Run proceed
	done chan struct{} // closed when Run returns
}

func newBlockingFakeRunner() *blockingFakeRunner {
	return &blockingFakeRunner{
		gate: make(chan struct{}),
		done: make(chan struct{}),
	}
}

func (b *blockingFakeRunner) Run(ctx context.Context, _ db.Job, _ []*job.Execution) {
	defer close(b.done)
	select {
	case <-b.gate:
		// released normally
	case <-ctx.Done():
		// cancelled externally
	}
}

// release opens the gate so Run can return naturally.
func (b *blockingFakeRunner) release() { close(b.gate) }

// waitDone blocks until Run has returned (or the test deadline fires).
func (b *blockingFakeRunner) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-b.done:
	case <-time.After(3 * time.Second):
		t.Fatal("blockingFakeRunner.Run did not return within 3 s")
	}
}

// ----------------------------------------------------------------------------
// Cancel-on-the-fly end-to-end scenarios
// ----------------------------------------------------------------------------

// TestCancelMidRun_KicksRunnerViaRegistry verifies the full cancel path:
//
//  1. POST /jobs → 201; runner goroutine blocks on gate.
//  2. Cancel func is registered with the canceller before the goroutine starts.
//  3. DELETE /jobs/:id → 204; canceller.Cancel is invoked.
//  4. The cancel func closes the runner's ctx; the runner observes ctx.Done()
//     and returns via the gate select.
//  5. After the goroutine exits, canceller.Has(jobID) is false — the deferred
//     Remove fired.
func TestCancelMidRun_KicksRunnerViaRegistry(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	canceller := newFakeCanceller()
	runner := newBlockingFakeRunner()
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, canceller)

	// Step 1: create the job.
	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo block",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	jobID := body.ID
	require.NotEmpty(t, jobID)

	// Step 2: wait until the cancel func is registered with the canceller.
	// The handler registers before launching the goroutine, but we wait with
	// Eventually to avoid a race with goroutine scheduling.
	assert.Eventually(t, func() bool {
		return canceller.Has(jobID)
	}, time.Second, 5*time.Millisecond, "cancel func should be registered before goroutine runs")

	// Step 3: cancel the job via DELETE.
	delRec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/"+jobID, nil)
	require.Equal(t, http.StatusNoContent, delRec.Code)

	// Canceller.Cancel was invoked.
	assert.True(t, canceller.wasCancelled(jobID), "canceller.Cancel should be called with job ID")

	// Step 4: the runner observed ctx.Done() and returned; wait for it.
	runner.waitDone(t)

	// Step 5: after the goroutine exits, the deferred Remove should have fired.
	assert.False(t, canceller.Has(jobID), "canceller.Has should be false after goroutine exits")
}

// TestNaturalCompletion_Cleanup verifies that when the runner finishes without
// cancellation, the deferred Remove in the goroutine fires and
// canceller.Has(jobID) becomes false.
func TestNaturalCompletion_Cleanup(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	canceller := newFakeCanceller()
	runner := newBlockingFakeRunner()
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, canceller)

	// Create the job — the goroutine starts and blocks on the gate.
	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo complete",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	jobID := body.ID
	require.NotEmpty(t, jobID)

	// Wait for registration so we know the goroutine is alive.
	assert.Eventually(t, func() bool {
		return canceller.Has(jobID)
	}, time.Second, 5*time.Millisecond, "cancel func should be registered")

	// Release the gate — the runner returns naturally without cancellation.
	runner.release()
	runner.waitDone(t)

	// After natural completion, Remove should have been deferred.
	assert.False(t, canceller.Has(jobID), "canceller.Has should be false after natural completion")
	assert.False(t, canceller.wasCancelled(jobID), "Cancel should NOT have been called on natural completion")
}

// TestDoubleCancel_Safety verifies that two concurrent DELETE requests both
// return 204 but only one of them triggers an effective cancel (i.e. actually
// invokes the registered cancel func).
func TestDoubleCancel_Safety(t *testing.T) {
	jobRepo := &fakeJobRepo{}
	canceller := newFakeCanceller()
	runner := newBlockingFakeRunner()
	scheduler := &fakeScheduler{
		executions: []*job.Execution{{JobID: "test-job-id", DeviceID: "device-1"}},
	}
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner, canceller)

	// Create the job.
	rec := doJSONRequest(r, http.MethodPost, "/api/v1/jobs", map[string]interface{}{
		"test_command": "echo double",
	})
	require.Equal(t, http.StatusCreated, rec.Code)

	var body jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	jobID := body.ID
	require.NotEmpty(t, jobID)

	// Wait until the cancel func is registered.
	assert.Eventually(t, func() bool {
		return canceller.Has(jobID)
	}, time.Second, 5*time.Millisecond)

	// Fire two concurrent DELETE requests.
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			delRec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/"+jobID, nil)
			codes[i] = delRec.Code
		}()
	}
	wg.Wait()

	// Both must return 204.
	assert.Equal(t, http.StatusNoContent, codes[0], "first DELETE should return 204")
	assert.Equal(t, http.StatusNoContent, codes[1], "second DELETE should return 204")

	// Only one of the two Cancel calls should have found a present entry.
	assert.Equal(t, 1, canceller.effectiveCancelCount(),
		"cancel func should be invoked exactly once regardless of concurrent DELETEs")

	// Let the goroutine drain.
	runner.waitDone(t)
}

// TestCancelTerminal_Idempotency verifies that deleting a job that is already
// in a terminal state (no cancel func registered) returns 204 without panicking.
// The canceller's Cancel is called but returns false, which is discarded.
func TestCancelTerminal_Idempotency(t *testing.T) {
	// Seed a completed job directly in the repo — no runner goroutine running.
	jobRepo := &fakeJobRepo{
		jobs: []db.Job{
			{ID: "job-done", Status: "completed"},
		},
	}
	canceller := newFakeCanceller() // no entry registered for "job-done"
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, canceller)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-done", nil)

	// 204 even though the job was already terminal.
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Cancel was called...
	assert.True(t, canceller.wasCancelled("job-done"), "Cancel should still be called")
	// ...but it found nothing, so no effective cancel.
	assert.Equal(t, 0, canceller.effectiveCancelCount(),
		"no cancel func was registered, so effective cancel count should be 0")
}

// TestCancelUnknownID_Returns404 verifies that DELETE /jobs/:id with an id
// that the repo does not know returns 404 and does NOT call canceller.Cancel.
func TestCancelUnknownID_Returns404(t *testing.T) {
	canceller := newFakeCanceller()
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{}, canceller)

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/no-such-job", nil)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job not found", body["error"])

	// The canceller must NOT be invoked on a 404 path.
	assert.False(t, canceller.wasCancelled("no-such-job"),
		"canceller.Cancel should NOT be called when the job is not found")
}

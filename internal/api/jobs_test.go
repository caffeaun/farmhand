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

// ----------------------------------------------------------------------------
// Test helpers
// ----------------------------------------------------------------------------

// newJobsRouter builds a minimal Gin engine with job routes registered.
func newJobsRouter(
	jobRepo jobRepoAPI,
	resultRepo jobResultRepoAPI,
	scheduler jobSchedulerAPI,
	runner jobRunnerAPI,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	rg := r.Group("/api/v1")
	RegisterJobRoutes(rg, jobRepo, resultRepo, scheduler, runner)
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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, runner)

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
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var body []jobResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 2)
}

// TestListJobs_EmptyArray verifies that an empty list returns [] not null.
func TestListJobs_EmptyArray(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

	rec := doJSONRequest(r, http.MethodGet, "/api/v1/jobs/job-xyz", nil)

	assert.Equal(t, http.StatusOK, rec.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Equal(t, json.RawMessage(`[]`), raw["results"])
}

// TestGetJob_NotFound verifies that GET /api/v1/jobs/:id with an unknown ID
// returns HTTP 404.
func TestGetJob_NotFound(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, scheduler, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, resultRepo, &fakeScheduler{}, &fakeRunner{})

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
	r := newJobsRouter(jobRepo, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/job-del", nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "cancelled", jobRepo.jobs[0].Status)
}

// TestDeleteJob_NotFound verifies that DELETE /api/v1/jobs/:id with an unknown
// ID returns HTTP 404.
func TestDeleteJob_NotFound(t *testing.T) {
	r := newJobsRouter(&fakeJobRepo{}, &fakeJobResultRepo{}, &fakeScheduler{}, &fakeRunner{})

	rec := doJSONRequest(r, http.MethodDelete, "/api/v1/jobs/unknown-id", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "error")
}

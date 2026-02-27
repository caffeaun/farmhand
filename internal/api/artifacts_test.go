package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

// fakeArtifactJobRepo implements artifactJobRepoAPI.
type fakeArtifactJobRepo struct {
	jobs map[string]db.Job
}

func newFakeArtifactJobRepo(jobs ...db.Job) *fakeArtifactJobRepo {
	r := &fakeArtifactJobRepo{jobs: make(map[string]db.Job)}
	for _, j := range jobs {
		r.jobs[j.ID] = j
	}
	return r
}

func (r *fakeArtifactJobRepo) FindByID(id string) (db.Job, error) {
	j, ok := r.jobs[id]
	if !ok {
		return db.Job{}, db.ErrNotFound
	}
	return j, nil
}

// fakeArtifactResultRepo implements artifactResultRepoAPI.
type fakeArtifactResultRepo struct {
	results map[string][]db.JobResult
}

func newFakeArtifactResultRepo() *fakeArtifactResultRepo {
	return &fakeArtifactResultRepo{results: make(map[string][]db.JobResult)}
}

func (r *fakeArtifactResultRepo) addResult(result db.JobResult) {
	r.results[result.JobID] = append(r.results[result.JobID], result)
}

func (r *fakeArtifactResultRepo) FindByJobID(jobID string) ([]db.JobResult, error) {
	return r.results[jobID], nil
}

// fakeArtifactCollector implements artifactCollectorAPI.
type fakeArtifactCollector struct {
	// artifacts maps "jobID/deviceID" to a slice of artifacts.
	artifacts map[string][]job.Artifact
	// fileContents maps artifact path to file bytes.
	fileContents map[string][]byte
}

func newFakeArtifactCollector() *fakeArtifactCollector {
	return &fakeArtifactCollector{
		artifacts:    make(map[string][]job.Artifact),
		fileContents: make(map[string][]byte),
	}
}

func (c *fakeArtifactCollector) addArtifact(jobID, deviceID string, a job.Artifact, content []byte) {
	key := jobID + "/" + deviceID
	c.artifacts[key] = append(c.artifacts[key], a)
	c.fileContents[a.Path] = content
}

func (c *fakeArtifactCollector) List(jobID, deviceID string) ([]job.Artifact, error) {
	return c.artifacts[jobID+"/"+deviceID], nil
}

func (c *fakeArtifactCollector) ReadArtifact(path string) (io.ReadCloser, error) {
	content, ok := c.fileContents[path]
	if !ok {
		return nil, io.ErrUnexpectedEOF
	}
	return io.NopCloser(bytes.NewReader(content)), nil
}

// --- helpers ---

// newArtifactTestEngine builds a minimal Gin engine with artifact routes registered.
func newArtifactTestEngine(
	jobRepo artifactJobRepoAPI,
	resultRepo artifactResultRepoAPI,
	collector artifactCollectorAPI,
) *gin.Engine {
	r := gin.New()
	rg := r.Group("/api/v1")
	RegisterArtifactRoutes(rg, jobRepo, resultRepo, collector)
	return r
}

// --- tests ---

// TestListArtifacts_Success verifies that GET /api/v1/jobs/:id/artifacts returns
// a JSON array containing filename, size_bytes, and mime_type for each artifact.
func TestListArtifacts_Success(t *testing.T) {
	const jobID = "job-1"
	const deviceID = "device-1"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	resultRepo.addResult(db.JobResult{JobID: jobID, DeviceID: deviceID})
	collector := newFakeArtifactCollector()
	collector.addArtifact(jobID, deviceID, job.Artifact{
		Filename:  "results.xml",
		Path:      "/artifacts/job-1/device-1/results.xml",
		SizeBytes: 512,
		MimeType:  "application/xml",
	}, []byte("<xml/>"))

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, "results.xml", body[0]["filename"])
	assert.Equal(t, float64(512), body[0]["size_bytes"])
	assert.Equal(t, "application/xml", body[0]["mime_type"])
}

// TestListArtifacts_JobNotFound verifies that listing artifacts for an unknown
// job returns HTTP 404.
func TestListArtifacts_JobNotFound(t *testing.T) {
	jobRepo := newFakeArtifactJobRepo() // empty
	resultRepo := newFakeArtifactResultRepo()
	collector := newFakeArtifactCollector()

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/artifacts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job not found", body["error"])
}

// TestListArtifacts_Empty verifies that a job with no artifacts returns an
// empty JSON array (not null).
func TestListArtifacts_Empty(t *testing.T) {
	const jobID = "job-empty"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	// No results means no artifact directories to scan.
	collector := newFakeArtifactCollector()

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body []interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Empty(t, body)
}

// TestDownloadArtifact_Success verifies that downloading an artifact streams
// the file bytes, sets Content-Type from the artifact's mime_type, and sets
// Content-Disposition: attachment with the correct filename.
func TestDownloadArtifact_Success(t *testing.T) {
	const jobID = "job-dl"
	const deviceID = "dev-dl"
	const content = "test log output"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	resultRepo.addResult(db.JobResult{JobID: jobID, DeviceID: deviceID})
	collector := newFakeArtifactCollector()
	collector.addArtifact(jobID, deviceID, job.Artifact{
		Filename:  "output.log",
		Path:      "/artifacts/job-dl/dev-dl/output.log",
		SizeBytes: int64(len(content)),
		MimeType:  "text/plain",
	}, []byte(content))

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts/output.log", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "output.log")
	assert.Equal(t, content, rec.Body.String())
}

// TestDownloadArtifact_PathTraversal verifies that a filename containing ".."
// is rejected with HTTP 400.
func TestDownloadArtifact_PathTraversal(t *testing.T) {
	const jobID = "job-pt"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	collector := newFakeArtifactCollector()

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	traversalCases := []struct {
		name     string
		filename string
	}{
		{name: "double dot", filename: "../etc/passwd"},
		{name: "nested traversal", filename: "subdir/../../etc/passwd"},
	}

	for _, tc := range traversalCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts/"+tc.filename, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var body map[string]interface{}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, "invalid filename", body["error"])
		})
	}
}

// TestDownloadArtifact_EncodedTraversal verifies that URL-encoded path
// traversal patterns (%2F, %2E%2E%2F) are rejected with HTTP 400.
func TestDownloadArtifact_EncodedTraversal(t *testing.T) {
	const jobID = "job-enc"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	collector := newFakeArtifactCollector()

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	// These are injected as literal strings in the URL path. Gin decodes percent
	// encoding before routing, so the handler receives the decoded form. However
	// our check covers both the decoded form (caught by the ".." check above) and
	// any residual encoded sequences that slip through without full decoding.
	encodedCases := []struct {
		name     string
		filename string
	}{
		{name: "encoded slash", filename: "%2Fetc%2Fpasswd"},
		{name: "encoded dot dot slash", filename: "%2E%2E%2Fetc%2Fpasswd"},
		{name: "mixed encoded", filename: "sub%2F..%2Fetc"},
	}

	for _, tc := range encodedCases {
		t.Run(tc.name, func(t *testing.T) {
			// Build the request with the raw URL so Gin receives the encoded form.
			req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts/"+tc.filename, nil)
			req.URL.RawPath = "/api/v1/jobs/" + jobID + "/artifacts/" + tc.filename
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestDownloadArtifact_JobNotFound verifies that downloading an artifact for an
// unknown job returns HTTP 404.
func TestDownloadArtifact_JobNotFound(t *testing.T) {
	jobRepo := newFakeArtifactJobRepo() // empty
	resultRepo := newFakeArtifactResultRepo()
	collector := newFakeArtifactCollector()

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/nonexistent/artifacts/file.log", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "job not found", body["error"])
}

// TestDownloadArtifact_NotFound verifies that requesting a non-existent artifact
// filename returns HTTP 404.
func TestDownloadArtifact_NotFound(t *testing.T) {
	const jobID = "job-miss"
	const deviceID = "dev-miss"

	jobRepo := newFakeArtifactJobRepo(db.Job{ID: jobID})
	resultRepo := newFakeArtifactResultRepo()
	resultRepo.addResult(db.JobResult{JobID: jobID, DeviceID: deviceID})
	collector := newFakeArtifactCollector()
	// No artifacts registered for this job/device.

	r := newArtifactTestEngine(jobRepo, resultRepo, collector)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID+"/artifacts/missing.xml", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "artifact not found", body["error"])
}

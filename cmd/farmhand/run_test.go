package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newJobServer creates a test HTTP server that handles job submission and
// related endpoints according to the supplied scenario.
//
// submitStatus is the HTTP status returned for POST /api/v1/jobs.
// submitJobID is the job ID embedded in the 201 response body.
// statusValue is the value returned by GET /api/v1/jobs/:id/status.
// logLines are SSE "data:" lines streamed before the "event: done" event.
func newJobServer(
	t *testing.T,
	submitStatus int,
	submitJobID string,
	statusValue string,
	logLines []string,
) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/jobs":
			if submitStatus != http.StatusCreated {
				w.WriteHeader(submitStatus)
				fmt.Fprintf(w, `{"error":"server error"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":%q}`, submitJobID)

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)

			for _, line := range logLines {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			// Terminate the stream with the done event.
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/status"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":%q,"status":%q}`, submitJobID, statusValue)

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAuthJobServer starts a job server that requires a specific bearer token,
// returning 401 for any request that omits or provides the wrong token.
func newAuthJobServer(t *testing.T, wantToken, jobID string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":"unauthorized"}`)
			return
		}

		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":%q}`, jobID)
			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --------------------------------------------------------------------------
// doSubmitJob
// --------------------------------------------------------------------------

func TestDoSubmitJob_Success(t *testing.T) {
	srv := newJobServer(t, http.StatusCreated, "job-abc", "completed", nil)

	id, err := doSubmitJob(srv.URL, "", "flutter test", "", nil, 30)
	require.NoError(t, err)
	assert.Equal(t, "job-abc", id)
}

func TestDoSubmitJob_WithPlatformAndTags(t *testing.T) {
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody = make([]byte, r.ContentLength)
		_, _ = r.Body.Read(capturedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"job-xyz"}`)
	}))
	t.Cleanup(srv.Close)

	id, err := doSubmitJob(srv.URL, "", "echo hello", "android", []string{"team-a", "ci"}, 10)
	require.NoError(t, err)
	assert.Equal(t, "job-xyz", id)

	var body struct {
		TestCommand    string `json:"test_command"`
		DeviceFilter   struct {
			Platform string   `json:"platform"`
			Tags     []string `json:"tags"`
		} `json:"device_filter"`
		TimeoutMinutes int `json:"timeout_minutes"`
	}
	require.NoError(t, json.Unmarshal(capturedBody, &body))
	assert.Equal(t, "echo hello", body.TestCommand)
	assert.Equal(t, "android", body.DeviceFilter.Platform)
	assert.Equal(t, []string{"team-a", "ci"}, body.DeviceFilter.Tags)
	assert.Equal(t, 10, body.TimeoutMinutes)
}

func TestDoSubmitJob_WithToken(t *testing.T) {
	srv := newAuthJobServer(t, "mytoken", "job-auth")

	id, err := doSubmitJob(srv.URL, "mytoken", "go test ./...", "", nil, 30)
	require.NoError(t, err)
	assert.Equal(t, "job-auth", id)
}

func TestDoSubmitJob_UnauthorizedWithoutToken(t *testing.T) {
	srv := newAuthJobServer(t, "secret", "job-auth")

	_, err := doSubmitJob(srv.URL, "", "go test ./...", "", nil, 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
}

func TestDoSubmitJob_ServerError(t *testing.T) {
	srv := newJobServer(t, http.StatusInternalServerError, "", "", nil)

	_, err := doSubmitJob(srv.URL, "", "flutter test", "", nil, 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestDoSubmitJob_ServerUnreachable(t *testing.T) {
	_, err := doSubmitJob("http://127.0.0.1:19999", "", "flutter test", "", nil, 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

func TestDoSubmitJob_EmptyID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":""}`)
	}))
	t.Cleanup(srv.Close)

	_, err := doSubmitJob(srv.URL, "", "flutter test", "", nil, 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty job ID")
}

// --------------------------------------------------------------------------
// doStreamLogs
// --------------------------------------------------------------------------

func TestDoStreamLogs_ReceivesDoneEvent(t *testing.T) {
	lines := []string{"Starting tests", "Test 1 passed", "Test 2 passed"}
	srv := newJobServer(t, http.StatusCreated, "job-1", "completed", lines)

	done, err := doStreamLogs(srv.URL, "", "job-1")
	require.NoError(t, err)
	assert.True(t, done, "expected done=true when server sends event: done")
}

func TestDoStreamLogs_EmptyStream(t *testing.T) {
	// Server sends the done event with no log lines before it.
	srv := newJobServer(t, http.StatusCreated, "job-2", "completed", nil)

	done, err := doStreamLogs(srv.URL, "", "job-2")
	require.NoError(t, err)
	assert.True(t, done, "expected done=true even when no log lines precede the done event")
}

func TestDoStreamLogs_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := doStreamLogs(srv.URL, "", "missing-job")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestDoStreamLogs_WithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer logtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
	}))
	t.Cleanup(srv.Close)

	done, err := doStreamLogs(srv.URL, "logtoken", "job-tok")
	require.NoError(t, err)
	assert.True(t, done)
}

// --------------------------------------------------------------------------
// doFetchStatus
// --------------------------------------------------------------------------

func TestDoFetchStatus_Completed(t *testing.T) {
	srv := newJobServer(t, http.StatusCreated, "job-s1", "completed", nil)

	status, err := doFetchStatus(srv.URL, "", "job-s1")
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
}

func TestDoFetchStatus_Failed(t *testing.T) {
	srv := newJobServer(t, http.StatusCreated, "job-s2", "failed", nil)

	status, err := doFetchStatus(srv.URL, "", "job-s2")
	require.NoError(t, err)
	assert.Equal(t, "failed", status)
}

func TestDoFetchStatus_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := doFetchStatus(srv.URL, "", "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestDoFetchStatus_WithToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer statustoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"job-t","status":"completed"}`)
	}))
	t.Cleanup(srv.Close)

	status, err := doFetchStatus(srv.URL, "statustoken", "job-t")
	require.NoError(t, err)
	assert.Equal(t, "completed", status)
}

func TestDoFetchStatus_ServerUnreachable(t *testing.T) {
	_, err := doFetchStatus("http://127.0.0.1:19999", "", "job-x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

// --------------------------------------------------------------------------
// addRunAuthHeader
// --------------------------------------------------------------------------

func TestAddRunAuthHeader_WithToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	addRunAuthHeader(req, "tok123")
	assert.Equal(t, "Bearer tok123", req.Header.Get("Authorization"))
}

func TestAddRunAuthHeader_EmptyToken(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	addRunAuthHeader(req, "")
	assert.Empty(t, req.Header.Get("Authorization"))
}

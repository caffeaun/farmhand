package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/config"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeStatsDeviceRepo struct {
	devices []db.Device
}

func (f *fakeStatsDeviceRepo) FindAll(_ db.DeviceFilter) ([]db.Device, error) {
	return f.devices, nil
}

type fakeStatsJobRepo struct {
	jobs []db.Job
}

func (f *fakeStatsJobRepo) FindAll(_ db.JobFilter) ([]db.Job, error) {
	return f.jobs, nil
}

// --- helpers ---

func newSystemTestRouter(cfg *config.Config, deviceRepo statsDeviceRepoAPI, jobRepo statsJobRepoAPI) *gin.Engine {
	r := gin.New()
	rg := r.Group("/api/v1")
	RegisterSystemRoutes(rg, cfg, deviceRepo, jobRepo)
	return r
}

// --- tests ---

// TestGetConfig_MaskedToken verifies that auth_token is replaced by "***".
func TestGetConfig_MaskedToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.AuthToken = "super-secret"
	cfg.Server.Port = 8080

	r := newSystemTestRouter(cfg, &fakeStatsDeviceRepo{}, &fakeStatsJobRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	server, ok := body["server"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "***", server["auth_token"])
}

// TestGetConfig_EmptyToken verifies that an empty token stays empty.
func TestGetConfig_EmptyToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.AuthToken = ""

	r := newSystemTestRouter(cfg, &fakeStatsDeviceRepo{}, &fakeStatsJobRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	server, ok := body["server"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "", server["auth_token"])
}

// TestGetStats_Counts verifies correct per-status counts.
func TestGetStats_Counts(t *testing.T) {
	deviceRepo := &fakeStatsDeviceRepo{
		devices: []db.Device{
			{ID: "d1", Status: "online"},
			{ID: "d2", Status: "online"},
			{ID: "d3", Status: "offline"},
			{ID: "d4", Status: "busy"},
		},
	}
	jobRepo := &fakeStatsJobRepo{
		jobs: []db.Job{
			{ID: "j1", Status: "queued"},
			{ID: "j2", Status: "running"},
			{ID: "j3", Status: "completed"},
			{ID: "j4", Status: "completed"},
			{ID: "j5", Status: "failed"},
		},
	}

	r := newSystemTestRouter(&config.Config{}, deviceRepo, jobRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body StatsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, 4, body.Devices.Total)
	assert.Equal(t, 2, body.Devices.Online)
	assert.Equal(t, 1, body.Devices.Offline)
	assert.Equal(t, 1, body.Devices.Busy)

	assert.Equal(t, 5, body.Jobs.Total)
	assert.Equal(t, 1, body.Jobs.Queued)
	assert.Equal(t, 1, body.Jobs.Running)
	assert.Equal(t, 2, body.Jobs.Completed)
	assert.Equal(t, 1, body.Jobs.Failed)
}

// TestGetStats_EmptyDB verifies that an empty database returns all zeros.
func TestGetStats_EmptyDB(t *testing.T) {
	r := newSystemTestRouter(&config.Config{}, &fakeStatsDeviceRepo{}, &fakeStatsJobRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body StatsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, 0, body.Devices.Total)
	assert.Equal(t, 0, body.Devices.Online)
	assert.Equal(t, 0, body.Devices.Offline)
	assert.Equal(t, 0, body.Devices.Busy)
	assert.Equal(t, 0, body.Jobs.Total)
}

// TestGetStats_DeviceCountsSum verifies total == online + offline + busy.
func TestGetStats_DeviceCountsSum(t *testing.T) {
	deviceRepo := &fakeStatsDeviceRepo{
		devices: []db.Device{
			{ID: "d1", Status: "online"},
			{ID: "d2", Status: "offline"},
			{ID: "d3", Status: "busy"},
			{ID: "d4", Status: "online"},
			{ID: "d5", Status: "busy"},
		},
	}

	r := newSystemTestRouter(&config.Config{}, deviceRepo, &fakeStatsJobRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body StatsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	sum := body.Devices.Online + body.Devices.Offline + body.Devices.Busy
	assert.Equal(t, body.Devices.Total, sum, "total should equal online + offline + busy")
}

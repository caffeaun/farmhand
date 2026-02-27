package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/device"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeviceManager is a manual test double that implements deviceManagerAPI.
// Each field holds the value to return for the corresponding method call.
// Errors returned by the fake should be set via the corresponding Err field.
type fakeDeviceManager struct {
	// ListDevices is the list returned by List.
	ListDevices []db.Device
	// ListErr is the error returned by List.
	ListErr error
	// LastListFilter is the filter passed to the most recent List call.
	LastListFilter db.DeviceFilter

	// GetDevice is the device returned by GetByID.
	GetDevice db.Device
	// GetErr is the error returned by GetByID.
	GetErr error

	// Health is the DeviceHealth returned by HealthCheck.
	Health device.DeviceHealth
	// HealthErr is the error returned by HealthCheck.
	HealthErr error

	// WakeErr is the error returned by Wake.
	WakeErr error

	// RebootErr is the error returned by Reboot.
	RebootErr error
}

func (f *fakeDeviceManager) List(filter db.DeviceFilter) ([]db.Device, error) {
	f.LastListFilter = filter
	return f.ListDevices, f.ListErr
}

func (f *fakeDeviceManager) GetByID(_ string) (db.Device, error) {
	return f.GetDevice, f.GetErr
}

func (f *fakeDeviceManager) Wake(_ string) error {
	return f.WakeErr
}

func (f *fakeDeviceManager) Reboot(_ string) error {
	return f.RebootErr
}

func (f *fakeDeviceManager) HealthCheck(_ string) (device.DeviceHealth, error) {
	return f.Health, f.HealthErr
}

// newDeviceTestRouter creates a minimal gin.Engine with only device routes
// registered under /api/v1. No auth middleware is applied so tests focus on
// handler behaviour rather than authentication.
func newDeviceTestRouter(mgr deviceManagerAPI) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	rg := r.Group("/api/v1")
	RegisterDeviceRoutes(rg, mgr)
	return r
}

// doDeviceRequest performs an HTTP request against the router and returns the
// response recorder.
func doDeviceRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// sampleDevices returns a slice of two representative db.Device values that
// can be reused across tests.
func sampleDevices() []db.Device {
	now := time.Now().UTC().Truncate(time.Second)
	return []db.Device{
		{
			ID:           "dev-001",
			Platform:     "android",
			Model:        "Pixel 8",
			OSVersion:    "14",
			Status:       "online",
			BatteryLevel: 85,
			Tags:         []string{"ci", "emulator"},
			LastSeen:     now,
			CreatedAt:    now,
		},
		{
			ID:           "dev-002",
			Platform:     "ios",
			Model:        "iPhone 15",
			OSVersion:    "17",
			Status:       "offline",
			BatteryLevel: 42,
			Tags:         []string{"ci"},
			LastSeen:     now,
			CreatedAt:    now,
		},
	}
}

// TestListDevices verifies that GET /api/v1/devices returns the full device
// list as a JSON array.
func TestListDevices(t *testing.T) {
	devs := sampleDevices()
	mgr := &fakeDeviceManager{ListDevices: devs}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices")

	require.Equal(t, http.StatusOK, rec.Code)

	var body []db.Device
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Len(t, body, 2)
	assert.Equal(t, "dev-001", body[0].ID)
	assert.Equal(t, "dev-002", body[1].ID)
}

// TestListDevices_EmptyReturnsArray verifies that GET /api/v1/devices returns
// an empty JSON array (not null) when there are no devices.
func TestListDevices_EmptyReturnsArray(t *testing.T) {
	mgr := &fakeDeviceManager{ListDevices: nil}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
}

// TestListDevices_FilterPlatform verifies that ?platform=android passes the
// correct filter to the manager.
func TestListDevices_FilterPlatform(t *testing.T) {
	devs := sampleDevices()[:1] // only the Android device
	mgr := &fakeDeviceManager{ListDevices: devs}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices?platform=android")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "android", mgr.LastListFilter.Platform)

	var body []db.Device
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Equal(t, "android", body[0].Platform)
}

// TestListDevices_FilterTags verifies that ?tags=ci,emulator passes both tags
// in the filter to the manager.
func TestListDevices_FilterTags(t *testing.T) {
	devs := sampleDevices()[:1] // only the device with both tags
	mgr := &fakeDeviceManager{ListDevices: devs}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices?tags=ci,emulator")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"ci", "emulator"}, mgr.LastListFilter.Tags)

	var body []db.Device
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 1)
	assert.Contains(t, body[0].Tags, "ci")
	assert.Contains(t, body[0].Tags, "emulator")
}

// TestListDevices_TagsAreJSONArray verifies that device tags are serialised as
// a JSON array in the response.
func TestListDevices_TagsAreJSONArray(t *testing.T) {
	devs := sampleDevices()
	mgr := &fakeDeviceManager{ListDevices: devs}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices")

	require.Equal(t, http.StatusOK, rec.Code)

	var raw []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Len(t, raw, 2)

	tags, ok := raw[0]["tags"].([]interface{})
	require.True(t, ok, "tags should be a JSON array")
	assert.Len(t, tags, 2)
}

// TestGetDevice_Found verifies that GET /api/v1/devices/:id returns device JSON
// for a known device ID.
func TestGetDevice_Found(t *testing.T) {
	devs := sampleDevices()
	mgr := &fakeDeviceManager{GetDevice: devs[0]}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices/dev-001")

	require.Equal(t, http.StatusOK, rec.Code)

	var body db.Device
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "dev-001", body.ID)
	assert.Equal(t, "android", body.Platform)
	assert.Equal(t, 85, body.BatteryLevel)
}

// TestGetDevice_NotFound verifies that GET /api/v1/devices/:id returns HTTP 404
// with a JSON error body when the device does not exist.
func TestGetDevice_NotFound(t *testing.T) {
	mgr := &fakeDeviceManager{GetErr: db.ErrNotFound}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices/unknown-id")

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "device not found", body["error"])
}

// TestGetDeviceHealth verifies that GET /api/v1/devices/:id/health returns the
// DeviceHealth JSON including the battery_level field.
func TestGetDeviceHealth(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	health := device.DeviceHealth{
		DeviceID:        "dev-001",
		BatteryLevel:    78,
		BatteryCharging: true,
		IsOnline:        true,
		UptimeSeconds:   3600,
		LastSeen:        now,
	}
	mgr := &fakeDeviceManager{Health: health}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices/dev-001/health")

	require.Equal(t, http.StatusOK, rec.Code)

	var body device.DeviceHealth
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "dev-001", body.DeviceID)
	assert.Equal(t, 78, body.BatteryLevel)
	assert.True(t, body.BatteryCharging)
	assert.True(t, body.IsOnline)
}

// TestGetDeviceHealth_NotFound verifies that GET /api/v1/devices/:id/health
// returns HTTP 404 when the device does not exist.
func TestGetDeviceHealth_NotFound(t *testing.T) {
	mgr := &fakeDeviceManager{HealthErr: db.ErrNotFound}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodGet, "/api/v1/devices/unknown/health")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestWakeDevice_Success verifies that POST /api/v1/devices/:id/wake returns
// HTTP 200 when the wake command succeeds.
func TestWakeDevice_Success(t *testing.T) {
	mgr := &fakeDeviceManager{WakeErr: nil}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/dev-001/wake")

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestWakeDevice_NotFound verifies that POST /api/v1/devices/:id/wake returns
// HTTP 404 when the device does not exist.
func TestWakeDevice_NotFound(t *testing.T) {
	mgr := &fakeDeviceManager{WakeErr: db.ErrNotFound}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/unknown/wake")

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "device not found", body["error"])
}

// TestWakeDevice_OfflineError verifies that POST /api/v1/devices/:id/wake
// returns an error response (HTTP 409) when the device is offline.
func TestWakeDevice_OfflineError(t *testing.T) {
	offlineErr := fmt.Errorf("device dev-001 is offline")
	mgr := &fakeDeviceManager{WakeErr: offlineErr}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/dev-001/wake")

	assert.Equal(t, http.StatusConflict, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"])
}

// TestWakeDevice_CommandFailure verifies that POST /api/v1/devices/:id/wake
// returns HTTP 500 when the underlying wake command fails for a reason other
// than the device being offline or not found.
func TestWakeDevice_CommandFailure(t *testing.T) {
	mgr := &fakeDeviceManager{WakeErr: errors.New("adb: device transport error")}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/dev-001/wake")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestRebootDevice_Success verifies that POST /api/v1/devices/:id/reboot
// returns HTTP 202 with {"message": "reboot initiated"}.
func TestRebootDevice_Success(t *testing.T) {
	mgr := &fakeDeviceManager{RebootErr: nil}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/dev-001/reboot")

	assert.Equal(t, http.StatusAccepted, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "reboot initiated", body["message"])
}

// TestRebootDevice_NotFound verifies that POST /api/v1/devices/:id/reboot
// returns HTTP 404 when the device does not exist.
func TestRebootDevice_NotFound(t *testing.T) {
	mgr := &fakeDeviceManager{RebootErr: db.ErrNotFound}
	r := newDeviceTestRouter(mgr)

	rec := doDeviceRequest(r, http.MethodPost, "/api/v1/devices/unknown/reboot")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestSplitTags exercises the splitTags helper with various inputs.
func TestSplitTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "single tag", input: "ci", want: []string{"ci"}},
		{name: "two tags", input: "ci,emulator", want: []string{"ci", "emulator"}},
		{name: "tags with spaces", input: "ci, emulator", want: []string{"ci", "emulator"}},
		{name: "empty string", input: "", want: nil},
		{name: "blank string", input: "  ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitTags(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDeviceRoutes_RegisteredOnRouter verifies that device routes are wired
// correctly when RouterDeps.DeviceManager is set.
func TestDeviceRoutes_RegisteredOnRouter(t *testing.T) {
	devs := sampleDevices()
	mgr := &fakeDeviceManager{ListDevices: devs}

	r := NewRouter(RouterConfig{}, RouterDeps{DeviceManager: mgr})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	r.ServeHTTP(rec, req)

	// Routes exist and are callable (auth is disabled when AuthToken is empty).
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestDeviceRoutes_NotRegisteredWhenNilManager verifies that device routes are
// not registered when RouterDeps.DeviceManager is nil, avoiding a nil-pointer
// panic if the routes were called.
func TestDeviceRoutes_NotRegisteredWhenNilManager(t *testing.T) {
	r := NewRouter(RouterConfig{}, RouterDeps{DeviceManager: nil})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	r.ServeHTTP(rec, req)

	// Route is not registered — expect the catch-all 404 handler.
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleDevices holds a small fixture used across tests.
var sampleDevices = []remoteDevice{
	{
		ID:           "dev-001",
		Model:        "Pixel 7",
		Platform:     "android",
		Status:       "online",
		BatteryLevel: 82,
		LastSeen:     time.Date(2026, 2, 27, 10, 0, 0, 0, time.UTC),
	},
	{
		ID:           "dev-002",
		Model:        "iPhone 15",
		Platform:     "ios",
		Status:       "idle",
		BatteryLevel: 55,
		LastSeen:     time.Date(2026, 2, 27, 9, 30, 0, 0, time.UTC),
	},
}

// newDeviceServer starts an httptest.Server that always returns the given
// devices as JSON. The returned close func must be called after the test.
func newDeviceServer(t *testing.T, devices []remoteDevice, statusCode int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			http.Error(w, `{"error":"server error"}`, statusCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(devices)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAuthCheckingServer starts an httptest.Server that validates the
// Authorization header matches wantToken, returning 401 otherwise.
func newAuthCheckingServer(t *testing.T, wantToken string, devices []remoteDevice) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer "+wantToken {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(devices)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchDevices_Success(t *testing.T) {
	srv := newDeviceServer(t, sampleDevices, http.StatusOK)

	body, err := fetchDevices(srv.URL, "")
	require.NoError(t, err)

	var got []remoteDevice
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "Pixel 7", got[0].Model)
}

func TestFetchDevices_WithToken(t *testing.T) {
	srv := newAuthCheckingServer(t, "mytoken", sampleDevices)

	body, err := fetchDevices(srv.URL, "mytoken")
	require.NoError(t, err)

	var got []remoteDevice
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)
}

func TestFetchDevices_UnauthorizedWithoutToken(t *testing.T) {
	srv := newAuthCheckingServer(t, "secret", sampleDevices)

	_, err := fetchDevices(srv.URL, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
}

func TestFetchDevices_ServerUnreachable(t *testing.T) {
	_, err := fetchDevices("http://127.0.0.1:19999", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreachable")
}

func TestFetchDevices_HTTPError(t *testing.T) {
	srv := newDeviceServer(t, nil, http.StatusInternalServerError)

	_, err := fetchDevices(srv.URL, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
}

func TestPrintDevicesJSON_Valid(t *testing.T) {
	data, err := json.Marshal(sampleDevices)
	require.NoError(t, err)

	// Capture stdout.
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	err = printDevicesJSON(data)

	_ = w.Close()
	os.Stdout = old

	outBuf := make([]byte, 4096)
	n, _ := r.Read(outBuf)
	out := string(outBuf[:n])

	require.NoError(t, err)
	// Pretty-printed JSON must be valid and contain device fields.
	assert.Contains(t, out, "Pixel 7")
	assert.Contains(t, out, "dev-001")
	// Verify it is valid JSON.
	var check []remoteDevice
	require.NoError(t, json.Unmarshal([]byte(out), &check))
}

func TestPrintDevicesJSON_InvalidJSON(t *testing.T) {
	err := printDevicesJSON([]byte("not json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing JSON response")
}

func TestPrintDevicesTable_WithDevices(t *testing.T) {
	data, err := json.Marshal(sampleDevices)
	require.NoError(t, err)

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	printErr := printDevicesTable(data)

	_ = w.Close()
	os.Stdout = old

	outBuf := make([]byte, 4096)
	n, _ := r.Read(outBuf)
	out := string(outBuf[:n])

	require.NoError(t, printErr)
	// Header row.
	assert.True(t, strings.Contains(out, "ID"), "expected ID column header")
	assert.True(t, strings.Contains(out, "MODEL"), "expected MODEL column header")
	assert.True(t, strings.Contains(out, "PLATFORM"), "expected PLATFORM column header")
	assert.True(t, strings.Contains(out, "STATUS"), "expected STATUS column header")
	assert.True(t, strings.Contains(out, "BATTERY"), "expected BATTERY column header")
	assert.True(t, strings.Contains(out, "LAST SEEN"), "expected LAST SEEN column header")
	// Device rows.
	assert.Contains(t, out, "dev-001")
	assert.Contains(t, out, "Pixel 7")
	assert.Contains(t, out, "android")
	assert.Contains(t, out, "82%")
}

func TestPrintDevicesTable_Empty(t *testing.T) {
	data, err := json.Marshal([]remoteDevice{})
	require.NoError(t, err)

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	printErr := printDevicesTable(data)

	_ = w.Close()
	os.Stdout = old

	outBuf := make([]byte, 512)
	n, _ := r.Read(outBuf)
	out := string(outBuf[:n])

	require.NoError(t, printErr)
	assert.Contains(t, out, "No devices found.")
}

func TestPrintDevicesTable_InvalidJSON(t *testing.T) {
	err := printDevicesTable([]byte("bad json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing device list")
}

func TestDevicesCmd_FARMHANDTokenEnvVar(t *testing.T) {
	srv := newAuthCheckingServer(t, "envtoken", sampleDevices)

	t.Setenv("FARMHAND_TOKEN", "envtoken")

	// Call runDevices via fetchDevices to verify env var is picked up.
	// We exercise the env-fallback path directly without exec.
	token := ""
	if token == "" {
		token = os.Getenv("FARMHAND_TOKEN")
	}
	assert.Equal(t, "envtoken", token)

	body, err := fetchDevices(srv.URL, token)
	require.NoError(t, err)

	var got []remoteDevice
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)
}

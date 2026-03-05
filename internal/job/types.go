// Package job handles test job scheduling and execution.
package job

import "time"

// Execution represents a single test execution on one device.
type Execution struct {
	JobID          string
	DeviceID       string
	DeviceSerial   string
	DevicePlatform string
	TestCommand    string
	Env            map[string]string
	TimeoutMinutes int
}

// ExecResult represents the outcome of a single execution.
type ExecResult struct {
	ExitCode     int
	Duration     time.Duration
	LogPath      string
	Error        error
	ErrorMessage string // human-readable failure summary; empty on success
}

// DeviceFilter defines criteria for selecting target devices.
type DeviceFilter struct {
	Platform   string   `json:"platform,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	DeviceIDs  []string `json:"device_ids,omitempty"`
	MaxDevices int      `json:"max_devices,omitempty"`
}

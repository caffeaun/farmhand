// Package device manages physical device lifecycle and state.
package device

import "time"

// Platform constants identify the OS running on a connected device.
const (
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
)

// Device represents a connected physical device.
type Device struct {
	ID           string    `json:"id"`
	Platform     string    `json:"platform"`
	Model        string    `json:"model"`
	OSVersion    string    `json:"os_version"`
	Status       string    `json:"status"`        // online, offline, busy, maintenance
	BatteryLevel int       `json:"battery_level"` // -1 if unknown
	Tags         []string  `json:"tags"`
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
}

// DeviceHealth contains health metrics for a device.
type DeviceHealth struct {
	DeviceID        string    `json:"device_id"`
	BatteryLevel    int       `json:"battery_level"`
	BatteryCharging bool      `json:"battery_charging"`
	IsOnline        bool      `json:"is_online"`
	UptimeSeconds   int64     `json:"uptime_seconds"`
	LastSeen        time.Time `json:"last_seen"`
}

// Bridge is the interface that platform-specific bridges must implement.
type Bridge interface {
	Devices() ([]Device, error)
	IsOnline(id string) bool
}

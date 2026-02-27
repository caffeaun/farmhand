// Package events provides an internal event bus for decoupled communication.
package events

import "time"

// Event types published to the bus.
const (
	DeviceOnline        = "device.online"
	DeviceOffline       = "device.offline"
	DeviceStatusChanged = "device.status_changed"
	JobStarted          = "job.started"
	JobCompleted        = "job.completed"
	JobFailed           = "job.failed"
)

// Event represents a system event published to the bus.
type Event struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

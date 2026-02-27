package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/device"
)

// deviceManagerAPI is the consumer-side interface for device.Manager.
// Defined here (consumer side) to keep the API package independent of
// device.Manager's full surface area.
type deviceManagerAPI interface {
	List(filter db.DeviceFilter) ([]db.Device, error)
	GetByID(id string) (db.Device, error)
	Wake(id string) error
	Reboot(id string) error
	HealthCheck(id string) (device.DeviceHealth, error)
}

// RegisterDeviceRoutes registers device endpoints on the given router group.
// All routes are relative to the group prefix (typically /api/v1).
func RegisterDeviceRoutes(rg *gin.RouterGroup, mgr deviceManagerAPI) {
	rg.GET("/devices", listDevices(mgr))
	rg.GET("/devices/:id", getDevice(mgr))
	rg.GET("/devices/:id/health", getDeviceHealth(mgr))
	rg.POST("/devices/:id/wake", wakeDevice(mgr))
	rg.POST("/devices/:id/reboot", rebootDevice(mgr))
}

// listDevices returns a gin.HandlerFunc that lists devices with optional
// platform and tags query parameter filtering.
//
// Query parameters:
//   - platform: filter by platform (e.g. "android", "ios")
//   - tags: comma-separated list of tags; device must have ALL specified tags
func listDevices(mgr deviceManagerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		filter := db.DeviceFilter{
			Platform: c.Query("platform"),
		}

		if tagsParam := c.Query("tags"); tagsParam != "" {
			filter.Tags = splitTags(tagsParam)
		}

		devices, err := mgr.List(filter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list devices"})
			return
		}

		// Return an empty JSON array rather than null when there are no results.
		if devices == nil {
			devices = []db.Device{}
		}

		c.JSON(http.StatusOK, devices)
	}
}

// getDevice returns a gin.HandlerFunc that retrieves a single device by ID.
// Returns HTTP 404 when the device does not exist.
func getDevice(mgr deviceManagerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		dev, err := mgr.GetByID(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get device"})
			return
		}

		c.JSON(http.StatusOK, dev)
	}
}

// getDeviceHealth returns a gin.HandlerFunc that returns health metrics for a device.
// Returns HTTP 404 when the device does not exist.
func getDeviceHealth(mgr deviceManagerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		health, err := mgr.HealthCheck(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get device health"})
			return
		}

		c.JSON(http.StatusOK, health)
	}
}

// wakeDevice returns a gin.HandlerFunc that sends a wake command to a device.
// Returns:
//   - HTTP 200 on success
//   - HTTP 404 if the device is not found
//   - HTTP 409 if the device is offline (cannot wake an offline device)
//   - HTTP 500 for other errors
func wakeDevice(mgr deviceManagerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		err := mgr.Wake(id)
		if err == nil {
			c.JSON(http.StatusOK, gin.H{"message": "wake command sent"})
			return
		}

		if errors.Is(err, db.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
			return
		}

		// Detect "device is offline" errors from Manager.Wake.
		// The manager returns a plain error with "offline" in the message.
		if isOfflineError(err) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// rebootDevice returns a gin.HandlerFunc that initiates a device reboot.
// Always returns HTTP 202 Accepted when the command is dispatched successfully.
// Returns HTTP 404 if the device is not found, HTTP 500 for other errors.
func rebootDevice(mgr deviceManagerAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")

		err := mgr.Reboot(id)
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{"message": "reboot initiated"})
	}
}

// splitTags splits a comma-separated tag string and trims whitespace.
// Returns nil for empty or blank input.
func splitTags(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isOfflineError reports whether the error indicates the device is offline.
// Manager.Wake returns fmt.Errorf("device %s is offline", id), which is a
// plain error (not a sentinel type), so we check the message text.
func isOfflineError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "offline")
}

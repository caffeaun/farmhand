package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/config"
	"github.com/caffeaun/farmhand/internal/db"
)

// statsDeviceRepoAPI is the consumer-side interface for counting devices by status.
type statsDeviceRepoAPI interface {
	FindAll(filter db.DeviceFilter) ([]db.Device, error)
}

// statsJobRepoAPI is the consumer-side interface for counting jobs by status.
type statsJobRepoAPI interface {
	FindAll(filter db.JobFilter) ([]db.Job, error)
}

// StatsResponse is the response body for GET /api/v1/stats.
type StatsResponse struct {
	Devices DeviceStats `json:"devices"`
	Jobs    JobStats    `json:"jobs"`
}

// DeviceStats holds per-status device counts.
type DeviceStats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
	Busy    int `json:"busy"`
}

// JobStats holds per-status job counts.
type JobStats struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// RegisterSystemRoutes registers the config and stats endpoints on the given
// router group. Both routes are protected by the group's auth middleware.
func RegisterSystemRoutes(rg *gin.RouterGroup, cfg *config.Config, deviceRepo statsDeviceRepoAPI, jobRepo statsJobRepoAPI) {
	rg.GET("/config", getConfig(cfg))
	rg.GET("/stats", getStats(deviceRepo, jobRepo))
}

// getConfig returns a handler that renders the running configuration.
// The auth_token field is masked as "***" to prevent credential leakage;
// an empty token stays empty (no-auth mode indicator).
func getConfig(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Shallow-copy the top-level struct and then the nested ServerConfig so
		// we can mask auth_token without mutating the shared config value.
		masked := *cfg
		if masked.Server.AuthToken != "" {
			masked.Server.AuthToken = "***"
		}
		c.JSON(http.StatusOK, masked)
	}
}

// getStats returns a handler that counts devices and jobs by status from the
// live database. All counts are initialised to zero so the response is always
// well-formed even when the database is empty.
func getStats(deviceRepo statsDeviceRepoAPI, jobRepo statsJobRepoAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		devices, err := deviceRepo.FindAll(db.DeviceFilter{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch device stats"})
			return
		}

		jobs, err := jobRepo.FindAll(db.JobFilter{})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch job stats"})
			return
		}

		var ds DeviceStats
		for _, d := range devices {
			ds.Total++
			switch d.Status {
			case "online":
				ds.Online++
			case "offline":
				ds.Offline++
			case "busy":
				ds.Busy++
			}
		}

		var js JobStats
		for _, j := range jobs {
			js.Total++
			switch j.Status {
			case "queued":
				js.Queued++
			case "running", "preparing", "installing":
				// Treat in-progress states as running.
				js.Running++
			case "completed":
				js.Completed++
			case "failed":
				js.Failed++
			}
		}

		c.JSON(http.StatusOK, StatsResponse{
			Devices: ds,
			Jobs:    js,
		})
	}
}

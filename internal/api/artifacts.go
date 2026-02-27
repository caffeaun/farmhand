package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/job"
)

// artifactCollectorAPI is the consumer-side interface for job.ArtifactCollector.
type artifactCollectorAPI interface {
	List(jobID, deviceID string) ([]job.Artifact, error)
	ReadArtifact(path string) (io.ReadCloser, error)
}

// artifactJobRepoAPI checks job existence for artifact endpoints.
type artifactJobRepoAPI interface {
	FindByID(id string) (db.Job, error)
}

// artifactResultRepoAPI gets device IDs for artifact listing.
type artifactResultRepoAPI interface {
	FindByJobID(jobID string) ([]db.JobResult, error)
}

// RegisterArtifactRoutes registers artifact list and download endpoints on the
// given router group.
func RegisterArtifactRoutes(rg *gin.RouterGroup, jobRepo artifactJobRepoAPI, resultRepo artifactResultRepoAPI, collector artifactCollectorAPI) {
	rg.GET("/jobs/:id/artifacts", listArtifacts(jobRepo, resultRepo, collector))
	rg.GET("/jobs/:id/artifacts/*filepath", downloadArtifact(jobRepo, resultRepo, collector))
}

// artifactResponse is the per-artifact entry returned by GET /jobs/:id/artifacts.
type artifactResponse struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
}

// listArtifacts returns a handler that aggregates artifacts from every device
// that ran the job and returns them as a flat JSON array.
//
// Response shape per entry: {"filename":"...","size_bytes":N,"mime_type":"..."}.
// Returns HTTP 404 when the job is not found.
// Returns an empty array (not null) when the job exists but has no artifacts.
func listArtifacts(jobRepo artifactJobRepoAPI, resultRepo artifactResultRepoAPI, collector artifactCollectorAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := c.Param("id")

		if _, err := jobRepo.FindByID(jobID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
			return
		}

		results, err := resultRepo.FindByJobID(jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job results"})
			return
		}

		out := make([]artifactResponse, 0)
		for _, result := range results {
			artifacts, err := collector.List(jobID, result.DeviceID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list artifacts"})
				return
			}
			for _, a := range artifacts {
				out = append(out, artifactResponse{
					Filename:  a.Filename,
					SizeBytes: a.SizeBytes,
					MimeType:  a.MimeType,
				})
			}
		}

		c.JSON(http.StatusOK, out)
	}
}

// downloadArtifact returns a handler that streams artifact file bytes to the
// client with appropriate Content-Type and Content-Disposition headers.
//
// Path traversal protection:
//   - Rejects filenames containing ".." or null bytes → 400
//   - Applies filepath.Clean and verifies the result has no ".." prefix and is
//     not an absolute path
//
// Returns HTTP 400 for invalid filenames, HTTP 404 when the job or artifact is
// not found.
func downloadArtifact(jobRepo artifactJobRepoAPI, resultRepo artifactResultRepoAPI, collector artifactCollectorAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := c.Param("id")

		// Gin's *filepath param includes a leading slash; strip it.
		filename := strings.TrimPrefix(c.Param("filepath"), "/")

		// Security: reject obvious path traversal patterns before any further
		// processing. Check for null bytes, ".." components, and encoded
		// variants (%2F, %2E%2E%2F). The URL is already decoded by Gin before
		// it reaches this handler, so we only need to inspect the raw string.
		if strings.Contains(filename, "..") ||
			strings.Contains(filename, "\x00") ||
			strings.Contains(filename, "%2e") ||
			strings.Contains(filename, "%2E") ||
			strings.Contains(filename, "%2f") ||
			strings.Contains(filename, "%2F") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
			return
		}

		// Additional safety: clean the path and verify it doesn't escape.
		cleaned := filepath.Clean(filename)
		if cleaned != filename ||
			strings.HasPrefix(cleaned, "/") ||
			strings.HasPrefix(cleaned, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
			return
		}

		if _, err := jobRepo.FindByID(jobID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
			return
		}

		results, err := resultRepo.FindByJobID(jobID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job results"})
			return
		}

		// Search all device results for an artifact matching the requested filename.
		var matched *job.Artifact
		for _, result := range results {
			artifacts, err := collector.List(jobID, result.DeviceID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list artifacts"})
				return
			}
			for i := range artifacts {
				if artifacts[i].Filename == filename {
					matched = &artifacts[i]
					break
				}
			}
			if matched != nil {
				break
			}
		}

		if matched == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
			return
		}

		rc, err := collector.ReadArtifact(matched.Path)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
			return
		}
		defer rc.Close() //nolint:errcheck

		c.Header("Content-Type", matched.MimeType)
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(matched.Filename)))
		c.Status(http.StatusOK)

		if _, err := io.Copy(c.Writer, rc); err != nil {
			// Headers have already been sent; we cannot change the status code.
			// Log the error implicitly through the recovery middleware if the
			// connection drops mid-stream.
			return
		}
	}
}

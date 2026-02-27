package job

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Artifact represents a collected test output file.
type Artifact struct {
	Filename  string `json:"filename"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
}

// mimeTypes maps file extensions to MIME types.
var mimeTypes = map[string]string{
	".xml":  "application/xml",
	".json": "application/json",
	".html": "text/html",
	".htm":  "text/html",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".mp4":  "video/mp4",
	".txt":  "text/plain",
	".log":  "text/plain",
	".csv":  "text/csv",
}

// ArtifactCollector gathers test output files after execution.
type ArtifactCollector struct {
	artifactDir string
}

// NewArtifactCollector creates a collector with the given base directory.
func NewArtifactCollector(artifactDir string) *ArtifactCollector {
	return &ArtifactCollector{artifactDir: artifactDir}
}

// Collect walks sourceDir recursively and copies all files to
// <artifactDir>/<jobID>/<deviceID>/. Returns the list of collected artifacts.
// Validates that all paths resolve within artifactDir (filepath.Clean check).
// If sourceDir does not exist, returns empty list without error.
func (c *ArtifactCollector) Collect(jobID, deviceID, sourceDir string) ([]Artifact, error) {
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil, nil
	}

	destDir := filepath.Join(c.artifactDir, jobID, deviceID)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("create dest dir: %w", err)
	}

	cleanBase := filepath.Clean(c.artifactDir)

	var artifacts []Artifact

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}

		destPath := filepath.Join(destDir, relPath)

		// Security: verify destPath is within artifactDir.
		cleanDest := filepath.Clean(destPath)
		if !strings.HasPrefix(cleanDest, cleanBase+string(filepath.Separator)) &&
			cleanDest != cleanBase {
			return fmt.Errorf("path traversal detected: %s", relPath)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("create subdir: %w", err)
		}

		if err := copyFile(path, destPath); err != nil {
			return fmt.Errorf("copy %s: %w", relPath, err)
		}

		artifacts = append(artifacts, Artifact{
			Filename:  relPath,
			Path:      destPath,
			SizeBytes: info.Size(),
			MimeType:  detectMimeType(relPath),
		})

		return nil
	})

	return artifacts, err
}

// List returns artifacts for a (jobID, deviceID) pair by scanning the artifact directory.
// If the directory does not exist, returns an empty list without error.
func (c *ArtifactCollector) List(jobID, deviceID string) ([]Artifact, error) {
	dir := filepath.Join(c.artifactDir, jobID, deviceID)
	cleanBase := filepath.Clean(c.artifactDir)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	var artifacts []Artifact

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}

		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}

		cleanPath := filepath.Clean(path)
		if !strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator)) &&
			cleanPath != cleanBase {
			return fmt.Errorf("path traversal detected: %s", relPath)
		}

		artifacts = append(artifacts, Artifact{
			Filename:  relPath,
			Path:      path,
			SizeBytes: info.Size(),
			MimeType:  detectMimeType(relPath),
		})

		return nil
	})

	return artifacts, err
}

// ReadArtifact opens an artifact file for reading.
// Returns an error if the file is not found or if the path escapes artifactDir.
func (c *ArtifactCollector) ReadArtifact(path string) (io.ReadCloser, error) {
	cleanPath := filepath.Clean(path)
	cleanBase := filepath.Clean(c.artifactDir)
	if !strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator)) &&
		cleanPath != cleanBase {
		return nil, fmt.Errorf("path traversal detected")
	}

	f, err := os.Open(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("artifact not found: %s", path)
		}
		return nil, err
	}
	return f, nil
}

// detectMimeType returns MIME type based on file extension.
func detectMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if mt, ok := mimeTypes[ext]; ok {
		return mt
	}
	return "application/octet-stream"
}

// copyFile copies src to dst, creating dst if it does not exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

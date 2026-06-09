package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// GitHubRepo identifies the upstream FarmHand repo we pull releases from.
const GitHubRepo = "caffeaun/farmhand"

// Release is the subset of fields we care about from the GitHub releases API.
type Release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// LatestRelease fetches the metadata for the latest published release.
func LatestRelease() (*Release, error) {
	return fetchRelease("https://api.github.com/repos/" + GitHubRepo + "/releases/latest")
}

// ReleaseByTag fetches a specific tagged release.
func ReleaseByTag(tag string) (*Release, error) {
	return fetchRelease("https://api.github.com/repos/" + GitHubRepo + "/releases/tags/" + tag)
}

func fetchRelease(url string) (*Release, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("github releases returned %d: %s", resp.StatusCode, string(body))
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}
	return &rel, nil
}

// AssetURL returns the browser download URL for the asset matching this
// platform (e.g. "farmhand-darwin-arm64") within rel.
func (rel *Release) AssetURL(assetName string) (string, error) {
	for _, a := range rel.Assets {
		if a.Name == assetName {
			return a.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("release %s has no asset named %s", rel.TagName, assetName)
}

// DownloadBinary fetches url to a temp file, sets exec bit, and returns the
// temp path. Caller is responsible for renaming/removing it.
func DownloadBinary(url string, progress io.Writer) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "farmhand-download-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		_ = os.Remove(tmpPath)
	}

	var w io.Writer = tmp
	if progress != nil {
		w = io.MultiWriter(tmp, progress)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		cleanup()
		return "", fmt.Errorf("write download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close download: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("chmod download: %w", err)
	}
	return tmpPath, nil
}

// AtomicSwapBinary replaces the binary at destPath with the file at srcPath,
// using rename if same-filesystem or copy+rename if not. The destination dir
// must be writable by the current process.
func AtomicSwapBinary(srcPath, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir for binary: %w", err)
	}
	// Cross-filesystem renames fail with EXDEV — fall back to copy+rename.
	if err := os.Rename(srcPath, destPath); err == nil {
		return nil
	}
	stagedPath := destPath + ".new"
	if err := copyFile(srcPath, stagedPath); err != nil {
		return err
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("chmod staged: %w", err)
	}
	if err := os.Rename(stagedPath, destPath); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("rename staged -> %s: %w", destPath, err)
	}
	_ = os.Remove(srcPath)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}

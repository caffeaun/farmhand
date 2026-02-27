package job

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LogCollector manages log file lifecycle for job executions.
type LogCollector struct {
	logDir string
}

// NewLogCollector creates a LogCollector with the given base directory.
func NewLogCollector(logDir string) *LogCollector {
	return &LogCollector{logDir: logDir}
}

// logPath returns the path to the log file for a (jobID, deviceID) pair.
func (c *LogCollector) logPath(jobID, deviceID string) string {
	return filepath.Join(c.logDir, jobID, deviceID+".log")
}

// Write appends a line to the log file, creating directories if needed.
// Appends a newline if the line doesn't already end with one.
// Uses O_APPEND flag for atomic POSIX writes.
func (c *LogCollector) Write(jobID, deviceID string, line []byte) error {
	dir := filepath.Join(c.logDir, jobID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	f, err := os.OpenFile(c.logPath(jobID, deviceID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	// Append newline if not present.
	if len(line) == 0 || line[len(line)-1] != '\n' {
		line = append(line, '\n')
	}

	_, err = f.Write(line)
	return err
}

// Read returns an io.ReadCloser for the full log file.
// Returns an error wrapping the path if the file does not exist.
func (c *LogCollector) Read(jobID, deviceID string) (io.ReadCloser, error) {
	path := c.logPath(jobID, deviceID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("log not found: %s/%s", jobID, deviceID)
		}
		return nil, fmt.Errorf("open log: %w", err)
	}
	return f, nil
}

// Tail streams new log lines to ch as they are written.
// First sends all existing lines, then polls for new content every 75ms.
// Stops when ctx is cancelled.
func (c *LogCollector) Tail(ctx context.Context, jobID, deviceID string, ch chan<- string) error {
	path := c.logPath(jobID, deviceID)

	// Poll until the file appears or context is cancelled.
	var f *os.File
	for {
		var err error
		f, err = os.Open(path)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("open log for tail: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer f.Close()

	// readNewLines drains any new content from the current file position,
	// sending each complete line to ch. Returns an error only on context cancel.
	readNewLines := func() error {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			select {
			case ch <- scanner.Text():
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	// Send all existing lines before starting to poll.
	if err := readNewLines(); err != nil {
		return err
	}

	// Poll for new content every 75ms. Each tick re-creates the scanner so
	// the underlying file offset advances past previously read content.
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := readNewLines(); err != nil {
				return err
			}
		}
	}
}

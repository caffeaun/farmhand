package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// runDeviceFilter is the device_filter sub-object in the job creation request.
type runDeviceFilter struct {
	Platform string   `json:"platform,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

// runCreateJobRequest is the JSON body for POST /api/v1/jobs.
type runCreateJobRequest struct {
	TestCommand    string          `json:"test_command"`
	InstallCommand string          `json:"install_command,omitempty"`
	DeviceFilter   runDeviceFilter `json:"device_filter"`
	TimeoutMinutes int             `json:"timeout_minutes"`
}

// runCreateJobResponse is the subset of the job record returned by the server
// that the run command needs.
type runCreateJobResponse struct {
	ID string `json:"id"`
}

// runJobStatusResponse is the subset of GET /api/v1/jobs/:id/status the run
// command inspects to determine the final exit code.
type runJobStatusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// runCmd submits a test job to the FarmHand server and optionally streams logs.
var runCmd = &cobra.Command{
	Use:          "run",
	Short:        "Submit and monitor a test job",
	Long:         "Submit a test job to a FarmHand server and, by default, stream log output until the job completes.\nUse --wait=false to submit the job and exit immediately after printing the job ID.",
	SilenceUsage: true,
	RunE:         runJobCmd,
}

func init() {
	runCmd.Flags().String("server", "http://localhost:8080", "FarmHand server base URL")
	runCmd.Flags().String("token", "", "bearer authentication token")
	runCmd.Flags().String("command", "", "test command to run on the device (required)")
	runCmd.Flags().String("install", "", "install command to run before the test command (optional)")
	runCmd.Flags().String("platform", "", "device platform filter (android or ios)")
	runCmd.Flags().StringSlice("tags", nil, "device tag filters (comma-separated or multiple flags)")
	runCmd.Flags().Int("timeout", 30, "job timeout in minutes")
	runCmd.Flags().Bool("wait", true, "wait for job completion and stream logs")

	if err := runCmd.MarkFlagRequired("command"); err != nil {
		// MarkFlagRequired only errors when the named flag does not exist.
		panic(err)
	}
}

// runJobCmd is the RunE handler for the run subcommand.
func runJobCmd(cmd *cobra.Command, _ []string) error {
	server, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	command, _ := cmd.Flags().GetString("command")
	install, _ := cmd.Flags().GetString("install")
	platform, _ := cmd.Flags().GetString("platform")
	tags, _ := cmd.Flags().GetStringSlice("tags")
	timeoutMinutes, _ := cmd.Flags().GetInt("timeout")
	wait, _ := cmd.Flags().GetBool("wait")

	// Fall back to the environment variable when the flag is not provided.
	if token == "" {
		token = os.Getenv("FARMHAND_TOKEN")
	}

	jobID, err := doSubmitJob(server, token, command, install, platform, tags, timeoutMinutes)
	if err != nil {
		return fmt.Errorf("submitting job: %w", err)
	}

	fmt.Println(jobID)

	if !wait {
		return nil
	}

	// Stream logs from the SSE endpoint until the server signals completion.
	done, err := doStreamLogs(server, token, jobID)
	if err != nil {
		return fmt.Errorf("streaming logs: %w", err)
	}

	if done {
		// Poll the status endpoint to determine the final exit code.
		status, err := doFetchStatus(server, token, jobID)
		if err != nil {
			return fmt.Errorf("fetching job status: %w", err)
		}

		if status != "completed" {
			os.Exit(1)
		}
	}

	return nil
}

// doSubmitJob sends POST /api/v1/jobs and returns the new job ID on success.
func doSubmitJob(server, token, command, install, platform string, tags []string, timeoutMinutes int) (string, error) {
	reqBody := runCreateJobRequest{
		TestCommand:    command,
		InstallCommand: install,
		DeviceFilter: runDeviceFilter{
			Platform: platform,
			Tags:     tags,
		},
		TimeoutMinutes: timeoutMinutes,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request body: %w", err)
	}

	url := strings.TrimRight(server, "/") + "/api/v1/jobs"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	addRunAuthHeader(req, token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("server unreachable (%s): %w", server, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var jobResp runCreateJobResponse
	if err := json.NewDecoder(resp.Body).Decode(&jobResp); err != nil {
		return "", fmt.Errorf("decoding job response: %w", err)
	}

	if jobResp.ID == "" {
		return "", fmt.Errorf("server returned an empty job ID")
	}

	return jobResp.ID, nil
}

// doStreamLogs connects to GET /api/v1/jobs/:id/logs (SSE) and writes each
// log line to stdout. It returns (true, nil) when the server sends the
// terminal "event: done" event, or (false, nil) when the stream closes
// without a done event.
func doStreamLogs(server, token, jobID string) (bool, error) {
	url := strings.TrimRight(server, "/") + "/api/v1/jobs/" + jobID + "/logs"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "text/event-stream")
	addRunAuthHeader(req, token)

	// No read timeout — the stream runs until the server signals completion or
	// the process is interrupted.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("connecting to log stream (%s): %w", server, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Parse the SSE stream line by line.
	//
	// SSE wire format (simplified):
	//   data: <text>\n\n          — a single-field log event
	//   event: done\ndata: {}\n\n — the terminal event
	//
	// We track the most-recently-seen "event:" field so we know which event
	// type the next "data:" line belongs to.
	scanner := bufio.NewScanner(resp.Body)
	currentEvent := ""

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")

		case strings.HasPrefix(line, "data: "):
			if currentEvent == "done" {
				// Terminal event received — stop consuming the stream.
				return true, nil
			}
			// Write the log payload to stdout.
			fmt.Println(strings.TrimPrefix(line, "data: "))

		case line == "":
			// A blank line marks the end of a multi-line event; reset the
			// event type for the next event.
			currentEvent = ""
		}
	}

	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("reading log stream: %w", err)
	}

	// Stream closed without a done event.
	return false, nil
}

// doFetchStatus calls GET /api/v1/jobs/:id/status and returns the job status string.
func doFetchStatus(server, token, jobID string) (string, error) {
	url := strings.TrimRight(server, "/") + "/api/v1/jobs/" + jobID + "/status"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building request for %s: %w", url, err)
	}
	addRunAuthHeader(req, token)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("server unreachable (%s): %w", server, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var statusResp runJobStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		return "", fmt.Errorf("decoding status response: %w", err)
	}

	return statusResp.Status, nil
}

// addRunAuthHeader sets the Authorization header when a token is present.
func addRunAuthHeader(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

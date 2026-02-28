package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// remoteDevice mirrors the JSON shape returned by GET /api/v1/devices.
// Only the fields required for display are declared.
type remoteDevice struct {
	ID           string    `json:"id"`
	Model        string    `json:"model"`
	Platform     string    `json:"platform"`
	Status       string    `json:"status"`
	BatteryLevel int       `json:"battery_level"`
	LastSeen     time.Time `json:"last_seen"`
}

// devicesCmd lists devices registered with the FarmHand server.
var devicesCmd = &cobra.Command{
	Use:          "devices",
	Short:        "List devices registered with the FarmHand server",
	Long:         "Fetches the list of devices from a running FarmHand server and prints them as a table (default) or JSON.",
	SilenceUsage: true,
	RunE:         runDevices,
}

func init() {
	devicesCmd.Flags().String("server", "http://localhost:8080", "FarmHand server base URL")
	devicesCmd.Flags().String("token", "", "Bearer auth token (overrides FARMHAND_TOKEN env var)")
	devicesCmd.Flags().String("format", "table", `Output format: "table" or "json"`)
}

// runDevices is the RunE handler for the devices subcommand.
func runDevices(cmd *cobra.Command, _ []string) error {
	server, _ := cmd.Flags().GetString("server")
	token, _ := cmd.Flags().GetString("token")
	format, _ := cmd.Flags().GetString("format")

	// Fall back to the environment variable when the flag is not provided.
	if token == "" {
		token = os.Getenv("FARMHAND_TOKEN")
	}

	if format != "table" && format != "json" {
		return fmt.Errorf("invalid --format %q: must be \"table\" or \"json\"", format)
	}

	body, err := fetchDevices(server, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	switch format {
	case "json":
		return printDevicesJSON(body)
	default:
		return printDevicesTable(body)
	}
}

// fetchDevices calls GET /api/v1/devices on server and returns the raw
// response body. On any transport or HTTP-level error it returns a descriptive
// error so the caller can print it to stderr and exit 1.
func fetchDevices(server, token string) ([]byte, error) {
	url := server + "/api/v1/devices"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 15 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server unreachable (%s): %w", server, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// printDevicesJSON pretty-prints the raw JSON response body to stdout.
func printDevicesJSON(body []byte) error {
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("parsing JSON response: %w", err)
	}

	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("formatting JSON: %w", err)
	}

	fmt.Println(string(pretty))
	return nil
}

// printDevicesTable parses the device list and renders an aligned ASCII table
// using text/tabwriter. Prints "No devices found." when the list is empty.
func printDevicesTable(body []byte) error {
	var devices []remoteDevice
	if err := json.Unmarshal(body, &devices); err != nil {
		return fmt.Errorf("parsing device list: %w", err)
	}

	if len(devices) == 0 {
		fmt.Println("No devices found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tMODEL\tPLATFORM\tSTATUS\tBATTERY\tLAST SEEN")

	for _, d := range devices {
		lastSeen := "never"
		if !d.LastSeen.IsZero() {
			lastSeen = d.LastSeen.Format(time.RFC3339)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d%%\t%s\n",
			d.ID,
			d.Model,
			d.Platform,
			d.Status,
			d.BatteryLevel,
			lastSeen,
		)
	}

	return w.Flush()
}

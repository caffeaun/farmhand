package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/device"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/caffeaun/farmhand/internal/vision"
)

// inspectManager is the manager surface inspect needs — just Screenshot.
// Declared narrowly so tests can fake it without satisfying the full
// Manager surface.
type inspectManager interface {
	Screenshot(id string) ([]byte, error)
}

// inspectDeps bundles the manager + vision client + cleanup that each CLI
// invocation needs. The factory is swappable for tests.
type inspectDeps struct {
	Manager inspectManager
	Client  vision.Client
	Cleanup func()
}

// inspectFactory is the production wiring; tests override it to inject
// fakes without touching the real DB / adb / network.
var inspectFactory = newProductionInspectDeps

// inspectMockFactory is the wiring used when --mock-from is set: same
// device manager (so screenshots still come from the real device) but no
// vision client and no API-key check. Overridable by tests.
var inspectMockFactory = newMockInspectDeps

// newProductionManagerDeps wires the device manager only — used by both
// production and mock factories.
func newProductionManagerDeps() (inspectManager, func(), error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config not loaded")
	}
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	bridge, err := device.NewADBBridge(cfg.Devices.ADBPath)
	if err != nil {
		_ = database.Close()
		return nil, nil, fmt.Errorf("adb bridge: %w", err)
	}
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	mgr := device.NewManager(
		bridge,
		nil, nil,
		repo, bus,
		time.Hour, // pollInterval unused; Start() is never called
		zerolog.Nop(),
	)
	cleanup := func() {
		bus.Close()
		_ = database.Close()
	}
	return mgr, cleanup, nil
}

func newProductionInspectDeps() (inspectDeps, error) {
	if cfg == nil {
		return inspectDeps{}, fmt.Errorf("config not loaded")
	}
	apiKey := os.Getenv(cfg.Vision.APIKeyEnv)
	if apiKey == "" {
		return inspectDeps{}, fmt.Errorf("vision provider not configured: env %s is empty", cfg.Vision.APIKeyEnv)
	}

	mgr, cleanup, err := newProductionManagerDeps()
	if err != nil {
		return inspectDeps{}, err
	}

	timeout := time.Duration(cfg.Vision.TimeoutSec) * time.Second
	client := vision.NewMiniMaxClient(cfg.Vision.BaseURL, apiKey, cfg.Vision.Model, cfg.Vision.Detail, timeout)

	return inspectDeps{
		Manager: mgr,
		Client:  client,
		Cleanup: cleanup,
	}, nil
}

func newMockInspectDeps() (inspectDeps, error) {
	mgr, cleanup, err := newProductionManagerDeps()
	if err != nil {
		return inspectDeps{}, err
	}
	return inspectDeps{
		Manager: mgr,
		Client:  nil, // never used in mock mode
		Cleanup: cleanup,
	}, nil
}

var inspectCmd = &cobra.Command{
	Use:          "inspect",
	Short:        "Inspect a device screenshot via a vision LLM and print the topic list",
	Long:         "Takes a screenshot of --device, POSTs it to the configured vision provider with a system prompt asking for a structured inspection, and prints the resulting topic list ({topics: [{name, coordinates, color, type, text}], screenshot_size}) as JSON on stdout.\n\nUse --mock-from <file> to skip the vision provider entirely and read the topic list from a local JSON file — useful for E2E testing the inspect+tap chain against a real device without spending LLM tokens.",
	SilenceUsage: true,
	RunE:         runInspect,
}

func init() {
	inspectCmd.Flags().String("device", "", "device ID (required)")
	inspectCmd.Flags().String("mock-from", "", "path to a JSON file containing a canned InspectResult to return instead of calling the vision provider")
	_ = inspectCmd.MarkFlagRequired("device")
}

// inspectOutput is the stdout shape: the model's topic list plus screen
// dimensions, so callers can sanity-check that bounding boxes are inside
// the screen before computing tap coordinates.
type inspectOutput struct {
	Topics         []vision.Topic `json:"topics"`
	ScreenshotSize struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"screenshot_size"`
}

func runInspect(cmd *cobra.Command, _ []string) error {
	deviceID, _ := cmd.Flags().GetString("device")
	mockFrom, _ := cmd.Flags().GetString("mock-from")

	// --mock-from short-circuits the vision provider; we still need the
	// real device manager so the screenshot grab + tap chain runs end-to-end
	// against the hardware. inspectMockFactory is the lighter factory used
	// in that branch — it skips the API-key check.
	var (
		deps inspectDeps
		err  error
	)
	if mockFrom != "" {
		deps, err = inspectMockFactory()
	} else {
		deps, err = inspectFactory()
	}
	if err != nil {
		return err
	}
	if deps.Cleanup != nil {
		defer deps.Cleanup()
	}

	pngBytes, err := deps.Manager.Screenshot(deviceID)
	if err != nil {
		return fmt.Errorf("screenshot: %w", err)
	}

	cfgImg, decodeErr := png.DecodeConfig(bytes.NewReader(pngBytes))
	if decodeErr != nil {
		return fmt.Errorf("decode png header: %w", decodeErr)
	}

	var res vision.InspectResult
	if mockFrom != "" {
		res, err = loadMockInspectResult(mockFrom)
		if err != nil {
			return fmt.Errorf("--mock-from %s: %w", mockFrom, err)
		}
	} else {
		ctx := context.Background()
		res, err = deps.Client.Inspect(ctx, pngBytes)
		if err != nil {
			return fmt.Errorf("vision inspect: %w", err)
		}
	}

	out := inspectOutput{Topics: res.Topics}
	out.ScreenshotSize.Width = cfgImg.Width
	out.ScreenshotSize.Height = cfgImg.Height

	return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
}

// loadMockInspectResult reads a JSON file containing an InspectResult and
// returns it. Accepts either the top-level InspectResult shape ({topics:
// [...]}) or the full inspectOutput shape ({topics: [...], screenshot_size:
// {...}}) — the screenshot_size in the file is ignored; the CLI uses the
// dimensions of the real screenshot it captured.
func loadMockInspectResult(path string) (vision.InspectResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return vision.InspectResult{}, err
	}
	var res vision.InspectResult
	if err := json.Unmarshal(data, &res); err != nil {
		return vision.InspectResult{}, fmt.Errorf("parse JSON: %w", err)
	}
	return res, nil
}

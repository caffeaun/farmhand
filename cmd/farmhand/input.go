package main

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/device"
	"github.com/caffeaun/farmhand/internal/events"
)

// deviceManagerCLI is the subset of *device.Manager methods used by the
// tap/swipe/keyevent/text subcommands. Defined here so tests can inject a
// fake without touching the real DB or adb binary.
type deviceManagerCLI interface {
	Tap(id string, x, y int) error
	Swipe(id string, x1, y1, x2, y2, durationMs int) error
	KeyEvent(id, keycode string) error
	InputText(id, text string) error
}

// inputManagerFactory builds the per-invocation manager + cleanup. The
// default implementation wires SQLite + adb; tests override it to return
// a fake manager so they need not stub the bridge or DB.
var inputManagerFactory = newProductionInputManager

// newProductionInputManager wires up an offline-friendly Manager backed by
// SQLite + adb. No polling goroutine is started — the CLI uses Manager
// purely as a one-shot dispatcher with FindByID + bridge-call semantics.
func newProductionInputManager() (deviceManagerCLI, func(), error) {
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
		nil, nil, // input is android-only; no iOS / simulator bridges needed
		repo, bus,
		time.Hour, // pollInterval is irrelevant — Start() is never called
		zerolog.Nop(),
	)
	cleanup := func() {
		bus.Close()
		_ = database.Close()
	}
	return mgr, cleanup, nil
}

// --- tap ---

var tapCmd = &cobra.Command{
	Use:          "tap",
	Short:        "Send a single tap to an Android device",
	Long:         "Dispatch one input-tap at (x, y) on the device with the given --device ID (USB serial or wireless ip:port).",
	SilenceUsage: true,
	RunE:         runTap,
}

func init() {
	tapCmd.Flags().String("device", "", "device ID (required)")
	tapCmd.Flags().Int("x", 0, "X coordinate (required, non-negative)")
	tapCmd.Flags().Int("y", 0, "Y coordinate (required, non-negative)")
	_ = tapCmd.MarkFlagRequired("device")
	_ = tapCmd.MarkFlagRequired("x")
	_ = tapCmd.MarkFlagRequired("y")
}

func runTap(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")
	x, _ := cmd.Flags().GetInt("x")
	y, _ := cmd.Flags().GetInt("y")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	return mgr.Tap(id, x, y)
}

// --- swipe ---

var swipeCmd = &cobra.Command{
	Use:          "swipe",
	Short:        "Send a swipe gesture to an Android device",
	Long:         "Dispatch a swipe from (--from-x, --from-y) to (--to-x, --to-y) on the device. --duration-ms is the gesture duration; 0 (default) lets the device choose.",
	SilenceUsage: true,
	RunE:         runSwipe,
}

func init() {
	swipeCmd.Flags().String("device", "", "device ID (required)")
	swipeCmd.Flags().Int("from-x", 0, "starting X coordinate (required)")
	swipeCmd.Flags().Int("from-y", 0, "starting Y coordinate (required)")
	swipeCmd.Flags().Int("to-x", 0, "ending X coordinate (required)")
	swipeCmd.Flags().Int("to-y", 0, "ending Y coordinate (required)")
	swipeCmd.Flags().Int("duration-ms", 0, "gesture duration in milliseconds (0 = device default)")
	for _, name := range []string{"device", "from-x", "from-y", "to-x", "to-y"} {
		_ = swipeCmd.MarkFlagRequired(name)
	}
}

func runSwipe(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")
	x1, _ := cmd.Flags().GetInt("from-x")
	y1, _ := cmd.Flags().GetInt("from-y")
	x2, _ := cmd.Flags().GetInt("to-x")
	y2, _ := cmd.Flags().GetInt("to-y")
	dur, _ := cmd.Flags().GetInt("duration-ms")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	return mgr.Swipe(id, x1, y1, x2, y2, dur)
}

// --- keyevent ---

var keyEventCmd = &cobra.Command{
	Use:          "keyevent",
	Short:        "Send a keyevent to an Android device",
	Long:         "Dispatch one keyevent. --keycode accepts either a non-negative integer (e.g. \"4\" for BACK) or a symbolic name matching ^KEYCODE_[A-Z0-9_]+$ (e.g. \"KEYCODE_BACK\").",
	SilenceUsage: true,
	RunE:         runKeyEvent,
}

func init() {
	keyEventCmd.Flags().String("device", "", "device ID (required)")
	keyEventCmd.Flags().String("keycode", "", "keycode (integer or KEYCODE_X) (required)")
	_ = keyEventCmd.MarkFlagRequired("device")
	_ = keyEventCmd.MarkFlagRequired("keycode")
}

func runKeyEvent(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")
	keycode, _ := cmd.Flags().GetString("keycode")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	return mgr.KeyEvent(id, keycode)
}

// --- text ---

var textCmd = &cobra.Command{
	Use:          "text",
	Short:        "Type text into the focused field on an Android device",
	Long:         "Send `input text` to the device. The text is shell-quoted on the device side so embedded metacharacters (; & | $ etc.) are treated literally.",
	SilenceUsage: true,
	RunE:         runText,
}

func init() {
	textCmd.Flags().String("device", "", "device ID (required)")
	textCmd.Flags().String("text", "", "text to type (required)")
	_ = textCmd.MarkFlagRequired("device")
	_ = textCmd.MarkFlagRequired("text")
}

func runText(cmd *cobra.Command, _ []string) error {
	id, _ := cmd.Flags().GetString("device")
	text, _ := cmd.Flags().GetString("text")

	mgr, cleanup, err := inputManagerFactory()
	if err != nil {
		return err
	}
	defer cleanup()

	return mgr.InputText(id, text)
}

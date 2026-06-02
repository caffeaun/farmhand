package device

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// fakeADB implements adbDriver with configurable behaviour.
type fakeADB struct {
	devices         []Device
	devErr          error
	wakeErr         error
	rebootErr       error
	batteryLevel    int
	batteryCharging bool
	batteryErr      error
	connectCalls    []string
	connectErr      error
	properties      map[string]string // key = "serial:prop"
	propErr         error

	// Input recorders & error knobs for Tap/Swipe/KeyEvent/InputText.
	tapCalls       []tapCall
	swipeCalls     []swipeCall
	keyEventCalls  []keyEventCall
	inputTextCalls []inputTextCall
	tapErr         error
	swipeErr       error
	keyEventErr    error
	inputTextErr   error

	// Capture recorders & error knobs for Screenshot/Logcat.
	screenshotCalls []string
	screenshotBytes []byte
	screenshotErr   error
	logcatCalls     []logcatCall
	logcatBytes     []byte
	logcatErr       error
}

type logcatCall struct {
	Serial string
	Opts   LogcatOptions
}

type tapCall struct {
	Serial string
	X, Y   int
}
type swipeCall struct {
	Serial                 string
	X1, Y1, X2, Y2, DurMs  int
}
type keyEventCall struct {
	Serial, Keycode string
}
type inputTextCall struct {
	Serial, Text string
}

func (f *fakeADB) Devices() ([]Device, error) {
	return f.devices, f.devErr
}

func (f *fakeADB) GetProperty(serial, prop string) (string, error) {
	if f.propErr != nil {
		return "", f.propErr
	}
	if f.properties != nil {
		if v, ok := f.properties[serial+":"+prop]; ok {
			return v, nil
		}
	}
	return "", nil
}

func (f *fakeADB) Connect(serial string) error {
	f.connectCalls = append(f.connectCalls, serial)
	return f.connectErr
}

func (f *fakeADB) WakeDevice(_ string) error   { return f.wakeErr }
func (f *fakeADB) RebootDevice(_ string) error { return f.rebootErr }
func (f *fakeADB) GetBatteryInfo(_ string) (int, bool, error) {
	return f.batteryLevel, f.batteryCharging, f.batteryErr
}
func (f *fakeADB) Tap(serial string, x, y int) error {
	f.tapCalls = append(f.tapCalls, tapCall{serial, x, y})
	return f.tapErr
}
func (f *fakeADB) Swipe(serial string, x1, y1, x2, y2, durationMs int) error {
	f.swipeCalls = append(f.swipeCalls, swipeCall{serial, x1, y1, x2, y2, durationMs})
	return f.swipeErr
}
func (f *fakeADB) KeyEvent(serial, keycode string) error {
	f.keyEventCalls = append(f.keyEventCalls, keyEventCall{serial, keycode})
	return f.keyEventErr
}
func (f *fakeADB) InputText(serial, text string) error {
	f.inputTextCalls = append(f.inputTextCalls, inputTextCall{serial, text})
	return f.inputTextErr
}
func (f *fakeADB) Screenshot(serial string) ([]byte, error) {
	f.screenshotCalls = append(f.screenshotCalls, serial)
	return f.screenshotBytes, f.screenshotErr
}
func (f *fakeADB) Logcat(serial string, opts LogcatOptions) ([]byte, error) {
	f.logcatCalls = append(f.logcatCalls, logcatCall{serial, opts})
	return f.logcatBytes, f.logcatErr
}

// fakeIOS implements iosDriver with configurable behaviour.
type fakeIOS struct {
	devices []Device
	devErr  error
}

func (f *fakeIOS) Devices() ([]Device, error) {
	return f.devices, f.devErr
}

// fakeSim implements simulatorDriver with configurable behaviour.
type fakeSim struct {
	devices []Device
	devErr  error
}

func (f *fakeSim) Devices() ([]Device, error) {
	return f.devices, f.devErr
}

// openTestDB opens a file-backed SQLite database in t.TempDir().
// File-backed so WAL mode and foreign keys work as in production.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() }) //nolint:errcheck
	return database
}

// newManagerWithFakes builds a Manager backed by fake bridges for unit tests.
// ios may be nil to simulate a non-macOS host.
func newManagerWithFakes(
	adb *fakeADB,
	ios *fakeIOS,
	repo *db.DeviceRepository,
	bus *events.Bus,
	interval time.Duration,
) *Manager {
	var iosI iosDriver
	if ios != nil {
		iosI = ios
	}
	return &Manager{
		adb:          adb,
		ios:          iosI,
		repo:         repo,
		bus:          bus,
		logger:       zerolog.Nop(),
		pollInterval: interval,
	}
}

func makeOnlineDevice(id, platform string) Device {
	now := time.Now().UTC()
	return Device{
		ID:           id,
		Platform:     platform,
		Model:        "TestModel",
		OSVersion:    "1.0",
		Status:       "online",
		BatteryLevel: 80,
		Tags:         nil,
		LastSeen:     now,
		CreatedAt:    now,
	}
}

func TestManager_PollDiscoversAndUpsertsDevices(t *testing.T) {
	adb := &fakeADB{devices: []Device{makeOnlineDevice("adb-1", PlatformAndroid)}}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	got, err := repo.FindByID("adb-1")
	if err != nil {
		t.Fatalf("FindByID after poll: %v", err)
	}
	if got.Platform != PlatformAndroid {
		t.Errorf("Platform = %q, want %q", got.Platform, PlatformAndroid)
	}
	if got.Status != "online" {
		t.Errorf("Status = %q, want online", got.Status)
	}
}

func TestManager_StaleDeviceMarkedOffline(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// Seed a device with a very old last_seen.
	stale := db.Device{
		ID:           "stale-1",
		Platform:     PlatformAndroid,
		Model:        "OldPhone",
		OSVersion:    "9.0",
		Status:       "online",
		BatteryLevel: 50,
		LastSeen:     time.Now().UTC().Add(-10 * time.Minute),
		CreatedAt:    time.Now().UTC().Add(-10 * time.Minute),
	}
	if err := repo.Upsert(stale); err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}

	// ADB returns no devices this poll cycle.
	adb := &fakeADB{devices: nil}

	// pollInterval = 1s → staleThreshold = now - 2s; stale.LastSeen = now - 10m → stale.
	mgr := newManagerWithFakes(adb, nil, repo, bus, 1*time.Second)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	got, err := repo.FindByID("stale-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "offline" {
		t.Errorf("Status = %q, want offline", got.Status)
	}

	// Expect a DeviceOffline event.
	select {
	case ev := <-sub:
		if ev.Type != events.DeviceOffline {
			t.Errorf("event type = %q, want %q", ev.Type, events.DeviceOffline)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for DeviceOffline event")
	}
}

func TestManager_WakeOfflineDeviceReturnsError(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	d := db.Device{
		ID:        "offline-dev",
		Platform:  PlatformAndroid,
		Status:    "offline",
		LastSeen:  time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
	}
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	err := mgr.Wake("offline-dev")
	if err == nil {
		t.Fatal("expected error for offline device, got nil")
	}
}

func TestManager_WakeUnknownDeviceReturnsNotFound(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	err := mgr.Wake("does-not-exist")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestManager_ContextCancellationStopsLoop(t *testing.T) {
	adb := &fakeADB{devices: nil}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// Very short poll interval so the ticker fires quickly.
	mgr := newManagerWithFakes(adb, nil, repo, bus, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	// Start launches a goroutine and returns immediately.
	mgr.Start(ctx)

	// Let it poll a few times then cancel.
	time.Sleep(60 * time.Millisecond)
	cancel()

	// Give the goroutine time to observe ctx.Done() and exit.
	// The goroutine holds mu during poll(); after cancellation the next
	// select will pick ctx.Done() and return.
	time.Sleep(100 * time.Millisecond)

	// If we reach here without a deadlock/panic, the loop stopped cleanly.
}

func TestManager_EventsPublishedOnDeviceOnline(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// First time this device is seen → DeviceOnline event expected.
	adb := &fakeADB{devices: []Device{makeOnlineDevice("evt-1", PlatformAndroid)}}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	select {
	case ev := <-sub:
		if ev.Type != events.DeviceOnline {
			t.Errorf("event type = %q, want %q", ev.Type, events.DeviceOnline)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for DeviceOnline event")
	}
}

func TestManager_IOSBridgeSkippedWhenNil(t *testing.T) {
	adb := &fakeADB{devices: []Device{makeOnlineDevice("adb-only", PlatformAndroid)}}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// nil iOS bridge — must not panic.
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx) // must not panic

	got, err := repo.FindByID("adb-only")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != "adb-only" {
		t.Errorf("ID = %q, want adb-only", got.ID)
	}
}

func TestManager_IOSDevicesDiscovered(t *testing.T) {
	adb := &fakeADB{devices: nil}
	ios := &fakeIOS{devices: []Device{makeOnlineDevice("ios-1", PlatformIOS)}}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	mgr := newManagerWithFakes(adb, ios, repo, bus, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	got, err := repo.FindByID("ios-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Platform != PlatformIOS {
		t.Errorf("Platform = %q, want %q", got.Platform, PlatformIOS)
	}
}

func TestManager_SimulatorDevicesDiscovered(t *testing.T) {
	adb := &fakeADB{devices: nil}
	simDev := makeOnlineDevice("SIMUDID-0001", PlatformIOS)
	simDev.Tags = []string{"simulator"}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)
	mgr.sim = &fakeSim{devices: []Device{simDev}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	got, err := repo.FindByID("SIMUDID-0001")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Platform != PlatformIOS {
		t.Errorf("Platform = %q, want %q", got.Platform, PlatformIOS)
	}
	if got.Status != "online" {
		t.Errorf("Status = %q, want online", got.Status)
	}
	if got.HardwareID != "SIMUDID-0001" {
		t.Errorf("HardwareID = %q, want SIMUDID-0001 (UDID)", got.HardwareID)
	}
	if !deviceHasTag(got.Tags, "simulator") {
		t.Errorf("Tags = %v, want to contain 'simulator'", got.Tags)
	}
}

func TestManager_PollWithNilADBBridge(t *testing.T) {
	// Simulates a macOS host with no adb installed: only a simulator bridge.
	simDev := makeOnlineDevice("SIM-NOADB-1", PlatformIOS)
	simDev.Tags = []string{"simulator"}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	mgr := newManagerWithFakes(nil, nil, repo, bus, 10*time.Second)
	mgr.adb = nil // explicit: no ADB bridge
	mgr.sim = &fakeSim{devices: []Device{simDev}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic despite m.adb being nil.
	mgr.poll(ctx)

	got, err := repo.FindByID("SIM-NOADB-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "online" {
		t.Errorf("Status = %q, want online", got.Status)
	}
}

// deviceHasTag is a small test helper checking tag membership.
func deviceHasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func TestManager_List(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	for _, id := range []string{"l1", "l2", "l3"} {
		d := db.Device{
			ID: id, Platform: PlatformAndroid, Status: "online",
			LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
		}
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("Upsert %s: %v", id, err)
		}
	}

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	all, err := mgr.List(db.DeviceFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len = %d, want 3", len(all))
	}
}

func TestManager_GetByID(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	d := db.Device{
		ID: "get-1", Platform: PlatformIOS, Status: "online",
		LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	got, err := mgr.GetByID("get-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != "get-1" {
		t.Errorf("ID = %q, want get-1", got.ID)
	}
}

func TestManager_ADBBridgeErrorGracefullyHandled(t *testing.T) {
	adb := &fakeADB{devErr: errors.New("adb not running")}

	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic; DB stays empty.
	mgr.poll(ctx)

	all, err := mgr.List(db.DeviceFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 devices, got %d", len(all))
	}
}

func TestManager_DeviceRecoveredFromOfflinePublishesOnline(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// Seed device as offline.
	d := db.Device{
		ID: "recover-1", Platform: PlatformAndroid, Status: "offline",
		LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Now the bridge sees it as online again.
	adb := &fakeADB{devices: []Device{makeOnlineDevice("recover-1", PlatformAndroid)}}
	mgr := newManagerWithFakes(adb, nil, repo, bus, 10*time.Second)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.poll(ctx)

	select {
	case ev := <-sub:
		if ev.Type != events.DeviceOnline {
			t.Errorf("event type = %q, want %q", ev.Type, events.DeviceOnline)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("timeout waiting for DeviceOnline event after device recovery")
	}
}

// --------------------------------------------------------------------------
// isWirelessSerial
// --------------------------------------------------------------------------

func TestIsWirelessSerial(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"192.168.1.50:5555", true},
		{"10.0.0.1:42893", true},
		{"[::1]:5555", true},
		{"ZX1G226B4T", false},
		{"emulator-5554", false},
		{"R38M80ABCDE", false},
		{"192.168.1.50", false},   // bare IP, no port
		{"", false},               // empty
		{"localhost:5555", false}, // hostname, not IP
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got := isWirelessSerial(tc.id)
			if got != tc.want {
				t.Errorf("isWirelessSerial(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Wireless reconnect
// --------------------------------------------------------------------------

func TestPoll_ReconnectsOfflineWirelessDevice(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{devices: nil} // ADB returns no devices

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	// Seed an offline wireless device in the DB.
	_ = repo.Upsert(db.Device{
		ID: "192.168.1.5:5555", Platform: "android", Status: "offline",
		LastSeen: time.Now().UTC(),
	})

	mgr.poll(context.Background())

	if len(adb.connectCalls) != 1 || adb.connectCalls[0] != "192.168.1.5:5555" {
		t.Errorf("connectCalls = %v, want [192.168.1.5:5555]", adb.connectCalls)
	}
}

func TestPoll_DoesNotReconnectUSBDevice(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{devices: nil}

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	// Seed an offline USB device.
	_ = repo.Upsert(db.Device{
		ID: "ZX1G226B4T", Platform: "android", Status: "offline",
		LastSeen: time.Now().UTC(),
	})

	mgr.poll(context.Background())

	if len(adb.connectCalls) != 0 {
		t.Errorf("connectCalls = %v, want empty (USB device should not trigger reconnect)", adb.connectCalls)
	}
}

func TestPoll_ReconnectSkippedWhenADBErrors(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{devErr: errors.New("adb down")}

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	_ = repo.Upsert(db.Device{
		ID: "192.168.1.5:5555", Platform: "android", Status: "offline",
		LastSeen: time.Now().UTC(),
	})

	mgr.poll(context.Background())

	if len(adb.connectCalls) != 0 {
		t.Errorf("connectCalls = %v, want empty (ADB errored)", adb.connectCalls)
	}
}

func TestPoll_ConnectErrorIsNonFatal(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{devices: nil, connectErr: errors.New("failed to connect")}

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	_ = repo.Upsert(db.Device{
		ID: "192.168.1.5:5555", Platform: "android", Status: "offline",
		LastSeen: time.Now().UTC(),
	})

	// Should not panic.
	mgr.poll(context.Background())

	if len(adb.connectCalls) != 1 {
		t.Errorf("connectCalls = %v, want 1 attempt even though it fails", adb.connectCalls)
	}
}

// --------------------------------------------------------------------------
// Hardware ID merge
// --------------------------------------------------------------------------

func TestPoll_MergesDeviceWhenHardwareIDMatchesDifferentSerial(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// Old record: wireless device with old port, has tags.
	_ = repo.Upsert(db.Device{
		ID: "192.168.1.5:42893", Platform: "android", Status: "offline",
		HardwareID: "HW123", Tags: []string{"ci", "prod"},
		LastSeen: time.Now().UTC(),
	})

	// New discovery: same device with new port.
	newDevice := Device{
		ID: "192.168.1.5:38891", Platform: PlatformAndroid, Status: "online",
		LastSeen: time.Now(), CreatedAt: time.Now(),
	}

	adb := &fakeADB{
		devices: []Device{newDevice},
		properties: map[string]string{
			"192.168.1.5:38891:ro.serialno": "HW123",
		},
	}

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)
	mgr.poll(context.Background())

	// Old record should be gone.
	_, err := repo.FindByID("192.168.1.5:42893")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("old record should be deleted, got err = %v", err)
	}

	// New record should exist with tags preserved.
	dev, err := repo.FindByID("192.168.1.5:38891")
	if err != nil {
		t.Fatalf("new record not found: %v", err)
	}
	if dev.HardwareID != "HW123" {
		t.Errorf("HardwareID = %q, want HW123", dev.HardwareID)
	}
	if len(dev.Tags) != 2 || dev.Tags[0] != "ci" || dev.Tags[1] != "prod" {
		t.Errorf("Tags = %v, want [ci prod]", dev.Tags)
	}
}

// --------------------------------------------------------------------------
// Manager.Tap / Swipe / KeyEvent / InputText
// --------------------------------------------------------------------------

// seedOnlineAndroid is a small helper that upserts an online android device.
func seedOnlineAndroid(t *testing.T, repo *db.DeviceRepository, id string) {
	t.Helper()
	if err := repo.Upsert(db.Device{
		ID: id, Platform: PlatformAndroid, Status: "online",
		LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed online android %s: %v", id, err)
	}
}

// seedOnlineIOS is the iOS counterpart used for "unsupported platform" tests.
func seedOnlineIOS(t *testing.T, repo *db.DeviceRepository, id string) {
	t.Helper()
	if err := repo.Upsert(db.Device{
		ID: id, Platform: PlatformIOS, Status: "online",
		LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed online ios %s: %v", id, err)
	}
}

// seedOfflineAndroid is the offline counterpart used for 409-shape tests.
func seedOfflineAndroid(t *testing.T, repo *db.DeviceRepository, id string) {
	t.Helper()
	if err := repo.Upsert(db.Device{
		ID: id, Platform: PlatformAndroid, Status: "offline",
		LastSeen: time.Now().UTC(), CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed offline android %s: %v", id, err)
	}
}

// newManagerWithNilADB returns a manager with no ADB bridge (input must fail
// with "unavailable: ADB bridge not configured").
func newManagerWithNilADB(t *testing.T, repo *db.DeviceRepository, bus *events.Bus) *Manager {
	t.Helper()
	return &Manager{
		adb:          nil,
		repo:         repo,
		bus:          bus,
		logger:       zerolog.Nop(),
		pollInterval: time.Minute,
	}
}

func TestManager_Tap_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.Tap("dev-1", 100, 200); err != nil {
		t.Fatalf("Tap: %v", err)
	}
	if len(adb.tapCalls) != 1 || adb.tapCalls[0] != (tapCall{"dev-1", 100, 200}) {
		t.Errorf("tapCalls = %v, want one call for (dev-1, 100, 200)", adb.tapCalls)
	}
}

func TestManager_Tap_NotFound(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	err := mgr.Tap("does-not-exist", 100, 200)
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestManager_Tap_Offline(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	err := mgr.Tap("off-1", 100, 200)
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("err = %v, want offline-shape error", err)
	}
}

func TestManager_Tap_IOSUnsupported(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineIOS(t, repo, "ios-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	err := mgr.Tap("ios-1", 100, 200)
	if err == nil || !strings.Contains(err.Error(), "not supported for platform ios") {
		t.Errorf("err = %v, want unsupported-platform error for ios", err)
	}
}

func TestManager_Tap_NilADB(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	mgr := newManagerWithNilADB(t, repo, bus)

	err := mgr.Tap("dev-1", 100, 200)
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("err = %v, want not-configured error", err)
	}
}

func TestManager_Swipe_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.Swipe("dev-1", 100, 200, 300, 400, 250); err != nil {
		t.Fatalf("Swipe: %v", err)
	}
	want := swipeCall{"dev-1", 100, 200, 300, 400, 250}
	if len(adb.swipeCalls) != 1 || adb.swipeCalls[0] != want {
		t.Errorf("swipeCalls = %v, want one %v", adb.swipeCalls, want)
	}
}

func TestManager_Swipe_OfflineAndUnsupported(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	seedOnlineIOS(t, repo, "ios-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.Swipe("off-1", 0, 0, 1, 1, 0); err == nil {
		t.Error("expected offline error")
	}
	if err := mgr.Swipe("ios-1", 0, 0, 1, 1, 0); err == nil {
		t.Error("expected unsupported-platform error for ios")
	}
	if err := mgr.Swipe("nope", 0, 0, 1, 1, 0); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestManager_KeyEvent_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.KeyEvent("dev-1", "KEYCODE_BACK"); err != nil {
		t.Fatalf("KeyEvent: %v", err)
	}
	if len(adb.keyEventCalls) != 1 || adb.keyEventCalls[0] != (keyEventCall{"dev-1", "KEYCODE_BACK"}) {
		t.Errorf("keyEventCalls = %v, want one (dev-1, KEYCODE_BACK)", adb.keyEventCalls)
	}
}

func TestManager_KeyEvent_OfflineUnsupportedNilADB(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	seedOnlineIOS(t, repo, "ios-1")
	seedOnlineAndroid(t, repo, "nil-adb-target")

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.KeyEvent("off-1", "4"); err == nil {
		t.Error("expected offline error")
	}
	if err := mgr.KeyEvent("ios-1", "4"); err == nil {
		t.Error("expected unsupported-platform error for ios")
	}

	nilMgr := newManagerWithNilADB(t, repo, bus)
	if err := nilMgr.KeyEvent("nil-adb-target", "4"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("err = %v, want not-configured error", err)
	}
}

func TestManager_InputText_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.InputText("dev-1", "hello world"); err != nil {
		t.Fatalf("InputText: %v", err)
	}
	if len(adb.inputTextCalls) != 1 || adb.inputTextCalls[0] != (inputTextCall{"dev-1", "hello world"}) {
		t.Errorf("inputTextCalls = %v, want one (dev-1, hello world)", adb.inputTextCalls)
	}
}

func TestManager_InputText_OfflineUnsupported(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	seedOnlineIOS(t, repo, "ios-1")

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if err := mgr.InputText("off-1", "x"); err == nil {
		t.Error("expected offline error")
	}
	if err := mgr.InputText("ios-1", "x"); err == nil {
		t.Error("expected unsupported-platform error for ios")
	}
	if err := mgr.InputText("nope", "x"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --------------------------------------------------------------------------
// Manager.Screenshot / Logcat
// --------------------------------------------------------------------------

func TestManager_Screenshot_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	wantBytes := []byte{0x89, 'P', 'N', 'G'}
	adb := &fakeADB{screenshotBytes: wantBytes}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	got, err := mgr.Screenshot("dev-1")
	if err != nil {
		t.Fatalf("Screenshot: %v", err)
	}
	if string(got) != string(wantBytes) {
		t.Errorf("bytes = %q, want %q", got, wantBytes)
	}
	if len(adb.screenshotCalls) != 1 || adb.screenshotCalls[0] != "dev-1" {
		t.Errorf("screenshotCalls = %v, want [dev-1]", adb.screenshotCalls)
	}
}

func TestManager_Screenshot_FiveGuards(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	seedOnlineIOS(t, repo, "ios-1")
	seedOnlineAndroid(t, repo, "nil-adb-target")

	adb := &fakeADB{screenshotBytes: []byte("x")}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	// not found
	if _, err := mgr.Screenshot("does-not-exist"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("not-found: err = %v, want ErrNotFound", err)
	}
	// offline
	if _, err := mgr.Screenshot("off-1"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline: err = %v, want offline-shape", err)
	}
	// iOS unsupported
	if _, err := mgr.Screenshot("ios-1"); err == nil || !strings.Contains(err.Error(), "not supported for platform ios") {
		t.Errorf("ios: err = %v, want unsupported-platform", err)
	}
	// nil ADB
	nilMgr := newManagerWithNilADB(t, repo, bus)
	if _, err := nilMgr.Screenshot("nil-adb-target"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("nil adb: err = %v, want not-configured", err)
	}
}

func TestManager_Logcat_HappyPath(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOnlineAndroid(t, repo, "dev-1")
	want := []byte("01-01 12:00:00.000  1234  5678 I MainActivity: hello\n")
	adb := &fakeADB{logcatBytes: want}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	opts := LogcatOptions{Since: time.Minute, Filter: "E"}
	got, err := mgr.Logcat("dev-1", opts)
	if err != nil {
		t.Fatalf("Logcat: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("bytes = %q, want %q", got, want)
	}
	if len(adb.logcatCalls) != 1 || adb.logcatCalls[0] != (logcatCall{"dev-1", opts}) {
		t.Errorf("logcatCalls = %v, want one (dev-1, %+v)", adb.logcatCalls, opts)
	}
}

func TestManager_Logcat_FiveGuards(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	seedOfflineAndroid(t, repo, "off-1")
	seedOnlineIOS(t, repo, "ios-1")
	seedOnlineAndroid(t, repo, "nil-adb-target")

	adb := &fakeADB{}
	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)

	if _, err := mgr.Logcat("nope", LogcatOptions{}); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("not-found: err = %v, want ErrNotFound", err)
	}
	if _, err := mgr.Logcat("off-1", LogcatOptions{}); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline: err = %v, want offline-shape", err)
	}
	if _, err := mgr.Logcat("ios-1", LogcatOptions{}); err == nil || !strings.Contains(err.Error(), "not supported for platform ios") {
		t.Errorf("ios: err = %v, want unsupported-platform", err)
	}
	nilMgr := newManagerWithNilADB(t, repo, bus)
	if _, err := nilMgr.Logcat("nil-adb-target", LogcatOptions{}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("nil adb: err = %v, want not-configured", err)
	}
}

func TestPoll_HardwareIDEmptyDoesNotMerge(t *testing.T) {
	database := openTestDB(t)
	repo := db.NewDeviceRepository(database)
	bus := events.New()
	defer bus.Close()

	// Existing device.
	_ = repo.Upsert(db.Device{
		ID: "192.168.1.5:42893", Platform: "android", Status: "offline",
		LastSeen: time.Now().UTC(),
	})

	// New device with no hardware_id (GetProperty returns empty).
	newDevice := Device{
		ID: "192.168.1.5:38891", Platform: PlatformAndroid, Status: "online",
		LastSeen: time.Now(), CreatedAt: time.Now(),
	}

	adb := &fakeADB{devices: []Device{newDevice}}

	mgr := newManagerWithFakes(adb, nil, repo, bus, time.Minute)
	mgr.poll(context.Background())

	// Both records should exist — no merge without hardware_id.
	_, err1 := repo.FindByID("192.168.1.5:42893")
	_, err2 := repo.FindByID("192.168.1.5:38891")
	if err1 != nil {
		t.Errorf("old record should still exist: %v", err1)
	}
	if err2 != nil {
		t.Errorf("new record should exist: %v", err2)
	}
}

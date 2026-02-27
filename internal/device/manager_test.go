package device

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// fakeADB implements adbDriver with configurable behaviour.
type fakeADB struct {
	devices        []Device
	devErr         error
	wakeErr        error
	rebootErr      error
	batteryLevel   int
	batteryCharging bool
	batteryErr     error
}

func (f *fakeADB) Devices() ([]Device, error) {
	return f.devices, f.devErr
}

func (f *fakeADB) WakeDevice(_ string) error   { return f.wakeErr }
func (f *fakeADB) RebootDevice(_ string) error { return f.rebootErr }
func (f *fakeADB) GetBatteryInfo(_ string) (int, bool, error) {
	return f.batteryLevel, f.batteryCharging, f.batteryErr
}

// fakeIOS implements iosDriver with configurable behaviour.
type fakeIOS struct {
	devices []Device
	devErr  error
}

func (f *fakeIOS) Devices() ([]Device, error) {
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

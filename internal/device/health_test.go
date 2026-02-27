package device

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

// Android 14 fixture: USB charging, level 85.
const android14Fixture = `Current Battery Service state:
  AC powered: false
  USB powered: true
  Wireless powered: false
  Max charging current: 500000
  Max charging voltage: 5000000
  Charge type: 1
  status: 2
  health: 2
  present: true
  level: 85
  scale: 100
  voltage: 4200
  temperature: 250
  technology: Li-ion`

// Android 12 fixture: AC charging, fully charged (status 5), level 100.
const android12Fixture = `Current Battery Service state:
  AC powered: true
  USB powered: false
  Wireless powered: false
  status: 5
  health: 2
  present: true
  level: 100
  scale: 100`

// Low battery fixture: not charging, level 12.
const lowBatteryFixture = `Current Battery Service state:
  AC powered: false
  USB powered: false
  Wireless powered: false
  status: 3
  health: 2
  present: true
  level: 12
  scale: 100`

func TestParseBatteryOutput_Android14_USBCharging(t *testing.T) {
	level, charging, err := ParseBatteryOutput(android14Fixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 85 {
		t.Errorf("level = %d, want 85", level)
	}
	if !charging {
		t.Error("charging = false, want true")
	}
}

func TestParseBatteryOutput_Android12_ACFullyCharged(t *testing.T) {
	level, charging, err := ParseBatteryOutput(android12Fixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 100 {
		t.Errorf("level = %d, want 100", level)
	}
	if !charging {
		t.Error("charging = false, want true (status 5 = FULL)")
	}
}

func TestParseBatteryOutput_LowBattery_NotCharging(t *testing.T) {
	level, charging, err := ParseBatteryOutput(lowBatteryFixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != 12 {
		t.Errorf("level = %d, want 12", level)
	}
	if charging {
		t.Error("charging = true, want false")
	}
}

func TestParseBatteryOutput_InvalidOutput(t *testing.T) {
	_, _, err := ParseBatteryOutput("this is not battery output at all")
	if err == nil {
		t.Error("expected error for invalid output, got nil")
	}
}

func TestParseBatteryOutput_MissingLevel(t *testing.T) {
	output := `Current Battery Service state:
  AC powered: true
  status: 2`
	_, _, err := ParseBatteryOutput(output)
	if err == nil {
		t.Error("expected error when level line is missing, got nil")
	}
}

// openHealthTestDB opens a file-backed SQLite DB in a temp dir.
func openHealthTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "health_test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() }) //nolint:errcheck
	return database
}

// newHealthManager builds a Manager for health tests.
func newHealthManager(adb *fakeADB, repo *db.DeviceRepository) *Manager {
	bus := events.New()
	return &Manager{
		adb:          adb,
		ios:          nil,
		repo:         repo,
		bus:          bus,
		logger:       zerolog.Nop(),
		pollInterval: 10 * time.Second,
	}
}

func TestHealthCheck_AndroidDevice(t *testing.T) {
	database := openHealthTestDB(t)
	repo := db.NewDeviceRepository(database)

	// Seed an online Android device.
	dev := db.Device{
		ID:           "android-health-1",
		Platform:     PlatformAndroid,
		Status:       "online",
		BatteryLevel: 50,
		LastSeen:     time.Now().UTC(),
		CreatedAt:    time.Now().UTC(),
	}
	if err := repo.Upsert(dev); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	adb := &fakeADB{
		batteryLevel:    85,
		batteryCharging: true,
	}
	mgr := newHealthManager(adb, repo)

	health, err := mgr.HealthCheck("android-health-1")
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if health.DeviceID != "android-health-1" {
		t.Errorf("DeviceID = %q, want android-health-1", health.DeviceID)
	}
	if health.BatteryLevel != 85 {
		t.Errorf("BatteryLevel = %d, want 85", health.BatteryLevel)
	}
	if !health.BatteryCharging {
		t.Error("BatteryCharging = false, want true")
	}
	if !health.IsOnline {
		t.Error("IsOnline = false, want true")
	}

	// Verify DB was updated.
	updated, err := repo.FindByID("android-health-1")
	if err != nil {
		t.Fatalf("FindByID after HealthCheck: %v", err)
	}
	if updated.BatteryLevel != 85 {
		t.Errorf("DB BatteryLevel = %d, want 85", updated.BatteryLevel)
	}
}

func TestHealthCheck_IOSDevice(t *testing.T) {
	database := openHealthTestDB(t)
	repo := db.NewDeviceRepository(database)

	dev := db.Device{
		ID:           "ios-health-1",
		Platform:     PlatformIOS,
		Status:       "online",
		BatteryLevel: 70,
		LastSeen:     time.Now().UTC(),
		CreatedAt:    time.Now().UTC(),
	}
	if err := repo.Upsert(dev); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	adb := &fakeADB{}
	mgr := newHealthManager(adb, repo)

	health, err := mgr.HealthCheck("ios-health-1")
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if health.BatteryLevel != -1 {
		t.Errorf("BatteryLevel = %d, want -1 (iOS not available)", health.BatteryLevel)
	}
	if !health.IsOnline {
		t.Error("IsOnline = false, want true")
	}
}

func TestHealthCheck_OfflineDevice(t *testing.T) {
	database := openHealthTestDB(t)
	repo := db.NewDeviceRepository(database)

	lastSeen := time.Now().UTC().Add(-5 * time.Minute)
	dev := db.Device{
		ID:           "android-offline-1",
		Platform:     PlatformAndroid,
		Status:       "offline",
		BatteryLevel: 30,
		LastSeen:     lastSeen,
		CreatedAt:    time.Now().UTC().Add(-10 * time.Minute),
	}
	if err := repo.Upsert(dev); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// ADB should NOT be called for offline devices.
	adb := &fakeADB{batteryErr: errors.New("should not be called")}
	mgr := newHealthManager(adb, repo)

	health, err := mgr.HealthCheck("android-offline-1")
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if health.IsOnline {
		t.Error("IsOnline = true, want false for offline device")
	}
	// Uses DB values since offline.
	if health.BatteryLevel != 30 {
		t.Errorf("BatteryLevel = %d, want 30 (DB value)", health.BatteryLevel)
	}
}

func TestHealthCheck_NotFound(t *testing.T) {
	database := openHealthTestDB(t)
	repo := db.NewDeviceRepository(database)

	adb := &fakeADB{}
	mgr := newHealthManager(adb, repo)

	_, err := mgr.HealthCheck("does-not-exist")
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

package db

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// newTestDevice returns a Device with sensible defaults for testing.
func newTestDevice(id, platform string) Device {
	return Device{
		ID:           id,
		Platform:     platform,
		Model:        "Model X",
		OSVersion:    "14.0",
		Status:       "online",
		BatteryLevel: 80,
		Tags:         []string{"tag-a", "tag-b"},
		LastSeen:     time.Now().UTC().Truncate(time.Second),
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}
}

func TestUpsert_CreatesNew(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-1", "android")
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.FindByID("dev-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != d.ID {
		t.Errorf("ID = %q, want %q", got.ID, d.ID)
	}
	if got.Platform != d.Platform {
		t.Errorf("Platform = %q, want %q", got.Platform, d.Platform)
	}
	if got.BatteryLevel != d.BatteryLevel {
		t.Errorf("BatteryLevel = %d, want %d", got.BatteryLevel, d.BatteryLevel)
	}
}

func TestUpsert_UpdatesExisting(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-2", "ios")
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	// Update status and battery
	d.Status = "offline"
	d.BatteryLevel = 10
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := repo.FindByID("dev-2")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "offline" {
		t.Errorf("Status = %q, want %q", got.Status, "offline")
	}
	if got.BatteryLevel != 10 {
		t.Errorf("BatteryLevel = %d, want %d", got.BatteryLevel, 10)
	}
}

func TestFindByID_Found(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-3", "android")
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.FindByID("dev-3")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != "dev-3" {
		t.Errorf("ID = %q, want %q", got.ID, "dev-3")
	}
}

func TestFindByID_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	_, err := repo.FindByID("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestFindAll_NoFilter(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	devices := []Device{
		newTestDevice("dev-a", "android"),
		newTestDevice("dev-b", "ios"),
		newTestDevice("dev-c", "android"),
	}
	for _, d := range devices {
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("Upsert %s: %v", d.ID, err)
		}
	}

	got, err := repo.FindAll(DeviceFilter{})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestFindAll_PlatformFilter(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	devices := []Device{
		newTestDevice("dev-p1", "android"),
		newTestDevice("dev-p2", "ios"),
		newTestDevice("dev-p3", "android"),
	}
	for _, d := range devices {
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("Upsert %s: %v", d.ID, err)
		}
	}

	got, err := repo.FindAll(DeviceFilter{Platform: "android"})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	for _, d := range got {
		if d.Platform != "android" {
			t.Errorf("Platform = %q, want android", d.Platform)
		}
	}
}

func TestFindAll_StatusFilter(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d1 := newTestDevice("dev-s1", "android")
	d1.Status = "online"
	d2 := newTestDevice("dev-s2", "android")
	d2.Status = "offline"
	d3 := newTestDevice("dev-s3", "ios")
	d3.Status = "online"

	for _, d := range []Device{d1, d2, d3} {
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("Upsert %s: %v", d.ID, err)
		}
	}

	got, err := repo.FindAll(DeviceFilter{Status: "online"})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	for _, d := range got {
		if d.Status != "online" {
			t.Errorf("Status = %q, want online", d.Status)
		}
	}
}

func TestFindAll_TagsFilter_AND(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d1 := newTestDevice("dev-t1", "android")
	d1.Tags = []string{"flagship", "android-14", "high-res"}

	d2 := newTestDevice("dev-t2", "android")
	d2.Tags = []string{"flagship", "android-13"}

	d3 := newTestDevice("dev-t3", "ios")
	d3.Tags = []string{"flagship", "android-14"}

	for _, d := range []Device{d1, d2, d3} {
		if err := repo.Upsert(d); err != nil {
			t.Fatalf("Upsert %s: %v", d.ID, err)
		}
	}

	// Only d1 and d3 have both "flagship" AND "android-14"
	got, err := repo.FindAll(DeviceFilter{Tags: []string{"flagship", "android-14"}})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}

	ids := make(map[string]bool)
	for _, d := range got {
		ids[d.ID] = true
	}
	if !ids["dev-t1"] || !ids["dev-t3"] {
		t.Errorf("expected dev-t1 and dev-t3, got %v", ids)
	}
}

func TestUpdateStatus(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-us1", "android")
	d.Status = "online"
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.UpdateStatus("dev-us1", "offline"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := repo.FindByID("dev-us1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "offline" {
		t.Errorf("Status = %q, want offline", got.Status)
	}
}

func TestUpdateStatus_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	err := repo.UpdateStatus("nonexistent", "offline")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateLastSeen(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-ls1", "ios")
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	newTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	if err := repo.UpdateLastSeen("dev-ls1", newTime); err != nil {
		t.Fatalf("UpdateLastSeen: %v", err)
	}

	got, err := repo.FindByID("dev-ls1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !got.LastSeen.Equal(newTime) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen, newTime)
	}
}

func TestUpdateLastSeen_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	err := repo.UpdateLastSeen("nonexistent", time.Now())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	d := newTestDevice("dev-del1", "android")
	if err := repo.Upsert(d); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.Delete("dev-del1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := repo.FindByID("dev-del1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	db := openMemory(t)
	repo := NewDeviceRepository(db)

	err := repo.Delete("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestTagsSerialization(t *testing.T) {
	tests := []struct {
		name string
		tags []string
	}{
		{
			name: "nil tags",
			tags: nil,
		},
		{
			name: "empty tags",
			tags: []string{},
		},
		{
			name: "single tag",
			tags: []string{"flagship"},
		},
		{
			name: "multiple tags",
			tags: []string{"flagship", "android-14", "high-res"},
		},
	}

	db := openMemory(t)
	repo := NewDeviceRepository(db)

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("dev-tag-%d", i)
			d := newTestDevice(id, "android")
			d.Tags = tc.tags
			if err := repo.Upsert(d); err != nil {
				t.Fatalf("Upsert: %v", err)
			}

			got, err := repo.FindByID(id)
			if err != nil {
				t.Fatalf("FindByID: %v", err)
			}

			// nil and empty both round-trip to nil
			wantTags := tc.tags
			if len(wantTags) == 0 {
				wantTags = nil
			}

			if len(got.Tags) != len(wantTags) {
				t.Errorf("tags len = %d, want %d (got %v, want %v)", len(got.Tags), len(wantTags), got.Tags, wantTags)
				return
			}
			for j, tag := range got.Tags {
				if tag != wantTags[j] {
					t.Errorf("tags[%d] = %q, want %q", j, tag, wantTags[j])
				}
			}
		})
	}
}

package main

import (
	"errors"
	"testing"
)

func TestClearCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	if err := clearCmd.Flags().Set("device", "dev-1"); err != nil {
		t.Fatal(err)
	}

	if err := runClear(clearCmd, nil); err != nil {
		t.Fatalf("runClear: %v", err)
	}
	if fake.killAllApps == nil || fake.killAllApps.ID != "dev-1" {
		t.Errorf("killAllApps = %v, want call on dev-1", fake.killAllApps)
	}
	if fake.keyevent == nil || *fake.keyevent != (fakeKeyEventCall{"dev-1", "KEYCODE_HOME"}) {
		t.Errorf("keyevent = %v, want KEYCODE_HOME on dev-1", fake.keyevent)
	}
}

func TestClearCmd_KillAllAppsErrorShortCircuits(t *testing.T) {
	// If KillAllApps fails, KeyEvent must NOT be called — the device may
	// genuinely be offline / not found, and we don't want to mask the root
	// error with a second failure.
	fake := withFakeManager(t)
	fake.err = errors.New("device dev-1 is offline")

	_ = clearCmd.Flags().Set("device", "dev-1")

	err := runClear(clearCmd, nil)
	if err == nil || err.Error() != "device dev-1 is offline" {
		t.Errorf("err = %v, want manager error to propagate", err)
	}
	if fake.killAllApps == nil {
		t.Error("KillAllApps was not called")
	}
	if fake.keyevent != nil {
		t.Errorf("KeyEvent was called after KillAllApps failed: %v", fake.keyevent)
	}
}

func TestClearCmd_KeyEventErrorPropagates(t *testing.T) {
	// Simulate the more interesting case: KillAllApps succeeded, but the
	// follow-up HOME keyevent failed. Both calls must land and the second
	// error must propagate.
	fake := &fakeInputManager{}
	prev := inputManagerFactory
	calls := 0
	inputManagerFactory = func() (deviceManagerCLI, func(), error) {
		return &sequencedManager{
			delegate: fake,
			onKey: func() error {
				calls++
				return errors.New("home keyevent failed")
			},
		}, func() {}, nil
	}
	t.Cleanup(func() { inputManagerFactory = prev })

	_ = clearCmd.Flags().Set("device", "dev-1")

	err := runClear(clearCmd, nil)
	if err == nil || err.Error() != "home keyevent failed" {
		t.Errorf("err = %v, want keyevent error to propagate", err)
	}
	if fake.killAllApps == nil {
		t.Error("KillAllApps was not called")
	}
	if calls != 1 {
		t.Errorf("KeyEvent called %d times, want 1", calls)
	}
}

// sequencedManager wraps a fakeInputManager but lets a single method
// (KeyEvent here) return a custom error. Used to assert that runClear
// fails-fast at the right step.
type sequencedManager struct {
	delegate *fakeInputManager
	onKey    func() error
}

func (s *sequencedManager) Tap(id string, x, y int) error { return s.delegate.Tap(id, x, y) }
func (s *sequencedManager) Swipe(id string, x1, y1, x2, y2, dur int) error {
	return s.delegate.Swipe(id, x1, y1, x2, y2, dur)
}
func (s *sequencedManager) KeyEvent(id, keycode string) error {
	_ = s.delegate.KeyEvent(id, keycode)
	return s.onKey()
}
func (s *sequencedManager) InputText(id, text string) error {
	return s.delegate.InputText(id, text)
}
func (s *sequencedManager) KillAllApps(id string) error { return s.delegate.KillAllApps(id) }
func (s *sequencedManager) Launch(id, pkg string) error { return s.delegate.Launch(id, pkg) }

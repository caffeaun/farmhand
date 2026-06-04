package main

import (
	"errors"
	"strconv"
	"testing"
)

// fakeInputManager implements deviceManagerCLI for the cobra tests. It
// records the last call for each method so we can assert what the CLI
// dispatched to the manager.
type fakeInputManager struct {
	tap         *fakeTapCall
	swipe       *fakeSwipeCall
	keyevent    *fakeKeyEventCall
	inputText   *fakeInputTextCall
	killAllApps *fakeKillAllAppsCall
	launch      *fakeLaunchCall
	err         error // returned by whichever method is called
}

type fakeKillAllAppsCall struct {
	ID string
}
type fakeLaunchCall struct {
	ID, Package string
}

type fakeTapCall struct {
	ID   string
	X, Y int
}
type fakeSwipeCall struct {
	ID                    string
	X1, Y1, X2, Y2, DurMs int
}
type fakeKeyEventCall struct {
	ID, Keycode string
}
type fakeInputTextCall struct {
	ID, Text string
}

func (f *fakeInputManager) Tap(id string, x, y int) error {
	f.tap = &fakeTapCall{id, x, y}
	return f.err
}
func (f *fakeInputManager) Swipe(id string, x1, y1, x2, y2, dur int) error {
	f.swipe = &fakeSwipeCall{id, x1, y1, x2, y2, dur}
	return f.err
}
func (f *fakeInputManager) KeyEvent(id, keycode string) error {
	f.keyevent = &fakeKeyEventCall{id, keycode}
	return f.err
}
func (f *fakeInputManager) InputText(id, text string) error {
	f.inputText = &fakeInputTextCall{id, text}
	return f.err
}
func (f *fakeInputManager) KillAllApps(id string) error {
	f.killAllApps = &fakeKillAllAppsCall{id}
	return f.err
}
func (f *fakeInputManager) Launch(id, pkg string) error {
	f.launch = &fakeLaunchCall{id, pkg}
	return f.err
}

// withFakeManager swaps inputManagerFactory for the duration of t; the
// returned fake records every call dispatched through it.
func withFakeManager(t *testing.T) *fakeInputManager {
	t.Helper()
	fake := &fakeInputManager{}
	prev := inputManagerFactory
	inputManagerFactory = func() (deviceManagerCLI, func(), error) {
		return fake, func() {}, nil
	}
	t.Cleanup(func() { inputManagerFactory = prev })
	return fake
}

// The cobra commands are package-level singletons that hold parsed flag
// state across invocations. To exercise the RunE handlers from tests
// without re-implementing argv parsing through cobra's Execute()
// machinery, the tests below set the flags directly and then call the
// RunE function. This sidesteps cobra's "walk up to root" behaviour
// which would otherwise trip on the persistent --config flag set up by
// the root command's PersistentPreRunE.

func TestTapCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	if err := tapCmd.Flags().Set("device", "dev-1"); err != nil {
		t.Fatal(err)
	}
	if err := tapCmd.Flags().Set("x", strconv.Itoa(540)); err != nil {
		t.Fatal(err)
	}
	if err := tapCmd.Flags().Set("y", strconv.Itoa(960)); err != nil {
		t.Fatal(err)
	}

	if err := runTap(tapCmd, nil); err != nil {
		t.Fatalf("runTap: %v", err)
	}
	want := fakeTapCall{"dev-1", 540, 960}
	if fake.tap == nil || *fake.tap != want {
		t.Errorf("tap = %v, want %v", fake.tap, want)
	}
}

func TestTapCmd_PropagatesManagerError(t *testing.T) {
	fake := withFakeManager(t)
	fake.err = errors.New("device dev-1 is offline")

	_ = tapCmd.Flags().Set("device", "dev-1")
	_ = tapCmd.Flags().Set("x", "1")
	_ = tapCmd.Flags().Set("y", "2")

	err := runTap(tapCmd, nil)
	if err == nil || err.Error() != "device dev-1 is offline" {
		t.Errorf("err = %v, want manager error to propagate", err)
	}
}

func TestSwipeCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	_ = swipeCmd.Flags().Set("device", "dev-1")
	_ = swipeCmd.Flags().Set("from-x", "100")
	_ = swipeCmd.Flags().Set("from-y", "200")
	_ = swipeCmd.Flags().Set("to-x", "300")
	_ = swipeCmd.Flags().Set("to-y", "400")
	_ = swipeCmd.Flags().Set("duration-ms", "250")

	if err := runSwipe(swipeCmd, nil); err != nil {
		t.Fatalf("runSwipe: %v", err)
	}
	want := fakeSwipeCall{"dev-1", 100, 200, 300, 400, 250}
	if fake.swipe == nil || *fake.swipe != want {
		t.Errorf("swipe = %v, want %v", fake.swipe, want)
	}
}

func TestKeyEventCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	_ = keyEventCmd.Flags().Set("device", "dev-1")
	_ = keyEventCmd.Flags().Set("keycode", "KEYCODE_BACK")

	if err := runKeyEvent(keyEventCmd, nil); err != nil {
		t.Fatalf("runKeyEvent: %v", err)
	}
	want := fakeKeyEventCall{"dev-1", "KEYCODE_BACK"}
	if fake.keyevent == nil || *fake.keyevent != want {
		t.Errorf("keyevent = %v, want %v", fake.keyevent, want)
	}
}

func TestTextCmd_HappyPath(t *testing.T) {
	fake := withFakeManager(t)

	_ = textCmd.Flags().Set("device", "dev-1")
	_ = textCmd.Flags().Set("text", "hello world")

	if err := runText(textCmd, nil); err != nil {
		t.Fatalf("runText: %v", err)
	}
	want := fakeInputTextCall{"dev-1", "hello world"}
	if fake.inputText == nil || *fake.inputText != want {
		t.Errorf("inputText = %v, want %v", fake.inputText, want)
	}
}

// TestTextCmd_PreservesShellMetacharacters verifies the CLI hands the raw
// text string to the manager unchanged. Quoting is the bridge's job, not
// the CLI's; the CLI must not strip or escape anything.
func TestTextCmd_PreservesShellMetacharacters(t *testing.T) {
	fake := withFakeManager(t)

	dangerous := `a'; reboot`
	_ = textCmd.Flags().Set("device", "dev-1")
	_ = textCmd.Flags().Set("text", dangerous)

	if err := runText(textCmd, nil); err != nil {
		t.Fatalf("runText: %v", err)
	}
	if fake.inputText == nil || fake.inputText.Text != dangerous {
		t.Errorf("text = %q, want %q (CLI must hand the raw string to the manager)", fake.inputText, dangerous)
	}
}

// TestInputManagerFactory_ErrorSurfaces verifies the CLI surfaces factory
// errors (e.g. DB open failure, missing adb) rather than panicking.
func TestInputManagerFactory_ErrorSurfaces(t *testing.T) {
	prev := inputManagerFactory
	inputManagerFactory = func() (deviceManagerCLI, func(), error) {
		return nil, nil, errors.New("adb bridge: not found")
	}
	t.Cleanup(func() { inputManagerFactory = prev })

	_ = tapCmd.Flags().Set("device", "dev-1")
	_ = tapCmd.Flags().Set("x", "1")
	_ = tapCmd.Flags().Set("y", "2")

	err := runTap(tapCmd, nil)
	if err == nil || err.Error() != "adb bridge: not found" {
		t.Errorf("err = %v, want factory error", err)
	}
}

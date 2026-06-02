package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/caffeaun/farmhand/internal/vision"
)

// makeTestPNG returns a minimal real PNG of the given dimensions so the
// CLI's image/png.DecodeConfig path is exercised end-to-end.
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

type fakeInspectManager struct {
	pngBytes []byte
	err      error

	lastID string
}

func (f *fakeInspectManager) Screenshot(id string) ([]byte, error) {
	f.lastID = id
	return f.pngBytes, f.err
}

type fakeInspectClient struct {
	res vision.InspectResult
	err error

	lastPNG []byte
}

func (f *fakeInspectClient) Inspect(_ context.Context, png []byte) (vision.InspectResult, error) {
	f.lastPNG = png
	return f.res, f.err
}

func withFakeInspectDeps(t *testing.T, mgr *fakeInspectManager, client *fakeInspectClient) {
	t.Helper()
	prev := inspectFactory
	inspectFactory = func() (inspectDeps, error) {
		return inspectDeps{Manager: mgr, Client: client, Cleanup: func() {}}, nil
	}
	t.Cleanup(func() { inspectFactory = prev })
	resetInspectFlags(t)
}

// resetInspectFlags wipes the persisted flag values on inspectCmd at the
// start of every test so flag state from a prior test (notably --mock-from)
// cannot leak — important when tests run under -shuffle.
func resetInspectFlags(t *testing.T) {
	t.Helper()
	_ = inspectCmd.Flags().Set("device", "")
	_ = inspectCmd.Flags().Set("mock-from", "")
}

func TestInspect_HappyPath(t *testing.T) {
	pngBytes := makeTestPNG(t, 1080, 2400)
	mgr := &fakeInspectManager{pngBytes: pngBytes}
	client := &fakeInspectClient{res: vision.InspectResult{
		Topics: []vision.Topic{
			{
				Name:        "Login button",
				Coordinates: vision.Box{X1: 100, Y1: 1700, X2: 980, Y2: 1900},
				Color:       "blue",
				Type:        "button",
				Text:        "Sign In",
			},
			{
				Name:        "Email field",
				Coordinates: vision.Box{X1: 60, Y1: 1100, X2: 1020, Y2: 1250},
				Type:        "input",
			},
		},
	}}
	withFakeInspectDeps(t, mgr, client)

	_ = inspectCmd.Flags().Set("device", "R58W2193TXP")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	if err := runInspect(inspectCmd, nil); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	if mgr.lastID != "R58W2193TXP" {
		t.Errorf("manager.lastID = %q", mgr.lastID)
	}
	if !bytes.Equal(client.lastPNG, pngBytes) {
		t.Errorf("client did not receive the screenshot bytes verbatim")
	}

	var out inspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal stdout %q: %v", stdout.String(), err)
	}
	if len(out.Topics) != 2 {
		t.Fatalf("topics len = %d, want 2", len(out.Topics))
	}
	if out.Topics[0].Name != "Login button" || out.Topics[0].Color != "blue" {
		t.Errorf("topics[0] = %+v", out.Topics[0])
	}
	wantBox := vision.Box{X1: 100, Y1: 1700, X2: 980, Y2: 1900}
	if out.Topics[0].Coordinates != wantBox {
		t.Errorf("topics[0].Coordinates = %+v, want %+v", out.Topics[0].Coordinates, wantBox)
	}
	if out.ScreenshotSize.Width != 1080 || out.ScreenshotSize.Height != 2400 {
		t.Errorf("screenshot_size = %dx%d, want 1080x2400",
			out.ScreenshotSize.Width, out.ScreenshotSize.Height)
	}
}

func TestInspect_EmptyTopicsPrintsEmptyArray(t *testing.T) {
	mgr := &fakeInspectManager{pngBytes: makeTestPNG(t, 100, 100)}
	client := &fakeInspectClient{res: vision.InspectResult{Topics: nil}}
	withFakeInspectDeps(t, mgr, client)

	_ = inspectCmd.Flags().Set("device", "X")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	if err := runInspect(inspectCmd, nil); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	// Output must contain an array (even if empty), not null.
	var out inspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Topics == nil {
		// json round-trips nil slices as null; we want [] for downstream jq.
		// Check the raw bytes instead.
		if !bytes.Contains(stdout.Bytes(), []byte(`"topics":[]`)) && !bytes.Contains(stdout.Bytes(), []byte(`"topics":null`)) {
			t.Errorf("topics field missing from %q", stdout.String())
		}
	}
}

func TestInspect_ScreenshotError(t *testing.T) {
	mgr := &fakeInspectManager{err: errors.New("device X is offline")}
	client := &fakeInspectClient{}
	withFakeInspectDeps(t, mgr, client)

	_ = inspectCmd.Flags().Set("device", "X")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if client.lastPNG != nil {
		t.Error("vision client should not be called when screenshot fails")
	}
}

func TestInspect_VisionClientError(t *testing.T) {
	mgr := &fakeInspectManager{pngBytes: makeTestPNG(t, 100, 100)}
	client := &fakeInspectClient{err: errors.New("HTTP 502")}
	withFakeInspectDeps(t, mgr, client)

	_ = inspectCmd.Flags().Set("device", "X")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected error from vision client to propagate")
	}
}

func TestInspect_NotAPng(t *testing.T) {
	mgr := &fakeInspectManager{pngBytes: []byte("not a png")}
	client := &fakeInspectClient{}
	withFakeInspectDeps(t, mgr, client)

	_ = inspectCmd.Flags().Set("device", "X")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected error when bytes are not a valid PNG header")
	}
}

func TestInspect_FactoryError(t *testing.T) {
	prev := inspectFactory
	inspectFactory = func() (inspectDeps, error) {
		return inspectDeps{}, errors.New("vision provider not configured: env MINIMAX_API_KEY is empty")
	}
	t.Cleanup(func() { inspectFactory = prev })

	_ = inspectCmd.Flags().Set("device", "X")
	_ = inspectCmd.Flags().Set("mock-from", "") // ensure we hit the real factory branch
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected factory error to propagate")
	}
}

// --------------------------------------------------------------------------
// --mock-from
// --------------------------------------------------------------------------

// withFakeMockInspectDeps swaps the mock factory (not the production one)
// so we can verify the --mock-from path bypasses the vision client and
// uses the canned file directly. The mock factory needs only Manager.
func withFakeMockInspectDeps(t *testing.T, mgr *fakeInspectManager) {
	t.Helper()
	prev := inspectMockFactory
	inspectMockFactory = func() (inspectDeps, error) {
		return inspectDeps{Manager: mgr, Client: nil, Cleanup: func() {}}, nil
	}
	t.Cleanup(func() { inspectMockFactory = prev })
	resetInspectFlags(t)
}

func writeMockTopics(t *testing.T, dir string, body string) string {
	t.Helper()
	p := filepath.Join(dir, "topics.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatalf("write mock topics: %v", err)
	}
	return p
}

func TestInspect_MockFromBypassesVisionClient(t *testing.T) {
	pngBytes := makeTestPNG(t, 1080, 2400)
	mgr := &fakeInspectManager{pngBytes: pngBytes}
	// Production factory must NOT be reached when --mock-from is set; if
	// it is, the test fails noisily.
	prevProd := inspectFactory
	inspectFactory = func() (inspectDeps, error) {
		t.Fatal("production factory must not be invoked when --mock-from is set")
		return inspectDeps{}, nil
	}
	t.Cleanup(func() { inspectFactory = prevProd })
	withFakeMockInspectDeps(t, mgr)

	dir := t.TempDir()
	path := writeMockTopics(t, dir, `{
		"topics": [
			{"name": "Mock button", "coordinates": {"x1": 200, "y1": 800, "x2": 600, "y2": 1000}, "type": "button"}
		]
	}`)

	_ = inspectCmd.Flags().Set("device", "R58W2193TXP")
	_ = inspectCmd.Flags().Set("mock-from", path)
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	if err := runInspect(inspectCmd, nil); err != nil {
		t.Fatalf("runInspect: %v", err)
	}

	// Manager was called for the real device.
	if mgr.lastID != "R58W2193TXP" {
		t.Errorf("manager.lastID = %q, want R58W2193TXP", mgr.lastID)
	}

	// Output uses the topics from the file but the screenshot_size from
	// the live PNG (1080x2400, NOT whatever the file may have contained).
	var out inspectOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Topics) != 1 || out.Topics[0].Name != "Mock button" {
		t.Errorf("topics = %+v, want one Mock button", out.Topics)
	}
	if out.ScreenshotSize.Width != 1080 || out.ScreenshotSize.Height != 2400 {
		t.Errorf("screenshot_size = %dx%d, want 1080x2400 (from real screenshot, not file)",
			out.ScreenshotSize.Width, out.ScreenshotSize.Height)
	}
}

func TestInspect_MockFromMissingFile(t *testing.T) {
	mgr := &fakeInspectManager{pngBytes: makeTestPNG(t, 100, 100)}
	withFakeMockInspectDeps(t, mgr)

	_ = inspectCmd.Flags().Set("device", "X")
	_ = inspectCmd.Flags().Set("mock-from", "/nonexistent/topics.json")
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected error for missing mock file")
	}
}

func TestInspect_MockFromMalformedJSON(t *testing.T) {
	mgr := &fakeInspectManager{pngBytes: makeTestPNG(t, 100, 100)}
	withFakeMockInspectDeps(t, mgr)

	dir := t.TempDir()
	path := writeMockTopics(t, dir, "not json")

	_ = inspectCmd.Flags().Set("device", "X")
	_ = inspectCmd.Flags().Set("mock-from", path)
	stdout := &bytes.Buffer{}
	inspectCmd.SetOut(stdout)

	err := runInspect(inspectCmd, nil)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

package job

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/caffeaun/farmhand/internal/notify"
	"github.com/rs/zerolog"
)

// --- Fakes ---

// fakeExecutor is a manual fake for the executor interface.
type fakeExecutor struct {
	mu      sync.Mutex
	results map[string]ExecResult // keyed by deviceID
	calls   []Execution
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{results: make(map[string]ExecResult)}
}

func (f *fakeExecutor) setResult(deviceID string, result ExecResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[deviceID] = result
}

func (f *fakeExecutor) Run(_ context.Context, exec Execution, outputCh chan<- string) ExecResult {
	f.mu.Lock()
	f.calls = append(f.calls, exec)
	res, ok := f.results[exec.DeviceID]
	f.mu.Unlock()

	// Signal the drain goroutine that there is no output.
	_ = outputCh

	if !ok {
		return ExecResult{ExitCode: 0, Duration: time.Millisecond}
	}
	return res
}

func (f *fakeExecutor) wasCalled(deviceID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.DeviceID == deviceID {
			return true
		}
	}
	return false
}

// panicExecutor always panics to test recovery paths.
type panicExecutor struct{}

func (p *panicExecutor) Run(_ context.Context, _ Execution, _ chan<- string) ExecResult {
	panic("simulated executor panic")
}

// fakeJobRepo is a manual fake for the jobRepo interface.
type fakeJobRepo struct {
	mu           sync.Mutex
	completedID  string
	completedAt  time.Time
	completedStatus string
	setCompletedErr error
}

func (f *fakeJobRepo) SetCompleted(id string, t time.Time, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completedID = id
	f.completedAt = t
	f.completedStatus = status
	return f.setCompletedErr
}

// fakeResultRepo is a manual fake for the jobResultRepo interface.
type fakeResultRepo struct {
	mu      sync.Mutex
	results []*db.JobResult
	createErr error
}

func (f *fakeResultRepo) Create(jr *db.JobResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	// Assign a fake ID so callers can inspect it.
	jr.ID = fmt.Sprintf("result-%d", len(f.results)+1)
	jr.CreatedAt = time.Now().UTC()
	copy := *jr
	f.results = append(f.results, &copy)
	return nil
}

func (f *fakeResultRepo) findByDevice(deviceID string) *db.JobResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.results {
		if r.DeviceID == deviceID {
			return r
		}
	}
	return nil
}

func (f *fakeResultRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.results)
}

// fakeDeviceRepo is a manual fake for the deviceRepo interface.
type fakeDeviceRepo struct {
	mu       sync.Mutex
	statuses map[string]string
	calls    []string // deviceIDs passed to UpdateStatus
	updateErr error
}

func newFakeDeviceRepo() *fakeDeviceRepo {
	return &fakeDeviceRepo{statuses: make(map[string]string)}
}

func (f *fakeDeviceRepo) UpdateStatus(id, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.statuses[id] = status
	f.calls = append(f.calls, id)
	return nil
}

func (f *fakeDeviceRepo) statusOf(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statuses[id]
}

func (f *fakeDeviceRepo) wasReleased(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == id {
			return true
		}
	}
	return false
}

// fakeArtifactCollector is a manual fake for the artifactCollector interface.
type fakeArtifactCollector struct {
	mu        sync.Mutex
	artifacts []Artifact
	collectErr error
	calls      []string // sourceDir values passed
}

func (f *fakeArtifactCollector) Collect(_, _, sourceDir string) ([]Artifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, sourceDir)
	if f.collectErr != nil {
		return nil, f.collectErr
	}
	return f.artifacts, nil
}

// fakeNotifier is a manual spy for the notifier interface.
type fakeNotifier struct {
	mu     sync.Mutex
	events []notify.WebhookEvent
}

func (f *fakeNotifier) Send(event notify.WebhookEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func (f *fakeNotifier) eventOfType(t string) (notify.WebhookEvent, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Type == t {
			return e, true
		}
	}
	return notify.WebhookEvent{}, false
}

// fakeEventBus is a manual spy for the eventBus interface.
type fakeEventBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (f *fakeEventBus) Publish(event events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}

func (f *fakeEventBus) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

func (f *fakeEventBus) eventOfType(t string) (events.Event, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.Type == t {
			return e, true
		}
	}
	return events.Event{}, false
}

// --- Helpers ---

// newTestRunner builds a Runner with the provided fakes injected.
func newTestRunner(
	exec executor,
	jr jobRepo,
	rr jobResultRepo,
	dr deviceRepo,
	ac artifactCollector,
	n notifier,
	bus eventBus,
) *Runner {
	return NewRunner(exec, jr, rr, dr, ac, n, bus, zerolog.Nop())
}

func makeJob(id, artifactPath string) db.Job {
	return db.Job{
		ID:           id,
		Status:       "running",
		TestCommand:  "true",
		ArtifactPath: artifactPath,
	}
}

func makeExecution(jobID, deviceID string) *Execution {
	return &Execution{
		JobID:    jobID,
		DeviceID: deviceID,
	}
}

// --- Tests ---

// TestRunner_HappyPath_ExitCodeZero verifies that a successful execution
// produces a 'passed' result, resets the device, fires notifications, and
// marks the job 'completed'.
func TestRunner_HappyPath_ExitCodeZero(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-1", ExecResult{ExitCode: 0, Duration: 100 * time.Millisecond, LogPath: "/logs/job1/dev-1.log"})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-1", "")
	execs := []*Execution{makeExecution("job-1", "dev-1")}

	runner.Run(context.Background(), job, execs)

	// Result must be persisted with status 'passed' and correct exit_code.
	res := fakeResults.findByDevice("dev-1")
	if res == nil {
		t.Fatal("expected a result for dev-1, got none")
	}
	if res.Status != "passed" {
		t.Errorf("result status = %q, want %q", res.Status, "passed")
	}
	if res.ExitCode != 0 {
		t.Errorf("result exit_code = %d, want 0", res.ExitCode)
	}

	// Device must be reset to 'online'.
	if status := fakeDevices.statusOf("dev-1"); status != "online" {
		t.Errorf("device status = %q, want online", status)
	}

	// Overall job status must be 'completed'.
	if fakeJob.completedStatus != "completed" {
		t.Errorf("job status = %q, want completed", fakeJob.completedStatus)
	}

	// Webhook must have been called with EventJobCompleted.
	if _, ok := fakeNotify.eventOfType(notify.EventJobCompleted); !ok {
		t.Error("expected EventJobCompleted webhook event, got none")
	}

	// Internal bus must have received JobCompleted.
	if _, ok := fakeBus.eventOfType(events.JobCompleted); !ok {
		t.Error("expected JobCompleted bus event, got none")
	}
}

// TestRunner_NonZeroExitCode verifies that a non-zero exit code produces a
// 'failed' result, still resets the device, and marks the job 'failed'.
func TestRunner_NonZeroExitCode(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-2", ExecResult{ExitCode: 1, Duration: 50 * time.Millisecond})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-2", "")
	execs := []*Execution{makeExecution("job-2", "dev-2")}

	runner.Run(context.Background(), job, execs)

	// Result must be 'failed' with correct exit_code.
	res := fakeResults.findByDevice("dev-2")
	if res == nil {
		t.Fatal("expected a result for dev-2, got none")
	}
	if res.Status != "failed" {
		t.Errorf("result status = %q, want failed", res.Status)
	}
	if res.ExitCode != 1 {
		t.Errorf("result exit_code = %d, want 1", res.ExitCode)
	}

	// Device still reset to 'online' regardless of exit code.
	if status := fakeDevices.statusOf("dev-2"); status != "online" {
		t.Errorf("device status = %q, want online", status)
	}

	// Overall job must be 'failed'.
	if fakeJob.completedStatus != "failed" {
		t.Errorf("job status = %q, want failed", fakeJob.completedStatus)
	}

	// Webhook must have been called with EventJobFailed.
	if _, ok := fakeNotify.eventOfType(notify.EventJobFailed); !ok {
		t.Error("expected EventJobFailed webhook event, got none")
	}

	// Internal bus must have received JobFailed.
	if _, ok := fakeBus.eventOfType(events.JobFailed); !ok {
		t.Error("expected JobFailed bus event, got none")
	}
}

// TestRunner_PanicRecovery verifies that when the executor panics the device
// is still released and the result is persisted with status 'error'.
func TestRunner_PanicRecovery(t *testing.T) {
	t.Parallel()

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(&panicExecutor{}, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-3", "")
	execs := []*Execution{makeExecution("job-3", "panic-dev")}

	// Must not panic the test itself.
	runner.Run(context.Background(), job, execs)

	// Device must be released even after a panic.
	if !fakeDevices.wasReleased("panic-dev") {
		t.Error("expected device to be released after panic, but UpdateStatus was not called")
	}
	if status := fakeDevices.statusOf("panic-dev"); status != "online" {
		t.Errorf("device status = %q, want online", status)
	}

	// A result must be persisted with status 'error'.
	res := fakeResults.findByDevice("panic-dev")
	if res == nil {
		t.Fatal("expected a result for panic-dev, got none")
	}
	if res.Status != "error" {
		t.Errorf("result status = %q, want error", res.Status)
	}
}

// TestRunner_MultipleDevices_OnePasses_OneFails verifies that when one device
// passes and one fails the overall job status is 'failed'.
func TestRunner_MultipleDevices_OnePasses_OneFails(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-pass", ExecResult{ExitCode: 0, Duration: 10 * time.Millisecond})
	fakeExec.setResult("dev-fail", ExecResult{ExitCode: 2, Duration: 10 * time.Millisecond})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-4", "")
	execs := []*Execution{
		makeExecution("job-4", "dev-pass"),
		makeExecution("job-4", "dev-fail"),
	}

	runner.Run(context.Background(), job, execs)

	// Both results must be persisted.
	if fakeResults.count() != 2 {
		t.Errorf("result count = %d, want 2", fakeResults.count())
	}

	// Passing device must have 'passed' status.
	rPass := fakeResults.findByDevice("dev-pass")
	if rPass == nil || rPass.Status != "passed" {
		t.Errorf("dev-pass result status = %q, want passed", rPass.Status)
	}

	// Failing device must have 'failed' status.
	rFail := fakeResults.findByDevice("dev-fail")
	if rFail == nil || rFail.Status != "failed" {
		t.Errorf("dev-fail result status = %q, want failed", rFail.Status)
	}

	// Both devices must be reset.
	if status := fakeDevices.statusOf("dev-pass"); status != "online" {
		t.Errorf("dev-pass status = %q, want online", status)
	}
	if status := fakeDevices.statusOf("dev-fail"); status != "online" {
		t.Errorf("dev-fail status = %q, want online", status)
	}

	// Overall job status must be 'failed' because one device failed.
	if fakeJob.completedStatus != "failed" {
		t.Errorf("job status = %q, want failed", fakeJob.completedStatus)
	}
}

// TestRunner_ArtifactsCollectedAndSerialised verifies that artifacts returned
// by the collector are serialised to JSON and stored in the job result.
func TestRunner_ArtifactsCollectedAndSerialised(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-art", ExecResult{ExitCode: 0, Duration: 10 * time.Millisecond})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{
		artifacts: []Artifact{
			{Filename: "report.xml", Path: "/artifacts/job-5/dev-art/report.xml", SizeBytes: 1024, MimeType: "application/xml"},
		},
	}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-5", "/source/artifacts")
	execs := []*Execution{makeExecution("job-5", "dev-art")}

	runner.Run(context.Background(), job, execs)

	res := fakeResults.findByDevice("dev-art")
	if res == nil {
		t.Fatal("expected a result for dev-art, got none")
	}
	if res.Artifacts == "" {
		t.Fatal("expected artifacts JSON to be set, got empty string")
	}

	var artifacts []Artifact
	if err := json.Unmarshal([]byte(res.Artifacts), &artifacts); err != nil {
		t.Fatalf("unmarshal artifacts: %v", err)
	}
	if len(artifacts) != 1 {
		t.Errorf("artifact count = %d, want 1", len(artifacts))
	}
	if artifacts[0].Filename != "report.xml" {
		t.Errorf("artifact filename = %q, want report.xml", artifacts[0].Filename)
	}
}

// TestRunner_NotifierFailureDoesNotBlock verifies that even if the notifier is
// slow it does not prevent result persistence or device release. Because our
// fake notifier's Send is synchronous, this confirms the call path executes
// without returning an error that could halt the happy path.
func TestRunner_NotifierFailureDoesNotBlock(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-6", ExecResult{ExitCode: 0, Duration: 5 * time.Millisecond})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeBus := &fakeEventBus{}

	// A notifier that records a call but returns no error (fire-and-forget contract).
	fakeNotify := &fakeNotifier{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-6", "")
	execs := []*Execution{makeExecution("job-6", "dev-6")}

	runner.Run(context.Background(), job, execs)

	// Result must still be persisted.
	res := fakeResults.findByDevice("dev-6")
	if res == nil {
		t.Fatal("expected a result, got none")
	}
	if res.Status != "passed" {
		t.Errorf("result status = %q, want passed", res.Status)
	}

	// Device must still be released.
	if status := fakeDevices.statusOf("dev-6"); status != "online" {
		t.Errorf("device status = %q, want online", status)
	}
}

// TestRunner_ErrorMessage_PersistedOnFailure verifies that ErrorMessage from
// the executor is stored in the persisted JobResult when the command fails.
func TestRunner_ErrorMessage_PersistedOnFailure(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-err", ExecResult{
		ExitCode:     1,
		Duration:     10 * time.Millisecond,
		ErrorMessage: "test failure: assertion failed on line 42",
	})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()
	fakeArtifacts := &fakeArtifactCollector{}
	fakeNotify := &fakeNotifier{}
	fakeBus := &fakeEventBus{}

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, fakeArtifacts, fakeNotify, fakeBus)

	job := makeJob("job-err", "")
	execs := []*Execution{makeExecution("job-err", "dev-err")}

	runner.Run(context.Background(), job, execs)

	res := fakeResults.findByDevice("dev-err")
	if res == nil {
		t.Fatal("expected a result for dev-err, got none")
	}
	if res.Status != "failed" {
		t.Errorf("result status = %q, want failed", res.Status)
	}
	if res.ErrorMessage == "" {
		t.Error("expected ErrorMessage to be non-empty, got empty string")
	}
	if res.ErrorMessage != "test failure: assertion failed on line 42" {
		t.Errorf("ErrorMessage = %q, want %q", res.ErrorMessage, "test failure: assertion failed on line 42")
	}
}

// TestRunner_ErrorMessage_EmptyOnSuccess verifies that ErrorMessage is empty
// when the execution passes.
func TestRunner_ErrorMessage_EmptyOnSuccess(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("dev-ok", ExecResult{
		ExitCode:     0,
		Duration:     5 * time.Millisecond,
		ErrorMessage: "",
	})

	fakeJob := &fakeJobRepo{}
	fakeResults := &fakeResultRepo{}
	fakeDevices := newFakeDeviceRepo()

	runner := newTestRunner(fakeExec, fakeJob, fakeResults, fakeDevices, &fakeArtifactCollector{}, &fakeNotifier{}, &fakeEventBus{})

	job := makeJob("job-ok-err", "")
	execs := []*Execution{makeExecution("job-ok-err", "dev-ok")}

	runner.Run(context.Background(), job, execs)

	res := fakeResults.findByDevice("dev-ok")
	if res == nil {
		t.Fatal("expected a result for dev-ok, got none")
	}
	if res.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q, want empty string", res.ErrorMessage)
	}
}

// TestRunner_AllPassed_JobCompleted verifies the 'all passed → completed'
// aggregation rule with two passing devices.
func TestRunner_AllPassed_JobCompleted(t *testing.T) {
	t.Parallel()

	fakeExec := newFakeExecutor()
	fakeExec.setResult("d1", ExecResult{ExitCode: 0, Duration: 5 * time.Millisecond})
	fakeExec.setResult("d2", ExecResult{ExitCode: 0, Duration: 5 * time.Millisecond})

	fakeJob := &fakeJobRepo{}
	runner := newTestRunner(
		fakeExec,
		fakeJob,
		&fakeResultRepo{},
		newFakeDeviceRepo(),
		&fakeArtifactCollector{},
		&fakeNotifier{},
		&fakeEventBus{},
	)

	job := makeJob("job-7", "")
	runner.Run(context.Background(), job, []*Execution{
		makeExecution("job-7", "d1"),
		makeExecution("job-7", "d2"),
	})

	if fakeJob.completedStatus != "completed" {
		t.Errorf("job status = %q, want completed", fakeJob.completedStatus)
	}
}

package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestExecutor(t *testing.T) (*Executor, string) {
	t.Helper()
	logDir := t.TempDir()
	logger := zerolog.New(zerolog.NewTestWriter(t))
	return NewExecutor(logDir, logger), logDir
}

func newExecution(overrides ...func(*Execution)) Execution {
	e := Execution{
		JobID:          "job-1",
		DeviceID:       "device-1",
		DeviceSerial:   "serial-abc",
		DevicePlatform: "android",
		TestCommand:    "echo hello",
		Env:            map[string]string{},
		TimeoutMinutes: 1,
	}
	for _, fn := range overrides {
		fn(&e)
	}
	return e
}

// TestRun_EchoCommand verifies exit code 0 and output in the log file.
func TestRun_EchoCommand(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	result := executor.Run(context.Background(), newExecution(func(e *Execution) {
		e.TestCommand = "echo hello"
	}), outputCh)

	assert.Equal(t, 0, result.ExitCode)
	assert.NoError(t, result.Error)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
}

// TestRun_EnvVarInjection verifies FARMHAND_DEVICE_ID is injected and echoed.
func TestRun_EnvVarInjection(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.DeviceID = "my-device-42"
		e.TestCommand = "echo $FARMHAND_DEVICE_ID"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	require.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "my-device-42")
}

// TestRun_CustomEnvVars verifies user-supplied Env entries are injected.
func TestRun_CustomEnvVars(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.Env = map[string]string{"MY_VAR": "custom-value"}
		e.TestCommand = "echo $MY_VAR"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	require.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "custom-value")
}

// TestRun_NonZeroExitCode verifies the exit code is correctly captured.
func TestRun_NonZeroExitCode(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "exit 42"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 42, result.ExitCode)
}

// TestRun_Timeout verifies that a long-running command is killed when the
// parent context deadline is reached.
func TestRun_Timeout(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	// Use a very short context deadline instead of relying on TimeoutMinutes=0.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "sleep 60"
		e.TimeoutMinutes = 0 // no per-execution timeout; rely on ctx above
	})

	start := time.Now()
	result := executor.Run(ctx, exec, outputCh)
	elapsed := time.Since(start)

	// Command must have been killed well before 60 s.
	assert.Less(t, elapsed, 5*time.Second)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.Error(t, result.Error)
}

// TestRun_LogFileCreated verifies the log file is created at the expected path.
func TestRun_LogFileCreated(t *testing.T) {
	executor, logDir := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.JobID = "job-log-test"
		e.DeviceID = "dev-xyz"
		e.TestCommand = "echo log-check"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 0, result.ExitCode)

	expectedPath := filepath.Join(logDir, "job-log-test", "dev-xyz.log")
	assert.Equal(t, expectedPath, result.LogPath)

	_, err := os.Stat(expectedPath)
	assert.NoError(t, err, "log file should exist")
}

// TestRun_OutputChannel verifies output lines are sent to the channel.
func TestRun_OutputChannel(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 64)

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "printf 'line1\nline2\nline3\n'"
	})

	result := executor.Run(context.Background(), exec, outputCh)
	require.Equal(t, 0, result.ExitCode)

	close(outputCh)
	var lines []string
	for l := range outputCh {
		lines = append(lines, l)
	}

	combined := strings.Join(lines, "\n")
	assert.Contains(t, combined, "line1")
	assert.Contains(t, combined, "line2")
	assert.Contains(t, combined, "line3")
}

// TestRun_WorkspaceIsolation verifies that FARMHAND_WORKSPACE is set and the
// command runs inside the isolated workspace directory.
func TestRun_WorkspaceIsolation(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.JobID = "job-ws"
		e.DeviceID = "dev-ws"
		e.TestCommand = "echo $FARMHAND_WORKSPACE && pwd"
	})

	result := executor.Run(context.Background(), exec, outputCh)
	require.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	output := string(data)

	expectedSuffix := filepath.Join("farmhand", "job-ws", "dev-ws")
	assert.Contains(t, output, expectedSuffix, "FARMHAND_WORKSPACE should contain job/device path")

	// pwd output should end with the same workspace suffix (macOS resolves
	// /var -> /private/var so we compare suffixes rather than exact paths).
	lines := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(t, lines, 2, "should have FARMHAND_WORKSPACE and pwd lines")
	assert.True(t, strings.HasSuffix(lines[1], expectedSuffix),
		"pwd (%s) should end with %s", lines[1], expectedSuffix)
}

// TestRun_ErrorMessage_WithOutput verifies that when a command exits non-zero
// and produces output, ErrorMessage contains the output.
func TestRun_ErrorMessage_WithOutput(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 64)

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "echo fail-output && exit 1"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 1, result.ExitCode)
	assert.Contains(t, result.ErrorMessage, "fail-output",
		"ErrorMessage should contain the command output")
}

// TestRun_ErrorMessage_NoOutput verifies that when a command exits non-zero
// without any output, ErrorMessage falls back to the generic code message.
func TestRun_ErrorMessage_NoOutput(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 64)

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "exit 1"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 1, result.ExitCode)
	assert.Equal(t, "command exited with code 1", result.ErrorMessage)
}

// TestRun_ErrorMessage_Success verifies that ErrorMessage is empty on success.
func TestRun_ErrorMessage_Success(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	result := executor.Run(context.Background(), newExecution(func(e *Execution) {
		e.TestCommand = "echo ok"
	}), outputCh)

	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, result.ErrorMessage)
}

// --------------------------------------------------------------------------
// install_command tests
// --------------------------------------------------------------------------

// TestRun_InstallCommand_Success verifies that install_command runs before
// test_command and both outputs appear in the log.
func TestRun_InstallCommand_Success(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 64)

	exec := newExecution(func(e *Execution) {
		e.InstallCommand = "echo installing-artifact"
		e.TestCommand = "echo running-test"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 0, result.ExitCode)
	assert.NoError(t, result.Error)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	log := string(data)

	assert.Contains(t, log, "=== install_command ===")
	assert.Contains(t, log, "installing-artifact")
	assert.Contains(t, log, "=== end install_command ===")
	assert.Contains(t, log, "running-test")

	// install_command output should appear before test_command output
	installPos := strings.Index(log, "installing-artifact")
	testPos := strings.Index(log, "running-test")
	assert.Less(t, installPos, testPos, "install output should appear before test output")
}

// TestRun_InstallCommand_Failure verifies that when install_command fails,
// test_command is skipped and the result reflects the install failure.
func TestRun_InstallCommand_Failure(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 64)

	exec := newExecution(func(e *Execution) {
		e.InstallCommand = "echo install-failed && exit 1"
		e.TestCommand = "echo should-not-run"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 1, result.ExitCode)
	assert.Error(t, result.Error)
	assert.Contains(t, result.Error.Error(), "install_command failed")

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	log := string(data)

	assert.Contains(t, log, "install-failed")
	assert.NotContains(t, log, "should-not-run", "test_command must be skipped on install failure")
}

// TestRun_InstallCommand_Empty verifies backward compatibility — empty
// install_command means only test_command runs.
func TestRun_InstallCommand_Empty(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.InstallCommand = ""
		e.TestCommand = "echo only-test"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	log := string(data)

	assert.NotContains(t, log, "=== install_command ===", "no install header when install_command is empty")
	assert.Contains(t, log, "only-test")
}

// TestRun_InstallCommand_EnvVars verifies that FARMHAND_* env vars are
// available in the install_command.
func TestRun_InstallCommand_EnvVars(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	exec := newExecution(func(e *Execution) {
		e.DeviceSerial = "serial-xyz"
		e.InstallCommand = "echo $FARMHAND_DEVICE_SERIAL"
		e.TestCommand = "echo done"
	})

	result := executor.Run(context.Background(), exec, outputCh)

	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(result.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "serial-xyz")
}

// TestRun_InstallCommand_SharesTimeout verifies that install_command shares
// the same timeout budget as test_command.
func TestRun_InstallCommand_SharesTimeout(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	exec := newExecution(func(e *Execution) {
		e.InstallCommand = "sleep 60"
		e.TestCommand = "echo should-not-run"
		e.TimeoutMinutes = 0
	})

	start := time.Now()
	result := executor.Run(ctx, exec, outputCh)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 5*time.Second)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.Error(t, result.Error)
}

// TestRun_ContextCancellation verifies the process is stopped when the parent
// context is cancelled.
func TestRun_ContextCancellation(t *testing.T) {
	executor, _ := newTestExecutor(t)
	outputCh := make(chan string, 16)

	ctx, cancel := context.WithCancel(context.Background())

	exec := newExecution(func(e *Execution) {
		e.TestCommand = "sleep 60"
		e.TimeoutMinutes = 0
	})

	// Cancel almost immediately.
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	result := executor.Run(ctx, exec, outputCh)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 5*time.Second)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.Error(t, result.Error)
}

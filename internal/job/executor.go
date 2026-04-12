package job

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

// Executor runs test commands on devices.
type Executor struct {
	logDir string
	logger zerolog.Logger
}

// NewExecutor creates an Executor with the given log directory.
func NewExecutor(logDir string, logger zerolog.Logger) *Executor {
	return &Executor{logDir: logDir, logger: logger}
}

// Run executes a test command for a single device.
//
// The command is run via /bin/sh -c to support shell quoting and pipes.
// FARMHAND_DEVICE_ID, FARMHAND_DEVICE_SERIAL, FARMHAND_DEVICE_PLATFORM,
// FARMHAND_JOB_ID, FARMHAND_WORKSPACE, and all Execution.Env entries are
// injected into the command environment.
//
// Each execution runs in an isolated workspace directory at
// $TMPDIR/farmhand/<jobID>/<deviceID>/ so that fan-out jobs on the same
// host do not collide (e.g. concurrent git operations on the same repo).
//
// stdout and stderr are captured to <logDir>/<jobID>/<deviceID>.log and
// streamed line-by-line to outputCh using a non-blocking send so the
// executor never blocks when no consumer is reading.
//
// A timeout is applied via context.WithTimeout using execution.TimeoutMinutes.
// If TimeoutMinutes is 0 only the parent ctx deadline/cancellation applies.
//
// Returns an ExecResult with exit code, duration, log path, and any error.
func (e *Executor) Run(ctx context.Context, execution Execution, outputCh chan<- string) ExecResult {
	start := time.Now()

	// Create per-job log directory.
	jobLogDir := filepath.Join(e.logDir, execution.JobID)
	if err := os.MkdirAll(jobLogDir, 0o755); err != nil {
		return ExecResult{
			ExitCode: -1,
			Duration: time.Since(start),
			Error:    fmt.Errorf("create log dir: %w", err),
		}
	}
	logPath := filepath.Join(jobLogDir, execution.DeviceID+".log")

	// Apply per-execution timeout when TimeoutMinutes is set.
	runCtx := ctx
	if execution.TimeoutMinutes > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(execution.TimeoutMinutes)*time.Minute)
		defer cancel()
	}

	// Run install_command first, if specified.
	if execution.InstallCommand != "" {
		installResult := e.runInstall(runCtx, execution, logPath, start)
		if installResult != nil {
			return *installResult
		}
	}

	// Build command: /bin/sh -c "<test_command>"
	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", execution.TestCommand)

	// Run the command in its own process group so that context cancellation
	// kills the entire tree (shell + children), not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Create per-device workspace directory so fan-out jobs don't collide.
	workspaceDir := filepath.Join(os.TempDir(), "farmhand", execution.JobID, execution.DeviceID)
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return ExecResult{
			ExitCode: -1,
			Duration: time.Since(start),
			Error:    fmt.Errorf("create workspace dir: %w", err),
		}
	}
	cmd.Dir = workspaceDir

	// Inherit current process environment then inject FARMHAND_* vars.
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"FARMHAND_DEVICE_ID="+execution.DeviceID,
		"FARMHAND_DEVICE_SERIAL="+execution.DeviceSerial,
		"FARMHAND_DEVICE_PLATFORM="+execution.DevicePlatform,
		"FARMHAND_JOB_ID="+execution.JobID,
		"FARMHAND_WORKSPACE="+workspaceDir,
	)
	for k, v := range execution.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Open combined output pipe (stdout + stderr together).
	pr, pw := io.Pipe()

	cmd.Stdout = pw
	cmd.Stderr = pw

	// Open log file. Use O_APPEND so install_command output is preserved.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ExecResult{
			ExitCode: -1,
			Duration: time.Since(start),
			LogPath:  logPath,
			Error:    fmt.Errorf("create log file: %w", err),
		}
	}
	defer logFile.Close()

	// Start command.
	if err = cmd.Start(); err != nil {
		return ExecResult{
			ExitCode: -1,
			Duration: time.Since(start),
			LogPath:  logPath,
			Error:    fmt.Errorf("start command: %w", err),
		}
	}

	// Drain the pipe in a goroutine: write each line to the log file and
	// send it to outputCh without blocking.
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintln(logFile, line)
			// Non-blocking send so we don't stall if no consumer is reading.
			select {
			case outputCh <- line:
			default:
			}
		}
	}()

	// Wait for command to finish, then close the write-end of the pipe so
	// the scanner goroutine drains any remaining data and exits.
	waitErr := cmd.Wait()
	pw.Close()
	<-scanDone

	// Determine exit code and surface context errors clearly.
	exitCode := 0
	var runErr error
	var errorMessage string

	switch {
	case waitErr == nil:
		// success — exitCode stays 0
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		exitCode = -1
		runErr = fmt.Errorf("execution timed out after %d minute(s)", execution.TimeoutMinutes)
		errorMessage = runErr.Error()
	case errors.Is(runCtx.Err(), context.Canceled):
		exitCode = -1
		runErr = fmt.Errorf("execution cancelled: %w", runCtx.Err())
		errorMessage = runErr.Error()
	default:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			runErr = waitErr
		}
	}

	// For non-zero exit codes that are not timeout/cancel, read the last 20
	// lines of the log file as the error message. The read is safe because
	// <-scanDone guarantees all writes are complete.
	if exitCode != 0 && errorMessage == "" {
		errorMessage = tailLogFile(logPath, 20, exitCode)
	}

	e.logger.Info().
		Str("job_id", execution.JobID).
		Str("device_id", execution.DeviceID).
		Int("exit_code", exitCode).
		Dur("duration", time.Since(start)).
		Msg("execution finished")

	return ExecResult{
		ExitCode:     exitCode,
		Duration:     time.Since(start),
		LogPath:      logPath,
		Error:        runErr,
		ErrorMessage: errorMessage,
	}
}

// runInstall runs the install_command before the test_command. Output is
// written to the same per-device log file. Returns a non-nil ExecResult if
// the install failed (caller should return it and skip test_command).
// Returns nil on success.
func (e *Executor) runInstall(ctx context.Context, execution Execution, logPath string, start time.Time) *ExecResult {
	e.logger.Info().
		Str("job_id", execution.JobID).
		Str("device_id", execution.DeviceID).
		Msg("running install_command")

	// Open the log file in append mode (it was already created by the caller).
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &ExecResult{
			ExitCode:     -1,
			Duration:     time.Since(start),
			LogPath:      logPath,
			Error:        fmt.Errorf("open log for install: %w", err),
			ErrorMessage: fmt.Sprintf("open log for install: %v", err),
		}
	}

	_, _ = fmt.Fprintln(logFile, "=== install_command ===")

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", execution.InstallCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Use the same workspace as the test_command.
	workspaceDir := filepath.Join(os.TempDir(), "farmhand", execution.JobID, execution.DeviceID)
	cmd.Dir = workspaceDir

	// Same environment as test_command.
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"FARMHAND_DEVICE_ID="+execution.DeviceID,
		"FARMHAND_DEVICE_SERIAL="+execution.DeviceSerial,
		"FARMHAND_DEVICE_PLATFORM="+execution.DevicePlatform,
		"FARMHAND_JOB_ID="+execution.JobID,
		"FARMHAND_WORKSPACE="+workspaceDir,
	)
	for k, v := range execution.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Capture output to log file.
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			_, _ = fmt.Fprintln(logFile, scanner.Text())
		}
	}()

	if err := cmd.Start(); err != nil {
		pw.Close()
		<-scanDone
		logFile.Close()
		return &ExecResult{
			ExitCode:     -1,
			Duration:     time.Since(start),
			LogPath:      logPath,
			Error:        fmt.Errorf("start install_command: %w", err),
			ErrorMessage: fmt.Sprintf("install_command failed to start: %v", err),
		}
	}

	waitErr := cmd.Wait()
	pw.Close()
	<-scanDone
	_, _ = fmt.Fprintln(logFile, "=== end install_command ===")
	logFile.Close()

	if waitErr != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		errorMessage := tailLogFile(logPath, 20, exitCode)

		e.logger.Warn().
			Str("job_id", execution.JobID).
			Str("device_id", execution.DeviceID).
			Int("exit_code", exitCode).
			Msg("install_command failed")

		return &ExecResult{
			ExitCode:     exitCode,
			Duration:     time.Since(start),
			LogPath:      logPath,
			Error:        fmt.Errorf("install_command failed: %w", waitErr),
			ErrorMessage: errorMessage,
		}
	}

	e.logger.Info().
		Str("job_id", execution.JobID).
		Str("device_id", execution.DeviceID).
		Msg("install_command succeeded")

	return nil
}

// tailLogFile opens the log file at path, reads all lines, and returns the
// last n lines joined by newline. If the file is empty or cannot be read,
// returns a fallback message including the exit code.
func tailLogFile(path string, n, exitCode int) string {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf("command exited with code %d", exitCode)
	}
	defer f.Close() //nolint:errcheck

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) == 0 {
		return fmt.Sprintf("command exited with code %d", exitCode)
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

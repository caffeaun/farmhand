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
// FARMHAND_JOB_ID, and all Execution.Env entries are injected into the
// command environment.
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

	// Build command: /bin/sh -c "<test_command>"
	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", execution.TestCommand)

	// Run the command in its own process group so that context cancellation
	// kills the entire tree (shell + children), not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Inherit current process environment then inject FARMHAND_* vars.
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"FARMHAND_DEVICE_ID="+execution.DeviceID,
		"FARMHAND_DEVICE_SERIAL="+execution.DeviceSerial,
		"FARMHAND_DEVICE_PLATFORM="+execution.DevicePlatform,
		"FARMHAND_JOB_ID="+execution.JobID,
	)
	for k, v := range execution.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Open combined output pipe (stdout + stderr together).
	pr, pw := io.Pipe()

	cmd.Stdout = pw
	cmd.Stderr = pw

	// Open log file.
	logFile, err := os.Create(logPath)
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

	switch {
	case waitErr == nil:
		// success — exitCode stays 0
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		exitCode = -1
		runErr = fmt.Errorf("execution timed out after %d minute(s)", execution.TimeoutMinutes)
	case errors.Is(runCtx.Err(), context.Canceled):
		exitCode = -1
		runErr = fmt.Errorf("execution cancelled: %w", runCtx.Err())
	default:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			runErr = waitErr
		}
	}

	e.logger.Info().
		Str("job_id", execution.JobID).
		Str("device_id", execution.DeviceID).
		Int("exit_code", exitCode).
		Dur("duration", time.Since(start)).
		Msg("execution finished")

	return ExecResult{
		ExitCode: exitCode,
		Duration: time.Since(start),
		LogPath:  logPath,
		Error:    runErr,
	}
}

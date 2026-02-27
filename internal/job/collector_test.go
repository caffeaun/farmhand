package job

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestCollector(t *testing.T) *LogCollector {
	t.Helper()
	return NewLogCollector(t.TempDir())
}

// TestWrite_CreatesFileAndDirs verifies that Write creates the job directory
// and the log file when they do not already exist.
func TestWrite_CreatesFileAndDirs(t *testing.T) {
	c := newTestCollector(t)

	if err := c.Write("job1", "dev1", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := c.logPath("job1", "dev1")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("log dir not created: %v", err)
	}
}

// TestWrite_AppendsNewline verifies that a line without a trailing newline
// gets one appended.
func TestWrite_AppendsNewline(t *testing.T) {
	c := newTestCollector(t)

	if err := c.Write("job1", "dev1", []byte("no newline")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(c.logPath("job1", "dev1"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Errorf("expected trailing newline, got %q", data)
	}
}

// TestWrite_PreservesExistingNewline verifies that a line already ending with
// \n does not get a second newline appended.
func TestWrite_PreservesExistingNewline(t *testing.T) {
	c := newTestCollector(t)

	if err := c.Write("job1", "dev1", []byte("has newline\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(c.logPath("job1", "dev1"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Should be exactly "has newline\n", not "has newline\n\n".
	if string(data) != "has newline\n" {
		t.Errorf("unexpected content %q", data)
	}
}

// TestRead_ReturnsContent writes lines and reads them back via Read.
func TestRead_ReturnsContent(t *testing.T) {
	c := newTestCollector(t)

	lines := []string{"line one", "line two", "line three"}
	for _, l := range lines {
		if err := c.Write("job2", "dev2", []byte(l)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	rc, err := c.Read("job2", "dev2")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}

	if len(got) != len(lines) {
		t.Fatalf("expected %d lines, got %d: %v", len(lines), len(got), got)
	}
	for i, want := range lines {
		if got[i] != want {
			t.Errorf("line %d: want %q, got %q", i, want, got[i])
		}
	}
}

// TestRead_FileNotFound verifies that Read returns an error when the file
// does not exist.
func TestRead_FileNotFound(t *testing.T) {
	c := newTestCollector(t)

	_, err := c.Read("nojob", "nodev")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "log not found") {
		t.Errorf("expected 'log not found' in error, got %q", err.Error())
	}
}

// TestTail_ExistingLines verifies that Tail sends all pre-existing lines
// before polling for new ones.
func TestTail_ExistingLines(t *testing.T) {
	c := newTestCollector(t)

	// Write lines before starting Tail.
	want := []string{"alpha", "beta", "gamma"}
	for _, l := range want {
		if err := c.Write("job3", "dev3", []byte(l)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.Tail(ctx, "job3", "dev3", ch)
	}()

	got := make([]string, 0, len(want))
	timeout := time.After(1 * time.Second)
collect:
	for len(got) < len(want) {
		select {
		case line := <-ch:
			got = append(got, line)
		case <-timeout:
			break collect
		}
	}

	cancel()
	<-done

	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d: want %q, got %q", i, w, got[i])
		}
	}
}

// TestTail_NewLines starts Tail before any content is written, then writes
// lines and verifies they arrive within 200ms.
func TestTail_NewLines(t *testing.T) {
	c := newTestCollector(t)

	// Create the file first so Tail doesn't have to wait for it.
	if err := c.Write("job4", "dev4", []byte("seed")); err != nil {
		t.Fatalf("Write seed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch := make(chan string, 20)
	done := make(chan error, 1)
	go func() {
		done <- c.Tail(ctx, "job4", "dev4", ch)
	}()

	// Drain the seed line.
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive seed line")
	}

	// Write new lines and expect them to arrive within 200ms each.
	newLines := []string{"new1", "new2", "new3"}
	for _, l := range newLines {
		deadline := time.After(200 * time.Millisecond)
		if err := c.Write("job4", "dev4", []byte(l)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		select {
		case got := <-ch:
			if got != l {
				t.Errorf("want %q, got %q", l, got)
			}
		case <-deadline:
			t.Errorf("line %q not received within 200ms", l)
		}
	}

	cancel()
	<-done
}

// TestTail_ContextCancel verifies that Tail exits cleanly when ctx is cancelled.
func TestTail_ContextCancel(t *testing.T) {
	c := newTestCollector(t)

	// Write a file so Tail opens successfully.
	if err := c.Write("job5", "dev5", []byte("line")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ch := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.Tail(ctx, "job5", "dev5", ch)
	}()

	// Let Tail start.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error from Tail after cancel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Tail did not stop after context cancel")
	}
}

// TestConcurrentWrites launches 10 goroutines each writing 10 lines and
// verifies the file contains exactly 100 lines without corruption.
func TestConcurrentWrites(t *testing.T) {
	c := newTestCollector(t)

	const goroutines = 10
	const linesEach = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(id int) {
			defer wg.Done()
			for j := range linesEach {
				line := []byte(strings.Repeat("x", 64)) // fixed-size content
				_ = line
				// Write a line that includes goroutine and iteration identifiers.
				if err := c.Write("jobC", "devC", []byte(
					strings.Join([]string{"goroutine", string(rune('A'+id)), "line", string(rune('0'+j))}, "-"),
				)); err != nil {
					t.Errorf("Write g%d l%d: %v", id, j, err)
				}
			}
		}(i)
	}
	wg.Wait()

	rc, err := c.Read("jobC", "devC")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	total := goroutines * linesEach
	if count != total {
		t.Errorf("expected %d lines, got %d", total, count)
	}
}

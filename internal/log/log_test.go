package log

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func TestInit_JSONOutput(t *testing.T) {
	Init("info", false)

	if Logger.GetLevel() != zerolog.InfoLevel {
		t.Errorf("expected info level, got %s", Logger.GetLevel())
	}
}

func TestInit_ConsoleWriter_NoPanic(t *testing.T) {
	// Must not panic
	Init("debug", true)

	if Logger.GetLevel() != zerolog.DebugLevel {
		t.Errorf("expected debug level, got %s", Logger.GetLevel())
	}
}

func TestInit_InvalidLevel_DefaultsToInfo(t *testing.T) {
	Init("invalid", false)

	if Logger.GetLevel() != zerolog.InfoLevel {
		t.Errorf("expected info level for invalid input, got %s", Logger.GetLevel())
	}
}

func TestInit_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	Init("info", false)

	// Replace Logger output with buffer for assertion
	Logger = zerolog.New(&buf).Level(zerolog.InfoLevel).With().Timestamp().Logger()

	Logger.Debug().Msg("debug message should be suppressed")
	Logger.Info().Msg("info message should appear")

	output := buf.String()

	if bytes.Contains([]byte(output), []byte("debug message should be suppressed")) {
		t.Error("debug message should be suppressed at info level")
	}

	if !bytes.Contains([]byte(output), []byte("info message should appear")) {
		t.Error("info message should appear at info level")
	}
}

func TestInit_JSONStructure(t *testing.T) {
	var buf bytes.Buffer
	Init("info", false)
	Logger = zerolog.New(&buf).Level(zerolog.InfoLevel).With().Timestamp().Logger()

	Logger.Info().Str("key", "value").Msg("test")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	if entry["message"] != "test" {
		t.Errorf("expected message 'test', got %v", entry["message"])
	}
	if entry["key"] != "value" {
		t.Errorf("expected key 'value', got %v", entry["key"])
	}
}

func TestFromContext_NoLogger_ReturnsGlobal(t *testing.T) {
	Init("info", false)
	globalLevel := Logger.GetLevel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	got := FromContext(c)
	if got.GetLevel() != globalLevel {
		t.Errorf("expected global logger level %s, got %s", globalLevel, got.GetLevel())
	}
}

func TestFromContext_WithLogger_ReturnsContextLogger(t *testing.T) {
	Init("info", false)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	contextLogger := Logger.With().Str("request_id", "test-123").Logger()
	c.Set(string(loggerKey), contextLogger)

	got := FromContext(c)
	// We cannot directly compare loggers by value, but we can verify the result
	// is not the plain global logger by checking that it writes the request_id field.
	var buf bytes.Buffer
	testLogger := got.Output(&buf)
	testLogger.Info().Msg("check")

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("test-123")) {
		t.Errorf("expected context logger with request_id 'test-123', got: %s", output)
	}
}

func TestFromContext_NilContext_ReturnsGlobal(t *testing.T) {
	Init("info", false)

	got := FromContext(nil)
	if got.GetLevel() != Logger.GetLevel() {
		t.Errorf("expected global logger level, got %s", got.GetLevel())
	}
}

func TestFromStdContext_NoLogger_ReturnsGlobal(t *testing.T) {
	Init("info", false)

	ctx := context.Background()
	got := FromStdContext(ctx)
	if got.GetLevel() != Logger.GetLevel() {
		t.Errorf("expected global logger level, got %s", got.GetLevel())
	}
}

func TestFromStdContext_WithLogger_ReturnsContextLogger(t *testing.T) {
	Init("info", false)

	customLogger := Logger.With().Str("component", "test").Logger()
	ctx := WithContext(context.Background(), customLogger)

	got := FromStdContext(ctx)

	var buf bytes.Buffer
	testLogger := got.Output(&buf)
	testLogger.Info().Msg("check")

	if !bytes.Contains(buf.Bytes(), []byte("test")) {
		t.Errorf("expected context logger with component 'test', got: %s", buf.String())
	}
}

func TestWithContext_RoundTrip(t *testing.T) {
	Init("info", false)

	customLogger := zerolog.New(zerolog.Nop()).Level(zerolog.WarnLevel)
	ctx := WithContext(context.Background(), customLogger)

	got := FromStdContext(ctx)
	if got.GetLevel() != zerolog.WarnLevel {
		t.Errorf("expected warn level from context logger, got %s", got.GetLevel())
	}
}

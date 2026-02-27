// Package log configures and exposes the application-wide structured logger.
package log

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const loggerKey contextKey = "logger"

// Logger is the package-level logger instance.
var Logger zerolog.Logger

// Init initializes the global logger with the given level and format.
// Call this once at startup after config is loaded.
// SECURITY: Never log Config.AuthToken or full config struct.
func Init(level string, devMode bool) {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		lvl = zerolog.InfoLevel
	}

	var output io.Writer = os.Stdout
	if devMode {
		output = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	Logger = zerolog.New(output).
		Level(lvl).
		With().
		Timestamp().
		Logger()
}

// FromContext retrieves the logger from a gin.Context or returns the global logger.
func FromContext(c *gin.Context) zerolog.Logger {
	if c != nil {
		if l, exists := c.Get(string(loggerKey)); exists {
			if logger, ok := l.(zerolog.Logger); ok {
				return logger
			}
		}
	}
	return Logger
}

// FromStdContext retrieves the logger from a standard context.Context.
func FromStdContext(ctx context.Context) zerolog.Logger {
	if l := ctx.Value(loggerKey); l != nil {
		if logger, ok := l.(zerolog.Logger); ok {
			return logger
		}
	}
	return Logger
}

// WithContext attaches a logger to a standard context.Context.
func WithContext(ctx context.Context, logger zerolog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

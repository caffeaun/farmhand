package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"

	"github.com/caffeaun/farmhand/internal/api"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/device"
	embedui "github.com/caffeaun/farmhand/internal/embed"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/caffeaun/farmhand/internal/job"
	farmlog "github.com/caffeaun/farmhand/internal/log"
	"github.com/caffeaun/farmhand/internal/notify"
)

// serveCmd starts the FarmHand HTTP server.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the FarmHand server",
	RunE:  runServe,
}

// runServe is the entry point for the serve subcommand. It wires all services,
// builds the gin router, and starts the HTTP server with graceful shutdown.
// Configuration is loaded by the root command's PersistentPreRunE into the
// global cfg variable before this function is called.
func runServe(_ *cobra.Command, _ []string) error {
	// Initialise the global logger before any other component logs.
	farmlog.Init("info", cfg.Server.DevMode)
	logger := farmlog.Logger

	logger.Info().
		Str("version", version).
		Str("host", cfg.Server.Host).
		Int("port", cfg.Server.Port).
		Msg("farmhand starting")

	// Open SQLite database. RunMigrations is called inside db.Open.
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close() //nolint:errcheck

	logger.Info().Str("path", cfg.Database.Path).Msg("database opened")

	// Create repositories.
	deviceRepo := db.NewDeviceRepository(database)
	jobRepo := db.NewJobRepository(database)
	jobResultRepo := db.NewJobResultRepository(database)

	// Create event bus.
	bus := events.New()
	defer bus.Close()

	// Create ADB bridge. A missing ADB binary is non-fatal; device
	// discovery is disabled but the server still starts.
	adbBridge, adbErr := device.NewADBBridge(cfg.Devices.ADBPath)
	if adbErr != nil {
		logger.Warn().Err(adbErr).Msg("ADB bridge unavailable; Android device discovery disabled")
	}

	// Create iOS bridge. Only available on macOS; silently nil elsewhere.
	iosBridge, iosErr := device.NewIOSBridge()
	if iosErr != nil {
		logger.Debug().Err(iosErr).Msg("iOS bridge unavailable; iOS device discovery disabled")
	}

	// Create device manager only when the ADB bridge is available.
	// The iOS bridge is optional and may be nil on non-macOS hosts.
	var deviceMgr *device.Manager
	if adbBridge != nil {
		pollInterval := time.Duration(cfg.Devices.PollIntervalSecs) * time.Second
		deviceMgr = device.NewManager(
			adbBridge,
			iosBridge,
			deviceRepo,
			bus,
			pollInterval,
			logger.With().Str("component", "device_manager").Logger(),
		)
	}

	// Create job services.
	logCollector := job.NewLogCollector(cfg.Jobs.LogDir)
	artifactCollector := job.NewArtifactCollector(cfg.Jobs.ArtifactDir)
	executor := job.NewExecutor(
		cfg.Jobs.LogDir,
		logger.With().Str("component", "executor").Logger(),
	)
	webhookNotifier := notify.New(
		cfg.Notifications.WebhookURL,
		logger.With().Str("component", "notifier").Logger(),
	)

	scheduler := job.NewScheduler(
		deviceMgr,
		jobRepo,
		deviceRepo,
		bus,
		logger.With().Str("component", "scheduler").Logger(),
	)

	runner := job.NewRunner(
		executor,
		jobRepo,
		jobResultRepo,
		deviceRepo,
		artifactCollector,
		webhookNotifier,
		bus,
		logger.With().Str("component", "runner").Logger(),
	)

	// Create WebSocket hub.
	hub := api.NewHub(
		deviceRepo,
		bus,
		logger.With().Str("component", "ws_hub").Logger(),
	)

	// Build gin router with all routes.
	if !cfg.Server.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	deps := api.RouterDeps{
		Config:            cfg,
		DeviceRepo:        deviceRepo,
		JobRepo:           jobRepo,
		JobResultRepo:     jobResultRepo,
		Scheduler:         scheduler,
		Runner:            runner,
		LogCollector:      logCollector,
		ArtifactCollector: artifactCollector,
		WSHub:             hub,
	}
	// Only set the interface field when the concrete pointer is non-nil.
	// A nil *device.Manager assigned to a deviceManagerAPI interface produces
	// a non-nil interface (typed nil), which bypasses the nil guard in NewRouter.
	if deviceMgr != nil {
		deps.DeviceManager = deviceMgr
	}

	router := api.NewRouter(
		api.RouterConfig{
			AuthToken:   cfg.Server.AuthToken,
			CORSOrigins: cfg.Server.CORSOrigins,
			Version:     version,
		},
		deps,
	)

	// Install the embedded UI handler as a NoRoute fallback so all non-API
	// paths serve the SPA.  This replaces the default JSON 404 handler set
	// by NewRouter.
	registerUIHandler(router)

	// Start background services after routing is configured.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if deviceMgr != nil {
		deviceMgr.Start(ctx)
		logger.Info().Msg("device manager started")
	}

	go hub.Run(ctx)
	logger.Info().Msg("websocket hub started")

	// Start HTTP server.
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		logger.Info().Str("addr", addr).Msg("listening")
		fmt.Printf("farmhand listening on %s\n", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("server error")
			os.Exit(1)
		}
	}()

	// Block until a termination signal is received.
	<-ctx.Done()
	stop() // stop receiving further signals
	logger.Info().Msg("shutdown signal received")

	// Graceful shutdown: give in-flight requests 30 seconds to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("server shutdown error")
		return fmt.Errorf("server shutdown: %w", err)
	}

	logger.Info().Msg("server stopped cleanly")
	return nil
}

// registerUIHandler serves the embedded SPA on all non-API routes.
// For paths that exist in the embedded FS, the file is served directly.
// For any other path (SPA deep links), index.html is returned so the
// client-side router can handle navigation.
func registerUIHandler(r *gin.Engine) {
	// Sub-filesystem rooted at the ui_dist directory inside the embedded FS.
	uiFS, err := fs.Sub(embedui.UI, "ui_dist")
	if err != nil {
		// This is a programming error (wrong embed path), not a runtime one.
		panic(fmt.Sprintf("failed to create UI sub-filesystem: %v", err))
	}

	fileServer := http.FileServer(http.FS(uiFS))

	// Serve any path that does not start with /api/ from the embedded UI.
	// Gin's NoRoute catch-all is used to intercept unmatched routes.
	// We replace the existing NoRoute handler set in NewRouter.
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// API routes that reach NoRoute return 404 JSON (already handled by
		// the authorised group; this branch handles any edge case).
		if len(path) >= 4 && path[:4] == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// Check whether the requested path exists in the embedded FS.
		// Strip the leading "/" to get a relative path for fs.Stat.
		relPath := path
		if len(relPath) > 0 && relPath[0] == '/' {
			relPath = relPath[1:]
		}

		if relPath == "" {
			relPath = "index.html"
		}

		_, statErr := fs.Stat(uiFS, relPath)
		if statErr != nil {
			// File not found in embedded FS — serve index.html for SPA routing.
			c.Request.URL.Path = "/"
		}

		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

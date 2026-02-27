package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/caffeaun/farmhand/internal/config"
	farmlog "github.com/caffeaun/farmhand/internal/log"
)

// RouterConfig holds the configuration needed to build the router.
type RouterConfig struct {
	// AuthToken is the static bearer token required for protected endpoints.
	// When empty, authentication is skipped entirely (dev/no-auth mode).
	AuthToken string

	// CORSOrigins is the list of allowed CORS origins. When empty or unset,
	// defaults to ["*"].
	//
	// SECURITY NOTE: Using "*" allows any browser origin to call this API. In
	// production deployments that use cookie-based authentication or are not
	// meant to be public, restrict this to specific trusted origins.
	CORSOrigins []string

	// Version is embedded in the /api/v1/health response.
	Version string
}

// RouterDeps holds service dependencies for API handlers.
// Add new service interfaces here as additional route groups are implemented.
type RouterDeps struct {
	// DeviceManager provides device management operations for device endpoints.
	// When nil, device routes are not registered.
	DeviceManager deviceManagerAPI

	// Config is the full application configuration, used by system routes.
	// When nil, system routes are not registered.
	Config *config.Config

	// DeviceRepo provides device listing for stats and WebSocket snapshot.
	// When nil, system and WebSocket routes are not registered.
	DeviceRepo statsDeviceRepoAPI

	// JobRepo is the job repository used by job, log, and system routes.
	// When nil, job and log routes are not registered.
	JobRepo jobStatsRepoAPI

	// JobResultRepo is the job result repository used by job, log, and artifact routes.
	// When nil, job and artifact routes are not registered.
	JobResultRepo jobResultRepoAPI

	// Scheduler schedules jobs onto available devices.
	Scheduler jobSchedulerAPI

	// Runner executes scheduled job executions.
	Runner jobRunnerAPI

	// LogCollector streams log output for running jobs.
	LogCollector logCollectorAPI

	// ArtifactCollector lists and reads job artifact files.
	ArtifactCollector artifactCollectorAPI

	// WSHub handles WebSocket connections and event broadcasting.
	WSHub *Hub
}

// jobStatsRepoAPI merges the jobRepoAPI and statsJobRepoAPI interfaces so a
// single *db.JobRepository value satisfies both job CRUD routes and system
// stats routes.
type jobStatsRepoAPI interface {
	jobRepoAPI
	statsJobRepoAPI
}

// NewRouter creates a gin.Engine with the full middleware stack applied in the
// following order:
//  1. Recovery   — catches panics and returns HTTP 500
//  2. RequestID  — generates X-Request-ID and sets it on context + response
//  3. Logging    — logs method, path, status, and latency via zerolog
//  4. CORS       — sets Access-Control-* headers and handles OPTIONS preflight
//  5. Auth       — validates Bearer token for protected route groups
//  6. Routes     — health (public) and /api/v1 group (protected)
func NewRouter(cfg RouterConfig, deps RouterDeps) *gin.Engine {
	r := gin.New()

	// 1. Recovery — must be first so panics in all later middleware are caught.
	r.Use(gin.Recovery())

	// 2. Request ID — attach before logging so the request ID appears in logs.
	r.Use(farmlog.RequestIDMiddleware())

	// 3. Request logging.
	r.Use(farmlog.RequestLoggerMiddleware())

	// 4. CORS.
	origins := cfg.CORSOrigins
	if len(origins) == 0 {
		origins = []string{"*"}
	}
	r.Use(corsMiddleware(origins))

	// Health endpoint — publicly accessible, no auth required.
	r.GET("/api/v1/health", healthHandler(cfg.Version))

	// Protected route group — all routes registered here require a valid token.
	authorized := r.Group("/api/v1")
	authorized.Use(authMiddleware(cfg.AuthToken))

	if deps.DeviceManager != nil {
		RegisterDeviceRoutes(authorized, deps.DeviceManager)
	}

	if deps.JobRepo != nil && deps.JobResultRepo != nil && deps.Scheduler != nil && deps.Runner != nil {
		RegisterJobRoutes(authorized, deps.JobRepo, deps.JobResultRepo, deps.Scheduler, deps.Runner)
	}

	if deps.JobRepo != nil && deps.JobResultRepo != nil && deps.LogCollector != nil {
		RegisterLogRoutes(authorized, deps.JobRepo, deps.JobResultRepo, deps.LogCollector)
	}

	if deps.JobRepo != nil && deps.JobResultRepo != nil && deps.ArtifactCollector != nil {
		RegisterArtifactRoutes(authorized, deps.JobRepo, deps.JobResultRepo, deps.ArtifactCollector)
	}

	if deps.Config != nil && deps.DeviceRepo != nil && deps.JobRepo != nil {
		RegisterSystemRoutes(authorized, deps.Config, deps.DeviceRepo, deps.JobRepo)
	}

	if deps.WSHub != nil {
		RegisterWSRoutes(authorized, deps.WSHub)
	}

	// 404 catch-all — overridden by serve.go to install the UI SPA fallback.
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})

	return r
}

// corsMiddleware returns a gin.HandlerFunc that sets CORS headers on every
// response and handles OPTIONS preflight requests with HTTP 204.
//
// origins is the slice of allowed origins. Pass []string{"*"} to allow all
// origins (not recommended for production deployments with authentication).
func corsMiddleware(origins []string) gin.HandlerFunc {
	allowOrigin := strings.Join(origins, ", ")

	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", allowOrigin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")

		// Handle preflight without invoking downstream handlers.
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// AuthMiddlewareFunc is the exported equivalent of authMiddleware. It is used
// by the serve command to apply authentication to route groups it registers
// after the initial call to NewRouter.
func AuthMiddlewareFunc(token string) gin.HandlerFunc {
	return authMiddleware(token)
}

// authMiddleware returns a gin.HandlerFunc that validates the static bearer
// token for all routes it is applied to.
//
// Token resolution order:
//  1. Authorization: Bearer <token> header
//  2. ?token= query parameter (for WebSocket upgrade requests)
//
// When token is empty the middleware is a no-op (all requests pass through).
// Comparison is performed with subtle.ConstantTimeCompare to prevent
// timing-based token enumeration.
func authMiddleware(token string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// No token configured — authentication is disabled.
		if token == "" {
			c.Next()
			return
		}

		provided := extractToken(c)
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		c.Next()
	}
}

// extractToken returns the bearer token from the Authorization header or the
// ?token= query parameter. Returns an empty string when neither is present.
func extractToken(c *gin.Context) string {
	if header := c.GetHeader("Authorization"); header != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(header, prefix) {
			return header[len(prefix):]
		}
		return ""
	}
	return c.Query("token")
}

// healthHandler returns a handler that reports server status and uptime.
// startTime is captured once at router construction so uptime is stable across
// requests.
func healthHandler(version string) gin.HandlerFunc {
	startTime := time.Now()
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":         "ok",
			"version":        version,
			"uptime_seconds": int(time.Since(startTime).Seconds()),
		})
	}
}

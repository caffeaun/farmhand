package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	farmlog "github.com/caffeaun/farmhand/internal/log"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Silence zerolog output during tests.
	farmlog.Init("disabled", false)
}

// newTestRouter creates a router with a known auth token for testing.
func newTestRouter(cfg RouterConfig) *gin.Engine {
	return NewRouter(cfg, RouterDeps{})
}

// doRequest performs an HTTP request against the router and returns the recorder.
func doRequest(r *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestHealthEndpoint_NoAuth verifies GET /api/v1/health returns 200 with the
// expected JSON body without requiring an auth token.
func TestHealthEndpoint_NoAuth(t *testing.T) {
	r := newTestRouter(RouterConfig{
		AuthToken: "secret",
		Version:   "test-version",
	})

	rec := doRequest(r, http.MethodGet, "/api/v1/health", nil)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "test-version", body["version"])
	_, hasUptime := body["uptime_seconds"]
	assert.True(t, hasUptime, "response should contain uptime_seconds")
}

// TestNotFound_Returns404JSON verifies that unmatched routes return HTTP 404
// with a JSON error body.
func TestNotFound_Returns404JSON(t *testing.T) {
	r := newTestRouter(RouterConfig{})

	rec := doRequest(r, http.MethodGet, "/api/v1/nonexistent", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "not found", body["error"])
}

// TestCORS_PreflightReturns204 verifies that OPTIONS requests return HTTP 204
// with the required CORS headers set.
func TestCORS_PreflightReturns204(t *testing.T) {
	r := newTestRouter(RouterConfig{
		CORSOrigins: []string{"https://example.com"},
	})

	rec := doRequest(r, http.MethodOptions, "/api/v1/health", map[string]string{
		"Origin": "https://example.com",
	})

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Methods"))
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Headers"))
}

// TestCORS_WildcardDefault verifies the default CORS origin is "*" when no
// origins are provided in config.
func TestCORS_WildcardDefault(t *testing.T) {
	r := newTestRouter(RouterConfig{})

	rec := doRequest(r, http.MethodGet, "/api/v1/health", nil)

	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}

// TestRequestID_Header verifies that every response carries an X-Request-ID
// header regardless of whether auth is required.
func TestRequestID_Header(t *testing.T) {
	r := newTestRouter(RouterConfig{})

	rec := doRequest(r, http.MethodGet, "/api/v1/health", nil)

	requestID := rec.Header().Get("X-Request-ID")
	assert.NotEmpty(t, requestID, "X-Request-ID header must be present")
}

// TestRequestID_HeaderOnNotFound verifies that even 404 responses include
// an X-Request-ID header (middleware runs before NoRoute handler).
func TestRequestID_HeaderOnNotFound(t *testing.T) {
	r := newTestRouter(RouterConfig{})

	rec := doRequest(r, http.MethodGet, "/does/not/exist", nil)

	assert.NotEmpty(t, rec.Header().Get("X-Request-ID"))
}

// TestAuthMiddleware_ValidToken verifies that a request with a valid Bearer
// token reaches the protected route group (returns non-401).
func TestAuthMiddleware_ValidToken(t *testing.T) {
	const token = "my-secret-token"
	r := newTestRouter(RouterConfig{AuthToken: token})

	// Register a test endpoint inside the protected group.
	protected := r.Group("/api/v1")
	protected.Use(authMiddleware(token))
	protected.GET("/protected-ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true})
	})

	rec := doRequest(r, http.MethodGet, "/api/v1/protected-ping", map[string]string{
		"Authorization": "Bearer " + token,
	})

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestAuthMiddleware_MissingToken verifies that a request with no Authorization
// header or token query param returns HTTP 401.
func TestAuthMiddleware_MissingToken(t *testing.T) {
	const token = "my-secret-token"
	r := gin.New()
	r.Use(authMiddleware(token))
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true})
	})

	rec := doRequest(r, http.MethodGet, "/ping", nil)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "unauthorized", body["error"])
}

// TestAuthMiddleware_WrongToken verifies that a request with an incorrect
// bearer token returns HTTP 401.
func TestAuthMiddleware_WrongToken(t *testing.T) {
	const token = "correct-token"
	r := gin.New()
	r.Use(authMiddleware(token))
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"pong": true})
	})

	rec := doRequest(r, http.MethodGet, "/ping", map[string]string{
		"Authorization": "Bearer wrong-token",
	})

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAuthMiddleware_QueryParam verifies that the ?token= query parameter is
// accepted as an alternative to the Authorization header (needed for WebSocket
// upgrade requests which cannot set custom headers from browsers).
func TestAuthMiddleware_QueryParam(t *testing.T) {
	const token = "ws-token"
	r := gin.New()
	r.Use(authMiddleware(token))
	r.GET("/ws", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/ws?token="+token, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestAuthMiddleware_QueryParam_Wrong verifies that a wrong ?token= query param
// returns HTTP 401.
func TestAuthMiddleware_QueryParam_Wrong(t *testing.T) {
	const token = "ws-token"
	r := gin.New()
	r.Use(authMiddleware(token))
	r.GET("/ws", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/ws?token=bad-token", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAuthMiddleware_EmptyConfig verifies that when AuthToken is empty, the
// auth middleware is a no-op and all requests pass through regardless of headers.
func TestAuthMiddleware_EmptyConfig(t *testing.T) {
	r := gin.New()
	r.Use(authMiddleware(""))
	r.GET("/open", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	tests := []struct {
		name   string
		header string
	}{
		{name: "no header", header: ""},
		{name: "random header value", header: "Bearer some-random-value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{}
			if tt.header != "" {
				headers["Authorization"] = tt.header
			}
			rec := doRequest(r, http.MethodGet, "/open", headers)
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// TestCORS_ExposeHeader verifies that X-Request-ID is listed in
// Access-Control-Expose-Headers so browsers can read it from JavaScript.
func TestCORS_ExposeHeader(t *testing.T) {
	r := newTestRouter(RouterConfig{})

	rec := doRequest(r, http.MethodGet, "/api/v1/health", nil)

	assert.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "X-Request-ID")
}

// TestHealthEndpoint_UptimeIncreases is a basic sanity check that uptime_seconds
// is a non-negative integer.
func TestHealthEndpoint_UptimeIncreases(t *testing.T) {
	r := newTestRouter(RouterConfig{Version: "v0.1.0"})

	rec := doRequest(r, http.MethodGet, "/api/v1/health", nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	uptime, ok := body["uptime_seconds"].(float64)
	require.True(t, ok, "uptime_seconds should be numeric")
	assert.GreaterOrEqual(t, uptime, float64(0))
}

// TestExtractToken covers the extractToken helper directly.
func TestExtractToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		header  string
		query   string
		want    string
	}{
		{name: "bearer header", header: "Bearer abc123", want: "abc123"},
		{name: "query param", query: "abc123", want: "abc123"},
		{name: "header takes precedence", header: "Bearer header-token", query: "query-token", want: "header-token"},
		{name: "malformed header no Bearer prefix", header: "Token abc123", want: ""},
		{name: "empty", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			url := "/test"
			if tt.query != "" {
				url += "?token=" + tt.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			c.Request = req

			got := extractToken(c)
			assert.Equal(t, tt.want, got)
		})
	}
}

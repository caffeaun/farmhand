package notify_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/caffeaun/farmhand/internal/notify"
)

// discardLogger returns a zerolog.Logger that discards all output.
func discardLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

func TestSendSync_POSTsJSONWithContentType(t *testing.T) {
	var gotMethod, gotContentType string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventJobStarted,
		Payload:   map[string]string{"job_id": "abc123"},
		Timestamp: time.Now().UTC().Truncate(time.Second),
	}

	err := n.SendSync(event)
	if err != nil {
		t.Fatalf("SendSync returned error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", gotContentType)
	}
	if len(gotBody) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestSendSync_CorrectEventTypeAndPayload(t *testing.T) {
	var received notify.WebhookEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("unmarshal body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventDeviceOnline,
		Payload:   map[string]string{"device_id": "device-42"},
		Timestamp: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
	}

	if err := n.SendSync(event); err != nil {
		t.Fatalf("SendSync returned error: %v", err)
	}

	if received.Type != notify.EventDeviceOnline {
		t.Errorf("expected type %q, got %q", notify.EventDeviceOnline, received.Type)
	}
	// Payload is decoded as map[string]interface{} by json.Unmarshal into interface{}
	payload, ok := received.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload is not a map, got %T", received.Payload)
	}
	if payload["device_id"] != "device-42" {
		t.Errorf("expected device_id=device-42, got %v", payload["device_id"])
	}
}

func TestSendSync_EmptyWebhookURL_NoRequest(t *testing.T) {
	requestMade := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMade = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use empty URL — server URL is irrelevant, no request should be made
	n := notify.New("", discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventJobFailed,
		Payload:   nil,
		Timestamp: time.Now(),
	}

	err := n.SendSync(event)
	if err != nil {
		t.Fatalf("expected nil error for empty URL, got %v", err)
	}
	if requestMade {
		t.Error("expected no HTTP request for empty webhook URL")
	}
}

func TestSendSync_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventJobCompleted,
		Payload:   nil,
		Timestamp: time.Now(),
	}

	err := n.SendSync(event)
	if err == nil {
		t.Fatal("expected error for non-2xx status, got nil")
	}
}

func TestSendSync_Non2xxErrorContainsStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventJobFailed,
		Payload:   nil,
		Timestamp: time.Now(),
	}

	err := n.SendSync(event)
	if err == nil {
		t.Fatal("expected error for 502 status, got nil")
	}
	expected := "webhook returned status 502"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestSendSync_Timeout(t *testing.T) {
	done := make(chan struct{})

	// Handler blocks until the test signals done (client timed out)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	}))

	// Use a notifier with a very short timeout via a custom client
	shortClient := &http.Client{Timeout: 50 * time.Millisecond}
	n := notify.NewWithClient(srv.URL, shortClient, discardLogger())

	event := notify.WebhookEvent{
		Type:      notify.EventJobStarted,
		Payload:   nil,
		Timestamp: time.Now(),
	}

	start := time.Now()
	err := n.SendSync(event)
	elapsed := time.Since(start)

	// Signal handler to unblock, then close server
	close(done)
	srv.Close()

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// Should complete well under the 15-second handler sleep
	if elapsed > 2*time.Second {
		t.Errorf("expected timeout before 2s, took %v", elapsed)
	}
}

func TestSend_Async_WebhookCalled(t *testing.T) {
	called := make(chan struct{}, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := notify.New(srv.URL, discardLogger())
	event := notify.WebhookEvent{
		Type:      notify.EventDeviceOffline,
		Payload:   map[string]string{"device_id": "dev-99"},
		Timestamp: time.Now(),
	}

	n.Send(event)

	select {
	case <-called:
		// success — webhook was invoked asynchronously
	case <-time.After(3 * time.Second):
		t.Fatal("webhook was not called within 3 seconds")
	}
}

// Package notify handles outbound notifications via webhooks.
// No retry logic is implemented in the MVP — webhook failures are logged at
// warn level and the caller is not blocked.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// WebhookEvent represents an event to send via webhook.
type WebhookEvent struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

// Event type constants for device and job lifecycle events.
const (
	EventDeviceOnline  = "device.online"
	EventDeviceOffline = "device.offline"
	EventJobStarted    = "job.started"
	EventJobCompleted  = "job.completed"
	EventJobFailed     = "job.failed"
)

// Notifier sends webhook notifications for system events.
type Notifier struct {
	webhookURL string
	client     *http.Client
	logger     zerolog.Logger
}

// New creates a Notifier. If webhookURL is empty, Send() is a no-op.
func New(webhookURL string, logger zerolog.Logger) *Notifier {
	return &Notifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger,
	}
}

// NewWithClient creates a Notifier with a custom HTTP client.
// Intended for testing scenarios that require timeout control.
func NewWithClient(webhookURL string, client *http.Client, logger zerolog.Logger) *Notifier {
	return &Notifier{
		webhookURL: webhookURL,
		client:     client,
		logger:     logger,
	}
}

// Send posts the event to the configured webhook URL asynchronously.
// If webhookURL is empty, this is a no-op.
// Fires in a goroutine with recover() so panics don't crash the server.
// No retry logic — failures are logged at warn level.
func (n *Notifier) Send(event WebhookEvent) {
	if n.webhookURL == "" {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				n.logger.Error().Interface("panic", r).Msg("webhook notifier panic recovered")
			}
		}()

		if err := n.send(event); err != nil {
			n.logger.Warn().Err(err).Str("type", event.Type).Msg("webhook notification failed")
		}
	}()
}

// send performs the actual HTTP POST (called from goroutine or SendSync).
func (n *Notifier) send(event WebhookEvent) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	return nil
}

// SendSync is like Send but blocks until the webhook completes.
// Intended for use in tests. Returns nil when webhookURL is empty.
func (n *Notifier) SendSync(event WebhookEvent) error {
	if n.webhookURL == "" {
		return nil
	}
	return n.send(event)
}

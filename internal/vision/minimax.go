package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MiniMaxClient is the Client implementation for MiniMax's OpenAI-compatible
// chat-completions endpoint. The request shape is the standard OpenAI
// `image_url` + `tools` / `tool_choice` combination, which forces the model
// to populate a single named function's arguments so we don't need to coax
// JSON out of free-form text.
//
// Reference: https://platform.minimax.io/docs/api-reference/text-openai-api
type MiniMaxClient struct {
	BaseURL    string
	APIKey     string
	Model      string
	Detail     string // "low" | "default" | "high"; defaults to "high" when empty
	HTTPClient *http.Client
}

// NewMiniMaxClient builds a MiniMaxClient with sensible defaults for any
// unset field. Pass timeout 0 to use http.Client's zero (no timeout); the
// CLI passes a non-zero value derived from VisionConfig.TimeoutSec.
func NewMiniMaxClient(baseURL, apiKey, model, detail string, timeout time.Duration) *MiniMaxClient {
	if baseURL == "" {
		baseURL = "https://api.minimax.io/v1"
	}
	if model == "" {
		model = "MiniMax-M3"
	}
	if detail == "" {
		detail = "high"
	}
	hc := &http.Client{Timeout: timeout}
	return &MiniMaxClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		Model:      model,
		Detail:     detail,
		HTTPClient: hc,
	}
}

// inspectSystemPrompt drives the model toward a deterministic shape. It
// is short on purpose: the JSON schema attached to the tool definition is
// what really constrains the output. The prompt just nudges the model to
// enumerate everything it sees rather than focus on one element.
const inspectSystemPrompt = `You are inspecting an Android device screenshot. Identify each distinct UI element visible on the screen. For every element, return its bounding box in screenshot pixel space (origin top-left), a short descriptive name (e.g. "Login button", "Cart icon", "Search bar"), and best-effort metadata for color, type, and visible text. Be thorough but do not invent elements that are not on the screen.`

// Inspect sends png to MiniMax-M3 with a forced tool-call and returns
// the structured topic list the model produced.
func (c *MiniMaxClient) Inspect(ctx context.Context, png []byte) (InspectResult, error) {
	if c.APIKey == "" {
		return InspectResult{}, fmt.Errorf("vision: API key is empty")
	}
	if len(png) == 0 {
		return InspectResult{}, fmt.Errorf("vision: empty image")
	}

	body := miniMaxRequest{
		Model: c.Model,
		Messages: []miniMaxMessage{
			{
				Role:    "system",
				Content: []miniMaxContent{{Type: "text", Text: inspectSystemPrompt}},
			},
			{
				Role: "user",
				Content: []miniMaxContent{
					{Type: "text", Text: "Inspect this screenshot and return the topic list."},
					{
						Type: "image_url",
						ImageURL: &miniMaxImageURL{
							URL:    "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
							Detail: c.Detail,
						},
					},
				},
			},
		},
		Tools: []miniMaxTool{{
			Type: "function",
			Function: miniMaxFunctionDef{
				Name:        "report_inspection",
				Description: "Report the list of UI elements visible in the screenshot.",
				Parameters:  reportInspectionSchema,
			},
		}},
		ToolChoice: &miniMaxToolChoice{
			Type:     "function",
			Function: miniMaxToolChoiceFunc{Name: "report_inspection"},
		},
		MaxTokens: 2048,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return InspectResult{}, fmt.Errorf("vision: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return InspectResult{}, fmt.Errorf("vision: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return InspectResult{}, fmt.Errorf("vision: HTTP error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return InspectResult{}, fmt.Errorf("vision: read response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := string(respBody)
		if len(preview) > 512 {
			preview = preview[:512] + "..."
		}
		return InspectResult{}, fmt.Errorf("vision: provider returned HTTP %d: %s", resp.StatusCode, preview)
	}

	var parsed miniMaxResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return InspectResult{}, fmt.Errorf("vision: parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return InspectResult{}, fmt.Errorf("vision: provider returned no choices")
	}
	tc := parsed.Choices[0].Message.ToolCalls
	if len(tc) == 0 || tc[0].Function.Arguments == "" {
		return InspectResult{}, fmt.Errorf("vision: provider returned no tool call; raw content=%q", parsed.Choices[0].Message.Content)
	}

	var out InspectResult
	if err := json.Unmarshal([]byte(tc[0].Function.Arguments), &out); err != nil {
		return InspectResult{}, fmt.Errorf("vision: parse tool-call arguments %q: %w", tc[0].Function.Arguments, err)
	}
	return out, nil
}

// --- MiniMax / OpenAI-compatible wire types ---

type miniMaxRequest struct {
	Model      string             `json:"model"`
	Messages   []miniMaxMessage   `json:"messages"`
	Tools      []miniMaxTool      `json:"tools,omitempty"`
	ToolChoice *miniMaxToolChoice `json:"tool_choice,omitempty"`
	MaxTokens  int                `json:"max_tokens,omitempty"`
}

type miniMaxMessage struct {
	Role    string           `json:"role"`
	Content []miniMaxContent `json:"content"`
}

type miniMaxContent struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *miniMaxImageURL `json:"image_url,omitempty"`
}

type miniMaxImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type miniMaxTool struct {
	Type     string             `json:"type"`
	Function miniMaxFunctionDef `json:"function"`
}

type miniMaxFunctionDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type miniMaxToolChoice struct {
	Type     string                `json:"type"`
	Function miniMaxToolChoiceFunc `json:"function"`
}

type miniMaxToolChoiceFunc struct {
	Name string `json:"name"`
}

type miniMaxResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// reportInspectionSchema is the JSON Schema embedded in the tool
// definition. Constrains the model to a list of topics with required
// name + bounding box and optional metadata (color, type, text).
var reportInspectionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"topics": map[string]interface{}{
			"type":        "array",
			"description": "Distinct UI elements visible in the screenshot.",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Short descriptive label for the element.",
					},
					"coordinates": map[string]interface{}{
						"type":        "object",
						"description": "Axis-aligned bounding box in screenshot pixel space (origin top-left).",
						"properties": map[string]interface{}{
							"x1": map[string]interface{}{"type": "integer"},
							"y1": map[string]interface{}{"type": "integer"},
							"x2": map[string]interface{}{"type": "integer"},
							"y2": map[string]interface{}{"type": "integer"},
						},
						"required": []interface{}{"x1", "y1", "x2", "y2"},
					},
					"color": map[string]interface{}{
						"type":        "string",
						"description": "Dominant or distinctive color (e.g. 'blue', '#1A73E8').",
					},
					"type": map[string]interface{}{
						"type":        "string",
						"description": "Element category (button, text, input, icon, image, etc.).",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "Visible text content of the element, if any.",
					},
				},
				"required": []interface{}{"name", "coordinates"},
			},
		},
	},
	"required": []interface{}{"topics"},
}

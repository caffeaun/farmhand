// Package vision provides clients for vision-LLM providers that accept a
// screenshot and return structured inspection results — a list of detected
// UI elements with bounding boxes and metadata.
//
// MiniMax-M3 is the only backend implemented today; the Client interface
// allows additional providers (Claude, GPT-4o, Gemini, specialized GUI
// grounding models) to be added without touching call sites.
package vision

import "context"

// Box is an axis-aligned bounding box in pixel space. x1, y1 is the
// top-left corner and x2, y2 the bottom-right. Origin is the top-left of
// the screenshot. All coordinates are non-negative integers.
type Box struct {
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
	X2 int `json:"x2"`
	Y2 int `json:"y2"`
}

// Topic describes one detected UI element. Name + Coordinates are always
// populated; Color, Type, and Text are best-effort and may be empty when
// the model could not infer them.
type Topic struct {
	Name        string `json:"name"`
	Coordinates Box    `json:"coordinates"`
	Color       string `json:"color,omitempty"`
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
}

// InspectResult is the structured payload every Client returns.
type InspectResult struct {
	Topics []Topic `json:"topics"`
}

// Client is the contract for any vision-inspection backend. The caller
// passes a PNG (or other supported image format) and receives the list of
// topics the model identified.
type Client interface {
	Inspect(ctx context.Context, png []byte) (InspectResult, error)
}

package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cannedSuccessBody returns a MiniMax chat-completion response that
// populates one tool call with the given arguments JSON string.
func cannedSuccessBody(args string) string {
	resp := miniMaxResponse{}
	resp.Choices = make([]struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}, 1)
	resp.Choices[0].Message.ToolCalls = make([]struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}, 1)
	resp.Choices[0].Message.ToolCalls[0].Function.Name = "report_inspection"
	resp.Choices[0].Message.ToolCalls[0].Function.Arguments = args
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestServer returns an httptest server that calls handler when
// /chat/completions is hit; everything else returns 404.
func newTestServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMiniMax_Inspect_HappyPath(t *testing.T) {
	pngBytes := []byte{0x89, 'P', 'N', 'G', 'X', 'Y'}
	var (
		gotAuth        string
		gotContentType string
		gotBody        miniMaxRequest
	)

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		bodyBytes, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(bodyBytes, &gotBody); err != nil {
			t.Errorf("server: parse body: %v", err)
		}
		_, _ = fmt.Fprint(w, cannedSuccessBody(`{
			"topics": [
				{
					"name": "Login button",
					"coordinates": {"x1": 100, "y1": 1700, "x2": 980, "y2": 1900},
					"color": "blue",
					"type": "button",
					"text": "Sign In"
				},
				{
					"name": "Email field",
					"coordinates": {"x1": 60, "y1": 1100, "x2": 1020, "y2": 1250},
					"type": "input"
				}
			]
		}`))
	})

	client := NewMiniMaxClient(srv.URL, "secret-key", "MiniMax-M3", "high", 5*time.Second)
	res, err := client.Inspect(context.Background(), pngBytes)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	if len(res.Topics) != 2 {
		t.Fatalf("topics len = %d, want 2", len(res.Topics))
	}

	login := res.Topics[0]
	if login.Name != "Login button" {
		t.Errorf("topics[0].Name = %q", login.Name)
	}
	wantBox := Box{X1: 100, Y1: 1700, X2: 980, Y2: 1900}
	if login.Coordinates != wantBox {
		t.Errorf("topics[0].Coordinates = %+v, want %+v", login.Coordinates, wantBox)
	}
	if login.Color != "blue" || login.Type != "button" || login.Text != "Sign In" {
		t.Errorf("topics[0] metadata = (color=%q, type=%q, text=%q)", login.Color, login.Type, login.Text)
	}

	// Optional fields absent from response should round-trip as empty.
	email := res.Topics[1]
	if email.Color != "" || email.Text != "" {
		t.Errorf("topics[1] optional fields should be empty, got color=%q text=%q", email.Color, email.Text)
	}
	if email.Type != "input" {
		t.Errorf("topics[1].Type = %q, want input", email.Type)
	}

	// Outgoing request shape.
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if gotBody.Model != "MiniMax-M3" {
		t.Errorf("model = %q", gotBody.Model)
	}

	// System prompt is the first message; image is on the user message.
	if len(gotBody.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2 (system + user)", len(gotBody.Messages))
	}
	if gotBody.Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want system", gotBody.Messages[0].Role)
	}
	if gotBody.Messages[1].Role != "user" {
		t.Errorf("second message role = %q, want user", gotBody.Messages[1].Role)
	}

	// User message carries the image_url part.
	userContent := gotBody.Messages[1].Content
	var imgPart *miniMaxContent
	for i := range userContent {
		if userContent[i].Type == "image_url" {
			imgPart = &userContent[i]
			break
		}
	}
	if imgPart == nil || imgPart.ImageURL == nil {
		t.Fatalf("missing image_url part in user message: %+v", userContent)
	}
	wantPrefix := "data:image/png;base64,"
	if !strings.HasPrefix(imgPart.ImageURL.URL, wantPrefix) {
		t.Errorf("image url prefix = %q", imgPart.ImageURL.URL)
	}
	encoded := strings.TrimPrefix(imgPart.ImageURL.URL, wantPrefix)
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != string(pngBytes) {
		t.Errorf("decoded png = %q, want %q", decoded, pngBytes)
	}
	if imgPart.ImageURL.Detail != "high" {
		t.Errorf("detail = %q", imgPart.ImageURL.Detail)
	}

	// Forced tool call.
	if gotBody.ToolChoice == nil ||
		gotBody.ToolChoice.Type != "function" ||
		gotBody.ToolChoice.Function.Name != "report_inspection" {
		t.Errorf("tool_choice = %+v, want forced function call to report_inspection", gotBody.ToolChoice)
	}
	if len(gotBody.Tools) != 1 || gotBody.Tools[0].Function.Name != "report_inspection" {
		t.Errorf("tools = %+v, want one report_inspection function", gotBody.Tools)
	}
}

func TestMiniMax_Inspect_EmptyTopicsAllowed(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, cannedSuccessBody(`{"topics": []}`))
	})

	client := NewMiniMaxClient(srv.URL, "k", "", "", 5*time.Second)
	res, err := client.Inspect(context.Background(), []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if len(res.Topics) != 0 {
		t.Errorf("topics = %+v, want empty", res.Topics)
	}
}

func TestMiniMax_RejectsEmptyInputs(t *testing.T) {
	client := NewMiniMaxClient("http://unused", "k", "", "", 0)

	if _, err := client.Inspect(context.Background(), nil); err == nil {
		t.Error("expected error for empty image")
	}

	client2 := NewMiniMaxClient("http://unused", "", "", "", 0)
	if _, err := client2.Inspect(context.Background(), []byte{1}); err == nil {
		t.Error("expected error for empty api key")
	}
}

func TestMiniMax_PropagatesNon2xx(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"image too large"}`))
	})

	client := NewMiniMaxClient(srv.URL, "k", "", "", 5*time.Second)
	_, err := client.Inspect(context.Background(), []byte{1, 2, 3})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("err = %v, want HTTP 400 in message", err)
	}
}

func TestMiniMax_MalformedJSON(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})

	client := NewMiniMaxClient(srv.URL, "k", "", "", 5*time.Second)
	_, err := client.Inspect(context.Background(), []byte{1})
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Errorf("err = %v, want parse-response error", err)
	}
}

func TestMiniMax_NoToolCall(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"sorry","tool_calls":[]}}]}`))
	})

	client := NewMiniMaxClient(srv.URL, "k", "", "", 5*time.Second)
	_, err := client.Inspect(context.Background(), []byte{1})
	if err == nil || !strings.Contains(err.Error(), "no tool call") {
		t.Errorf("err = %v, want no-tool-call error", err)
	}
}

func TestMiniMax_BadToolCallArguments(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, cannedSuccessBody("not json {"))
	})

	client := NewMiniMaxClient(srv.URL, "k", "", "", 5*time.Second)
	_, err := client.Inspect(context.Background(), []byte{1})
	if err == nil || !strings.Contains(err.Error(), "parse tool-call arguments") {
		t.Errorf("err = %v, want parse-tool-call-arguments error", err)
	}
}

func TestMiniMax_ContextCancellation(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	client := NewMiniMaxClient(srv.URL, "k", "", "", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := client.Inspect(ctx, []byte{1})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

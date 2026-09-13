package telegram_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/am-kenny/ampulsar/internal/telegram"
)

const token = "test_token"

func newClient(t *testing.T, status int, body string) *telegram.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return telegram.NewClient(token, telegram.WithBaseURL(srv.URL))
}

func wantAPIError(t *testing.T, err error) *telegram.APIError {
	t.Helper()
	var e *telegram.APIError
	if !errors.As(err, &e) {
		t.Fatalf("want *APIError, got %v", err)
	}
	return e
}

func TestSendMessage(t *testing.T) {
	c := newClient(t, 200, `{"ok":true,"result":{"message_id":4471}}`)
	id, err := c.SendMessage(context.Background(), "c", "hi", telegram.ParseHTML)
	if err != nil || id != 4471 {
		t.Fatalf("id=%d err=%v", id, err)
	}
}

func TestStructuredAPIError(t *testing.T) {
	c := newClient(t, 400, `{"ok":false,"description":"chat not found","error_code":400}`)
	_, err := c.SendMessage(context.Background(), "c", "t", telegram.ParseHTML)
	if e := wantAPIError(t, err); e.StatusCode != 400 || e.Description != "chat not found" {
		t.Errorf("apiErr = %+v", e)
	}
}

func TestNonJSONError(t *testing.T) {
	c := newClient(t, 502, "<html>502</html>")
	_, err := c.SendMessage(context.Background(), "c", "t", telegram.ParseHTML)
	var e *telegram.APIError
	if err == nil || errors.As(err, &e) {
		t.Fatalf("want plain non-JSON error, got %v", err)
	}
}

func TestRetryAfter(t *testing.T) {
	c := newClient(t, 429, `{"ok":false,"error_code":429,"parameters":{"retry_after":30}}`)
	_, err := c.SendMessage(context.Background(), "c", "t", telegram.ParseHTML)
	if e := wantAPIError(t, err); e.RetryAfter() != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", e.RetryAfter())
	}
}

func TestRequestShape(t *testing.T) {
	basePath := "/bot" + token

	// result shapes differ by method: send/edit return a message object,
	// delete/pin/unpin return a bool
	const (
		respMessage = `{"ok":true,"result":{"message_id":1}}`
		respBool    = `{"ok":true,"result":true}`
	)

	tests := []struct {
		name     string
		call     func(context.Context, *telegram.Client) error
		wantPath string
		wantBody map[string]any
		resp     string
	}{
		{
			name: "sendMessage",
			call: func(ctx context.Context, c *telegram.Client) error {
				_, err := c.SendHTMLMessage(ctx, "c1", "hi")
				return err
			},
			wantPath: basePath + "/sendMessage",
			wantBody: map[string]any{"chat_id": "c1", "text": "hi", "parse_mode": "HTML"},
			resp:     respMessage,
		},
		{
			name: "sendMessage plain omits parse_mode",
			call: func(ctx context.Context, c *telegram.Client) error {
				_, err := c.SendMessage(ctx, "c1", "hi", telegram.ParsePlainText)
				return err
			},
			wantPath: basePath + "/sendMessage",
			wantBody: map[string]any{"chat_id": "c1", "text": "hi"},
			resp:     respMessage,
		},
		{
			name: "editMessageText",
			call: func(ctx context.Context, c *telegram.Client) error {
				return c.EditHTMLMessageText(ctx, "c1", 42, "new")
			},
			wantPath: basePath + "/editMessageText",
			wantBody: map[string]any{"chat_id": "c1", "message_id": float64(42), "text": "new", "parse_mode": "HTML"},
			resp:     respMessage,
		},
		{
			name:     "deleteMessage",
			call:     func(ctx context.Context, c *telegram.Client) error { return c.DeleteMessage(ctx, "c1", 42) },
			wantPath: basePath + "/deleteMessage",
			wantBody: map[string]any{"chat_id": "c1", "message_id": float64(42)},
			resp:     respBool,
		},
		{
			name:     "pinChatMessage",
			call:     func(ctx context.Context, c *telegram.Client) error { return c.PinChatMessage(ctx, "c1", 42) },
			wantPath: basePath + "/pinChatMessage",
			wantBody: map[string]any{"chat_id": "c1", "message_id": float64(42)},
			resp:     respBool,
		},
		{
			name:     "unpinChatMessage",
			call:     func(ctx context.Context, c *telegram.Client) error { return c.UnpinChatMessage(ctx, "c1", 42) },
			wantPath: basePath + "/unpinChatMessage",
			wantBody: map[string]any{"chat_id": "c1", "message_id": float64(42)},
			resp:     respBool,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				b, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(b, &gotBody); err != nil {
					t.Errorf("request body not JSON: %v", err)
				}
				_, _ = w.Write([]byte(tt.resp))
			}))
			defer srv.Close()

			c := telegram.NewClient(token, telegram.WithBaseURL(srv.URL))
			if err := tt.call(context.Background(), c); err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if !maps.Equal(gotBody, tt.wantBody) {
				t.Errorf("body = %v, want %v", gotBody, tt.wantBody)
			}
		})
	}
}

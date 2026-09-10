package executor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestCodexRetryAfterExecutePreservesHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "37")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`))
	}))
	defer server.Close()

	exec := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}
	_, err := exec.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.6-luna",
		Payload: []byte(`{"model":"gpt-5.6-luna","input":"test"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want 429")
	}
	var retryAfterProvider interface {
		RetryAfter() *time.Duration
	}
	if !errors.As(err, &retryAfterProvider) || retryAfterProvider == nil {
		t.Fatalf("Execute() error %T does not expose RetryAfter", err)
	}
	if retryAfter := retryAfterProvider.RetryAfter(); retryAfter == nil || *retryAfter != 37*time.Second {
		t.Fatalf("RetryAfter() = %v, want 37s", retryAfter)
	}
}

func TestCodexRetryAfterBodyResetTakesPrecedence(t *testing.T) {
	body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":120}}`)
	err := newCodexStatusErrWithHeaders(
		http.StatusTooManyRequests,
		body,
		http.Header{"Retry-After": []string{"37"}},
		false,
	)
	if retryAfter := err.RetryAfter(); retryAfter == nil || *retryAfter != 120*time.Second {
		t.Fatalf("RetryAfter() = %v, want 2m from body reset", retryAfter)
	}
}

func TestCodexRetryAfterUsesStandardHeader(t *testing.T) {
	err := newCodexStatusErrWithHeaders(
		http.StatusTooManyRequests,
		[]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`),
		http.Header{"Retry-After": []string{"37"}},
		false,
	)
	if retryAfter := err.RetryAfter(); retryAfter == nil || *retryAfter != 37*time.Second {
		t.Fatalf("RetryAfter() = %v, want 37s", retryAfter)
	}
}

func TestCodexRetryAfterWebsocketErrorFrameUsesStandardHeader(t *testing.T) {
	err, ok := parseCodexWebsocketError([]byte(`{"type":"error","status":429,"error":{"code":"websocket_connection_limit_reached","message":"too many websockets"},"headers":{"retry-after":"37"}}`))
	if !ok {
		t.Fatal("parseCodexWebsocketError() did not recognize error frame")
	}
	var retryAfterProvider interface {
		RetryAfter() *time.Duration
	}
	if !errors.As(err, &retryAfterProvider) || retryAfterProvider == nil {
		t.Fatalf("websocket error %T does not expose RetryAfter", err)
	}
	if retryAfter := retryAfterProvider.RetryAfter(); retryAfter == nil || *retryAfter != 37*time.Second {
		t.Fatalf("RetryAfter() = %v, want 37s", retryAfter)
	}
	if got := cliproxyauth.SafeResponseHeaders(err).Get("Retry-After"); got != "37" {
		t.Fatalf("safe Retry-After header = %q, want 37", got)
	}
}

func TestCodexRetryAfterWebsocketBodyResetTakesPrecedence(t *testing.T) {
	err, ok := parseCodexWebsocketError([]byte(`{"type":"error","status":429,"body":{"error":{"type":"usage_limit_reached","resets_in_seconds":120}},"headers":{"retry-after":"37"}}`))
	if !ok {
		t.Fatal("parseCodexWebsocketError() did not recognize error frame")
	}
	var retryAfterProvider interface {
		RetryAfter() *time.Duration
	}
	if !errors.As(err, &retryAfterProvider) || retryAfterProvider == nil {
		t.Fatalf("websocket error %T does not expose RetryAfter", err)
	}
	if retryAfter := retryAfterProvider.RetryAfter(); retryAfter == nil || *retryAfter != 120*time.Second {
		t.Fatalf("RetryAfter() = %v, want 2m from body reset", retryAfter)
	}
}

func TestCodexRetryAfterParsesHTTPDate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	raw := now.Add(45 * time.Second).UTC().Format(http.TimeFormat)
	if retryAfter := parseCodexRetryAfterHeader(raw, now); retryAfter == nil || *retryAfter != 45*time.Second {
		t.Fatalf("parseCodexRetryAfterHeader(%q) = %v, want 45s", raw, retryAfter)
	}
}

func TestCodexRetryAfterRejectsNonStandardOrMissingValues(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, raw := range []string{"", "0", "+37", "1.5", "2026-09-04T12:00:00Z", "invalid", "9223372037"} {
		if retryAfter := parseCodexRetryAfterHeader(raw, now); retryAfter != nil {
			t.Fatalf("parseCodexRetryAfterHeader(%q) = %v, want nil", raw, *retryAfter)
		}
	}
	err := newCodexStatusErr(http.StatusTooManyRequests, []byte(`{"detail":"Rate limit exceeded"}`))
	if retryAfter := err.RetryAfter(); retryAfter != nil {
		t.Fatalf("headerless generic 429 RetryAfter() = %v, want nil without invented fallback", *retryAfter)
	}
}

func TestCodexRetryAfterIgnoresHeaderForNon429(t *testing.T) {
	err := newCodexStatusErrWithHeaders(
		http.StatusBadGateway,
		[]byte(`{"error":{"message":"upstream unavailable"}}`),
		http.Header{"Retry-After": []string{"37"}},
		false,
	)
	if retryAfter := err.RetryAfter(); retryAfter != nil {
		t.Fatalf("non-429 RetryAfter() = %v, want nil", *retryAfter)
	}
}

// Exercise the executor entrypoints so a patch to an unused compatibility helper
// cannot pass while the live HTTP or WebSocket paths lose retry or cooling state.
func TestCodexRetryAfterPreservesModelLevelCooling(t *testing.T) {
	for _, path := range []struct {
		name       string
		stream     bool
		websocket  bool
		errorFrame bool
		compact    bool
		imageModel string
	}{
		{name: "http"},
		{name: "compact", compact: true},
		{name: "http-stream", stream: true},
		{name: "image", imageModel: "gpt-image-1"},
		{name: "image-stream", imageModel: "gpt-image-1", stream: true},
		{name: "direct-image", imageModel: "gpt-image-2"},
		{name: "direct-image-stream", imageModel: "gpt-image-2", stream: true},
		{name: "websocket-handshake", websocket: true},
		{name: "websocket-stream-handshake", websocket: true, stream: true},
		{name: "websocket-frame", websocket: true, errorFrame: true},
		{name: "websocket-stream-frame", websocket: true, errorFrame: true, stream: true},
	} {
		for _, modelLevelCooling := range []bool{false, true} {
			for _, bodyReset := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/model-cooling=%t/body-reset=%t", path.name, modelLevelCooling, bodyReset), func(t *testing.T) {
					body := `{"error":{"type":"usage_limit_reached"}}`
					wantDelay := 37 * time.Second
					if bodyReset {
						body = `{"error":{"type":"usage_limit_reached","resets_in_seconds":120}}`
						wantDelay = 120 * time.Second
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if !path.errorFrame {
							w.Header().Set("Content-Type", "application/json")
							w.Header().Set("Retry-After", "37")
							w.WriteHeader(http.StatusTooManyRequests)
							_, _ = w.Write([]byte(body))
							return
						}
						upgrader := websocket.Upgrader{}
						conn, err := upgrader.Upgrade(w, r, nil)
						if err != nil {
							t.Errorf("upgrade websocket: %v", err)
							return
						}
						defer conn.Close()
						_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
						if _, _, err := conn.ReadMessage(); err != nil {
							t.Errorf("read websocket request: %v", err)
							return
						}
						payload := `{"type":"error","status":429,"body":` + body + `,"headers":{"retry-after":"37"}}`
						if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
							t.Errorf("write websocket error: %v", err)
						}
					}))
					defer server.Close()

					cfg := &config.Config{}
					cfg.Codex.ModelLevelCooling = modelLevelCooling
					auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL, "api_key": "test"}}
					req := cliproxyexecutor.Request{
						Model: "gpt-5.6-luna", Payload: []byte(`{"model":"gpt-5.6-luna","input":"test"}`),
					}
					opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response"), Stream: path.stream}
					if path.compact {
						opts.Alt = "responses/compact"
					}
					if path.imageModel != "" {
						req.Model = path.imageModel
						req.Payload = []byte(`{"model":"` + path.imageModel + `","prompt":"draw a tree"}`)
						opts.SourceFormat = sdktranslator.FromString(codexOpenAIImageSourceFormat)
						opts.Metadata = map[string]any{cliproxyexecutor.RequestPathMetadataKey: codexImagesGenerationsPath}
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					httpExec := NewCodexExecutor(cfg)
					execute, executeStream := httpExec.Execute, httpExec.ExecuteStream
					if path.websocket {
						wsExec := NewCodexWebsocketsExecutor(cfg)
						execute, executeStream = wsExec.Execute, wsExec.ExecuteStream
					}
					var err error
					if path.stream {
						var result *cliproxyexecutor.StreamResult
						result, err = executeStream(ctx, auth, req, opts)
						if err == nil && result != nil {
							for chunk := range result.Chunks {
								if chunk.Err != nil {
									err = chunk.Err
								}
							}
						}
					} else {
						_, err = execute(ctx, auth, req, opts)
					}
					var status interface {
						StatusCode() int
						RetryAfter() *time.Duration
						IsCredentialScoped() bool
					}
					if !errors.As(err, &status) {
						t.Fatalf("error = %v, want scoped retryable status error", err)
					}
					if status.StatusCode() != http.StatusTooManyRequests {
						t.Fatalf("status = %d, want 429", status.StatusCode())
					}
					if status.IsCredentialScoped() != !modelLevelCooling {
						t.Errorf("credential scope = %t, want %t", status.IsCredentialScoped(), !modelLevelCooling)
					}
					if delay := status.RetryAfter(); delay == nil || *delay != wantDelay {
						t.Errorf("RetryAfter = %v, want %s", delay, wantDelay)
					}
				})
			}
		}
	}
}

package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

type executorPolicyUsageCapture struct {
	records chan executorPolicyUsageRecord
}

type executorPolicyUsageRecord struct {
	decision apikeypolicy.RequestPolicyDecision
	record   coreusage.Record
}

func (c *executorPolicyUsageCapture) HandleUsage(ctx context.Context, record coreusage.Record) {
	if record.Provider != "openai-compatibility" && record.Provider != "claude" {
		return
	}
	decision, _ := apikeypolicy.DecisionFromContext(ctx)
	c.records <- executorPolicyUsageRecord{decision: decision, record: record}
}

func TestOpenAICompatTranslationPanicPublishesOneFailedFrozenPolicyUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"chatcmpl-policy","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
	}))
	defer server.Close()

	responseFormat := sdktranslator.Format("api-key-policy-translation-panic")
	sdktranslator.Register(responseFormat, sdktranslator.FormatOpenAI, nil, sdktranslator.ResponseTransform{
		NonStream: func(context.Context, string, []byte, []byte, []byte, *any) []byte {
			panic("forced terminal translation failure")
		},
	})

	capture := &executorPolicyUsageCapture{records: make(chan executorPolicyUsageRecord, 2)}
	pluginName := "api-key-policy-openai-translation-panic"
	coreusage.RegisterNamedPlugin(pluginName, capture)
	defer coreusage.UnregisterNamedPlugin(pluginName, capture)

	decision := newExecutorPolicyUsageDecision()
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	// Mutating the caller-owned decision after context capture must not affect
	// attribution published at the terminal failure.
	decision.Snapshot.ProfileName = "edited after request started"
	decision.Snapshot.RequestedModel = "edited-alias"

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	_, err := executor.Execute(ctx, openAICompatPolicyUsageAuth(server.URL), cliproxyexecutor.Request{
		Model:   "gpt-5",
		Payload: []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FormatOpenAI,
		ResponseFormat: responseFormat,
	})
	if err == nil || !strings.Contains(err.Error(), "forced terminal translation failure") {
		t.Fatalf("Execute() error = %v, want recovered translation failure", err)
	}

	captured := waitForExecutorPolicyUsage(t, capture.records)
	assertFrozenExecutorPolicyUsage(t, captured, true)
	if captured.record.Fail.StatusCode != http.StatusBadGateway {
		t.Fatalf("failure = %#v, want 502", captured.record.Fail)
	}
	if captured.record.Detail.InputTokens != 11 || captured.record.Detail.OutputTokens != 7 || captured.record.Detail.TotalTokens != 18 {
		t.Fatalf("usage detail = %#v, want 11/7/18", captured.record.Detail)
	}
	assertNoExecutorPolicyUsage(t, capture.records)
}

func TestOpenAICompatStreamCancellationPublishesOneFailedFrozenPolicyUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"chatcmpl-policy","choices":[{"index":0,"delta":{"content":"hello"}}],"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18}}`+"\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	capture := &executorPolicyUsageCapture{records: make(chan executorPolicyUsageRecord, 2)}
	pluginName := "api-key-policy-openai-stream-cancel"
	coreusage.RegisterNamedPlugin(pluginName, capture)
	defer coreusage.UnregisterNamedPlugin(pluginName, capture)

	decision := newExecutorPolicyUsageDecision()
	ctx, cancel := context.WithCancel(apikeypolicy.WithDecision(context.Background(), decision))
	result, err := NewOpenAICompatExecutor("openai-compatibility", &config.Config{}).ExecuteStream(
		ctx,
		openAICompatPolicyUsageAuth(server.URL),
		cliproxyexecutor.Request{
			Model:   "gpt-5",
			Payload: []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
		},
		cliproxyexecutor.Options{Stream: true, SourceFormat: sdktranslator.FormatOpenAI, ResponseFormat: sdktranslator.FormatOpenAI},
	)
	if err != nil {
		cancel()
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	select {
	case chunk, ok := <-result.Chunks:
		if !ok || chunk.Err != nil || len(chunk.Payload) == 0 {
			cancel()
			t.Fatalf("first chunk = %#v, want payload", chunk)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for first stream payload")
	}
	cancel()
	for range result.Chunks {
	}

	captured := waitForExecutorPolicyUsage(t, capture.records)
	assertFrozenExecutorPolicyUsage(t, captured, true)
	if captured.record.Detail.InputTokens != 13 || captured.record.Detail.OutputTokens != 5 || captured.record.Detail.TotalTokens != 18 {
		t.Fatalf("usage detail = %#v, want 13/5/18", captured.record.Detail)
	}
	if !strings.Contains(captured.record.Fail.Body, context.Canceled.Error()) {
		t.Fatalf("failure = %#v, want context cancellation", captured.record.Fail)
	}
	assertNoExecutorPolicyUsage(t, capture.records)
}

func TestClaudeSSEToNonStreamTranslationPanicPublishesOneFailedFrozenPolicyUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"event: message_start\n"+
				`data: {"type":"message_start","message":{"id":"msg-policy","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":17,"output_tokens":0}}}`+"\n\n"+
				"event: message_delta\n"+
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`+"\n\n"+
				"event: message_stop\n"+
				`data: {"type":"message_stop"}`+"\n\n",
		)
	}))
	defer server.Close()

	responseFormat := sdktranslator.Format("api-key-policy-claude-translation-panic")
	sdktranslator.Register(responseFormat, sdktranslator.FormatClaude, nil, sdktranslator.ResponseTransform{
		NonStream: func(context.Context, string, []byte, []byte, []byte, *any) []byte {
			panic("forced Claude terminal translation failure")
		},
	})

	capture := &executorPolicyUsageCapture{records: make(chan executorPolicyUsageRecord, 2)}
	pluginName := "api-key-policy-claude-translation-panic"
	coreusage.RegisterNamedPlugin(pluginName, capture)
	defer coreusage.UnregisterNamedPlugin(pluginName, capture)

	decision := newExecutorPolicyUsageDecision()
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	_, err := NewClaudeExecutor(&config.Config{}).Execute(ctx, &cliproxyauth.Auth{
		ID:       "policy-usage-auth",
		Provider: "claude",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "anthropic-test-key",
		},
	}, cliproxyexecutor.Request{
		Model:   "claude-opus-5",
		Payload: []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat:   responseFormat,
		ResponseFormat: responseFormat,
	})
	if err == nil || !strings.Contains(err.Error(), "forced Claude terminal translation failure") {
		t.Fatalf("Execute() error = %v, want recovered Claude translation failure", err)
	}

	captured := waitForExecutorPolicyUsageProvider(t, capture.records, "claude")
	assertFrozenExecutorPolicyUsage(t, captured, true)
	if captured.record.Detail.InputTokens != 17 || captured.record.Detail.OutputTokens != 3 || captured.record.Detail.TotalTokens != 20 {
		t.Fatalf("usage detail = %#v, want 17/3/20", captured.record.Detail)
	}
	assertNoExecutorPolicyUsage(t, capture.records)
}

func newExecutorPolicyUsageDecision() apikeypolicy.RequestPolicyDecision {
	return apikeypolicy.RequestPolicyDecision{
		Mode: apikeypolicy.ModeProfile,
		Snapshot: &apikeypolicy.RequestPolicySnapshot{
			PolicyID:       "policy-at-request-start",
			ProfileID:      "profile-at-request-start",
			ProfileName:    "Production",
			RequestedModel: "smart",
			EffectiveModel: "gpt-5",
		},
	}
}

func openAICompatPolicyUsageAuth(baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID:       "policy-usage-auth",
		Provider: "openai-compatibility",
		Attributes: map[string]string{
			"base_url": baseURL,
			"api_key":  "test",
		},
	}
}

func waitForExecutorPolicyUsage(t *testing.T, records <-chan executorPolicyUsageRecord) executorPolicyUsageRecord {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for executor policy usage")
		return executorPolicyUsageRecord{}
	}
}

func waitForExecutorPolicyUsageProvider(t *testing.T, records <-chan executorPolicyUsageRecord, provider string) executorPolicyUsageRecord {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case record := <-records:
			if record.record.Provider == provider {
				return record
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s executor policy usage", provider)
			return executorPolicyUsageRecord{}
		}
	}
}

func assertFrozenExecutorPolicyUsage(t *testing.T, captured executorPolicyUsageRecord, wantFailed bool) {
	t.Helper()
	attribution := captured.decision.UsageAttribution()
	if attribution.PolicyMode != apikeypolicy.ModeProfile || attribution.APIKeyPolicyID != "policy-at-request-start" || attribution.ProfileID != "profile-at-request-start" || attribution.ProfileName != "Production" || attribution.RequestedModel != "smart" || attribution.EffectiveModel != "gpt-5" {
		t.Fatalf("attribution = %#v", attribution)
	}
	if captured.record.Failed != wantFailed {
		t.Fatalf("Failed = %v, want %v; record = %#v", captured.record.Failed, wantFailed, captured.record)
	}
}

func assertNoExecutorPolicyUsage(t *testing.T, records <-chan executorPolicyUsageRecord) {
	t.Helper()
	select {
	case duplicate := <-records:
		t.Fatalf("duplicate usage = %#v", duplicate.record)
	case <-time.After(50 * time.Millisecond):
	}
}

type modelAuditWireTransport func(*http.Request) (*http.Response, error)

func (f modelAuditWireTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func TestModelAuditAntigravityUsesFinalWireModel(t *testing.T) {
	ctx := context.Background()
	e := &AntigravityExecutor{cfg: &config.Config{}}
	reporter := helps.NewExecutorUsageReporter(ctx, e, "gemini-3-pro", nil)
	payload := []byte(`{"model":"intermediate-model","request":{"contents":[]}}`)
	reporter.SetTranslatedReasoningEffort(payload, "antigravity")
	auth := &cliproxyauth.Auth{ID: "audit-auth", Metadata: map[string]any{"project_id": "test-project"}}
	req, err := e.buildRequest(ctx, auth, "test-token", "gemini-3-pro", payload, false, "", "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	client := reporter.TrackHTTPClient(&http.Client{Transport: modelAuditWireTransport(func(req *http.Request) (*http.Response, error) {
		actual, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		wire := gjson.GetBytes(actual, "model").String()
		if wire != "gemini-3-pro" || reporter.UpstreamModel() != wire {
			t.Fatalf("wire=%q recorded=%q", wire, reporter.UpstreamModel())
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

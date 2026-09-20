package pluginhost

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

type pluginExecutorUsageRecorder struct {
	records chan coreusage.Record
}

func (r *pluginExecutorUsageRecorder) HandleUsage(_ context.Context, record coreusage.Record) {
	if record.Provider == "plugin-provider" {
		r.records <- record
	}
}

type pluginExecutorPolicyUsage struct {
	decision apikeypolicy.RequestPolicyDecision
	record   coreusage.Record
}

type pluginExecutorPolicyUsageRecorder struct {
	records chan pluginExecutorPolicyUsage
}

func (r *pluginExecutorPolicyUsageRecorder) HandleUsage(ctx context.Context, record coreusage.Record) {
	if record.Provider != "plugin-provider" {
		return
	}
	decision, _ := apikeypolicy.DecisionFromContext(ctx)
	r.records <- pluginExecutorPolicyUsage{decision: decision, record: record}
}

type pluginExecutorStatusError struct {
	status int
}

func (e pluginExecutorStatusError) Error() string   { return "plugin upstream failed" }
func (e pluginExecutorStatusError) StatusCode() int { return e.status }

func TestPluginExecutorPublishesNonStreamUsage(t *testing.T) {
	recorder := &pluginExecutorUsageRecorder{records: make(chan coreusage.Record, 4)}
	coreusage.RegisterNamedPlugin("plugin-executor-usage-test", recorder)
	defer coreusage.UnregisterNamedPlugin("plugin-executor-usage-test", recorder)
	host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "usage-executor"}))
	executeErr := pluginExecutorStatusError{status: http.StatusTooManyRequests}
	executor := &fakeExecutor{
		identifier: "plugin-provider",
		execute: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
			return pluginapi.ExecutorResponse{}, executeErr
		},
		countTokens: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
			return pluginapi.ExecutorResponse{Payload: []byte(`{"total_tokens":1}`)}, nil
		},
	}
	adapter := newCurrentExecutorAdapterForTest(
		host,
		"usage-executor",
		executor,
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
	)
	auth := &coreauth.Auth{ID: "plugin-auth", Index: "plugin-index", Provider: "plugin-provider"}
	ctx := coreusage.NextAttemptContext(context.Background())
	_, err := adapter.Execute(ctx, auth, coreexecutor.Request{Model: "plugin-model"}, coreexecutor.Options{})
	if !errors.Is(err, executeErr) {
		t.Fatalf("Execute() error = %v, want %v", err, executeErr)
	}
	record := waitForPluginExecutorUsage(t, recorder.records)
	if !record.Failed || record.Fail.StatusCode != http.StatusTooManyRequests || record.Fail.Body != executeErr.Error() {
		t.Fatalf("failure = %#v", record.Fail)
	}
	if record.AuthID != auth.ID || record.AuthIndex != auth.Index || record.Model != "plugin-model" {
		t.Fatalf("identity = %#v", record)
	}
	if record.AttemptIndex == nil || *record.AttemptIndex != 0 {
		t.Fatalf("attempt index = %#v", record.AttemptIndex)
	}
	if _, err = adapter.CountTokens(ctx, auth, coreexecutor.Request{Model: "plugin-model"}, coreexecutor.Options{}); err != nil {
		t.Fatal(err)
	}
	assertNoPluginExecutorUsage(t, recorder.records)
}

func TestPluginExecutorPublishesParsedNonStreamTokens(t *testing.T) {
	recorder := &pluginExecutorUsageRecorder{records: make(chan coreusage.Record, 2)}
	coreusage.RegisterNamedPlugin("plugin-executor-token-usage-test", recorder)
	defer coreusage.UnregisterNamedPlugin("plugin-executor-token-usage-test", recorder)
	host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "token-usage-executor"}))
	adapter := newCurrentExecutorAdapterForTest(
		host,
		"token-usage-executor",
		&fakeExecutor{
			identifier: "plugin-provider",
			execute: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
				return pluginapi.ExecutorResponse{Payload: []byte(`{"model":"served-model","usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`)}, nil
			},
		},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
	)
	_, err := adapter.Execute(context.Background(), &coreauth.Auth{ID: "token-auth"}, coreexecutor.Request{
		Model: "plugin-model", Format: sdktranslator.FormatOpenAI,
	}, coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, ResponseFormat: sdktranslator.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	record := waitForPluginExecutorUsage(t, recorder.records)
	if record.UpstreamModel != "plugin-model" || record.ResponseModel != "served-model" || record.ModelMatchStatus != "mismatch" {
		t.Fatalf("adapter lost raw model audit: %+v", record)
	}
	if record.Detail.InputTokens != 5 || record.Detail.OutputTokens != 7 || record.Detail.TotalTokens != 12 {
		t.Fatalf("parsed usage = %#v", record.Detail)
	}
}

func TestPluginExecutorPublishesParsedStreamTokens(t *testing.T) {
	recorder := &pluginExecutorUsageRecorder{records: make(chan coreusage.Record, 2)}
	coreusage.RegisterNamedPlugin("plugin-executor-stream-token-usage-test", recorder)
	defer coreusage.UnregisterNamedPlugin("plugin-executor-stream-token-usage-test", recorder)
	chunks := make(chan pluginapi.ExecutorStreamChunk, 1)
	chunks <- pluginapi.ExecutorStreamChunk{Payload: []byte("event: response.completed\n" + `data: {"model":"served-model","usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`)}
	close(chunks)
	host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "stream-token-usage-executor"}))
	adapter := newCurrentExecutorAdapterForTest(
		host,
		"stream-token-usage-executor",
		&fakeExecutor{
			identifier: "plugin-provider",
			executeStream: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
				return pluginapi.ExecutorStreamResponse{Chunks: chunks}, nil
			},
		},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
	)
	result, err := adapter.ExecuteStream(context.Background(), &coreauth.Auth{ID: "stream-token-auth"}, coreexecutor.Request{
		Model: "plugin-model", Format: sdktranslator.FormatOpenAI,
	}, coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, ResponseFormat: sdktranslator.FormatOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	for range result.Chunks {
	}
	record := waitForPluginExecutorUsage(t, recorder.records)
	if record.UpstreamModel != "plugin-model" || record.ResponseModel != "served-model" || record.ModelMatchStatus != "mismatch" {
		t.Fatalf("adapter lost raw model audit: %+v", record)
	}
	if record.Detail.InputTokens != 4 || record.Detail.OutputTokens != 6 || record.Detail.TotalTokens != 10 {
		t.Fatalf("parsed stream usage = %#v", record.Detail)
	}
}

func TestPluginExecutorTranslationPanicPublishesFailure(t *testing.T) {
	recorder := &pluginExecutorPolicyUsageRecorder{records: make(chan pluginExecutorPolicyUsage, 2)}
	coreusage.RegisterNamedPlugin("plugin-executor-translation-panic-test", recorder)
	defer coreusage.UnregisterNamedPlugin("plugin-executor-translation-panic-test", recorder)
	customOutput := sdktranslator.Format("plugin-panic-output")
	sdktranslator.Register(sdktranslator.FormatOpenAI, customOutput, nil, sdktranslator.ResponseTransform{
		NonStream: func(context.Context, string, []byte, []byte, []byte, *any) []byte {
			panic("forced response translation panic")
		},
	})
	host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "translation-panic-executor"}))
	adapter := newCurrentExecutorAdapterForTest(
		host,
		"translation-panic-executor",
		&fakeExecutor{
			identifier: "plugin-provider",
			execute: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
				return pluginapi.ExecutorResponse{Payload: []byte(`{"ok":true}`)}, nil
			},
		},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{customOutput},
	)
	decision := apikeypolicy.RequestPolicyDecision{Mode: apikeypolicy.ModeProfile, Snapshot: &apikeypolicy.RequestPolicySnapshot{
		PolicyID: "translation-policy", ProfileID: "translation-profile", ProfileName: "Translation",
		RequestedModel: "translation-alias", EffectiveModel: "panic-model",
	}}
	ctx := apikeypolicy.WithDecision(context.Background(), decision)
	_, err := adapter.Execute(ctx, &coreauth.Auth{ID: "panic-auth"}, coreexecutor.Request{
		Model: "panic-model", Format: sdktranslator.FormatOpenAI, Payload: []byte(`{"model":"panic-model"}`),
	}, coreexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI, ResponseFormat: sdktranslator.FormatOpenAI})
	if err == nil {
		t.Fatal("Execute() error = nil, want recovered translation panic")
	}
	captured := waitForPluginExecutorPolicyUsage(t, recorder.records)
	if !captured.record.Failed || captured.record.Fail.Body == "" {
		t.Fatalf("translation panic failure = %#v", captured.record.Fail)
	}
	attribution := captured.decision.UsageAttribution()
	if attribution.APIKeyPolicyID != "translation-policy" || attribution.ProfileID != "translation-profile" || attribution.RequestedModel != "translation-alias" || attribution.EffectiveModel != "panic-model" {
		t.Fatalf("translation panic attribution=%#v", attribution)
	}
}

func TestPluginExecutorStreamPublishesTerminalOutcome(t *testing.T) {
	for _, test := range []struct {
		name       string
		terminal   error
		wantFailed bool
	}{
		{name: "normal close"},
		{name: "terminal error", terminal: pluginExecutorStatusError{status: http.StatusServiceUnavailable}, wantFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := &pluginExecutorUsageRecorder{records: make(chan coreusage.Record, 2)}
			coreusage.RegisterNamedPlugin("plugin-executor-stream-usage-test", recorder)
			defer coreusage.UnregisterNamedPlugin("plugin-executor-stream-usage-test", recorder)
			chunks := make(chan pluginapi.ExecutorStreamChunk, 2)
			chunks <- pluginapi.ExecutorStreamChunk{Payload: []byte("data")}
			if test.terminal != nil {
				chunks <- pluginapi.ExecutorStreamChunk{Err: test.terminal}
			}
			close(chunks)
			host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "stream-usage-executor"}))
			adapter := newCurrentExecutorAdapterForTest(
				host,
				"stream-usage-executor",
				&fakeExecutor{
					identifier: "plugin-provider",
					executeStream: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
						return pluginapi.ExecutorStreamResponse{Headers: http.Header{"Retry-After": []string{"60"}}, Chunks: chunks}, nil
					},
				},
				[]sdktranslator.Format{sdktranslator.FormatOpenAI},
				[]sdktranslator.Format{sdktranslator.FormatOpenAI},
			)
			ctx := context.Background()
			result, err := adapter.ExecuteStream(ctx, &coreauth.Auth{ID: "stream-auth", Index: "stream-index"}, coreexecutor.Request{Model: "stream-model"}, coreexecutor.Options{})
			if err != nil {
				t.Fatal(err)
			}
			for range result.Chunks {
			}
			record := waitForPluginExecutorUsage(t, recorder.records)
			if record.Failed != test.wantFailed {
				t.Fatalf("Failed = %v, want %v", record.Failed, test.wantFailed)
			}
			if record.ResponseHeaders.Get("Retry-After") != "60" {
				t.Fatalf("response headers = %#v", record.ResponseHeaders)
			}
			if test.terminal != nil && record.Fail.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("failure = %#v", record.Fail)
			}
			assertNoPluginExecutorUsage(t, recorder.records)
		})
	}
}

func TestPluginExecutorEmptyStreamPublishesFailure(t *testing.T) {
	recorder := &pluginExecutorUsageRecorder{records: make(chan coreusage.Record, 2)}
	coreusage.RegisterNamedPlugin("plugin-executor-empty-stream-test", recorder)
	defer coreusage.UnregisterNamedPlugin("plugin-executor-empty-stream-test", recorder)
	chunks := make(chan pluginapi.ExecutorStreamChunk)
	close(chunks)
	host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "empty-stream-executor"}))
	adapter := newCurrentExecutorAdapterForTest(
		host,
		"empty-stream-executor",
		&fakeExecutor{
			identifier: "plugin-provider",
			executeStream: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
				return pluginapi.ExecutorStreamResponse{Chunks: chunks}, nil
			},
		},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
		[]sdktranslator.Format{sdktranslator.FormatOpenAI},
	)
	result, err := adapter.ExecuteStream(context.Background(), &coreauth.Auth{ID: "empty-stream-auth"}, coreexecutor.Request{Model: "empty-stream-model"}, coreexecutor.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for range result.Chunks {
	}
	record := waitForPluginExecutorUsage(t, recorder.records)
	if !record.Failed || record.Fail.StatusCode != http.StatusBadGateway {
		t.Fatalf("empty stream failure = %#v", record.Fail)
	}
}

func TestPluginExecutorUsageKeepsFrozenAPIKeyPolicyAttribution(t *testing.T) {
	decision := apikeypolicy.RequestPolicyDecision{Mode: apikeypolicy.ModeProfile, Snapshot: &apikeypolicy.RequestPolicySnapshot{
		PolicyID: "plugin-policy", ProfileID: "plugin-profile", ProfileName: "Plugin restricted",
		RequestedModel: "plugin-alias", EffectiveModel: "plugin-model",
	}}
	assertAttribution := func(t *testing.T, captured pluginExecutorPolicyUsage, wantFailed bool) {
		t.Helper()
		attribution := captured.decision.UsageAttribution()
		if attribution.PolicyMode != apikeypolicy.ModeProfile || attribution.APIKeyPolicyID != "plugin-policy" || attribution.ProfileID != "plugin-profile" || attribution.ProfileName != "Plugin restricted" || attribution.RequestedModel != "plugin-alias" || attribution.EffectiveModel != "plugin-model" {
			t.Fatalf("attribution=%#v", attribution)
		}
		if captured.record.Failed != wantFailed {
			t.Fatalf("failed=%v, want %v; record=%#v", captured.record.Failed, wantFailed, captured.record)
		}
	}

	for _, test := range []struct {
		name       string
		stream     bool
		fail       bool
		terminal   error
		cancel     bool
		wantFailed bool
	}{
		{name: "execute success"},
		{name: "execute failure", fail: true, wantFailed: true},
		{name: "stream success", stream: true},
		{name: "stream terminal failure", stream: true, terminal: pluginExecutorStatusError{status: http.StatusBadGateway}, wantFailed: true},
		{name: "stream cancellation", stream: true, cancel: true, wantFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := &pluginExecutorPolicyUsageRecorder{records: make(chan pluginExecutorPolicyUsage, 2)}
			pluginName := "plugin-executor-policy-usage-" + strings.ReplaceAll(test.name, " ", "-")
			coreusage.RegisterNamedPlugin(pluginName, recorder)
			defer coreusage.UnregisterNamedPlugin(pluginName, recorder)
			host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "policy-usage-executor"}))
			executeErr := pluginExecutorStatusError{status: http.StatusBadGateway}
			executor := &fakeExecutor{identifier: "plugin-provider"}
			executor.execute = func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorResponse, error) {
				if test.fail {
					return pluginapi.ExecutorResponse{}, executeErr
				}
				return pluginapi.ExecutorResponse{Payload: []byte(`{"ok":true}`)}, nil
			}
			executor.executeStream = func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
				chunks := make(chan pluginapi.ExecutorStreamChunk, 2)
				if test.cancel {
					return pluginapi.ExecutorStreamResponse{Chunks: chunks}, nil
				}
				chunks <- pluginapi.ExecutorStreamChunk{Payload: []byte("data")}
				if test.terminal != nil {
					chunks <- pluginapi.ExecutorStreamChunk{Err: test.terminal}
				}
				close(chunks)
				return pluginapi.ExecutorStreamResponse{Chunks: chunks}, nil
			}
			adapter := newCurrentExecutorAdapterForTest(host, "policy-usage-executor", executor, []sdktranslator.Format{sdktranslator.FormatOpenAI}, []sdktranslator.Format{sdktranslator.FormatOpenAI})
			ctx := apikeypolicy.WithDecision(context.Background(), decision)
			var cancel context.CancelFunc
			if test.cancel {
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
			}
			if test.stream {
				result, err := adapter.ExecuteStream(ctx, &coreauth.Auth{ID: "plugin-policy-auth"}, coreexecutor.Request{Model: "plugin-model"}, coreexecutor.Options{})
				if err != nil {
					t.Fatal(err)
				}
				if test.cancel {
					cancel()
				}
				for range result.Chunks {
				}
			} else {
				_, err := adapter.Execute(ctx, &coreauth.Auth{ID: "plugin-policy-auth"}, coreexecutor.Request{Model: "plugin-model"}, coreexecutor.Options{})
				if test.fail && !errors.Is(err, executeErr) {
					t.Fatalf("Execute() error=%v", err)
				}
				if !test.fail && err != nil {
					t.Fatal(err)
				}
			}
			select {
			case captured := <-recorder.records:
				assertAttribution(t, captured, test.wantFailed)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for policy-attributed plugin usage")
			}
			select {
			case duplicate := <-recorder.records:
				t.Fatalf("duplicate usage=%#v", duplicate.record)
			case <-time.After(25 * time.Millisecond):
			}
		})
	}
}

func waitForPluginExecutorPolicyUsage(t *testing.T, records <-chan pluginExecutorPolicyUsage) pluginExecutorPolicyUsage {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin executor policy usage")
		return pluginExecutorPolicyUsage{}
	}
}

func waitForPluginExecutorUsage(t *testing.T, records <-chan coreusage.Record) coreusage.Record {
	t.Helper()
	select {
	case record := <-records:
		return record
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin executor usage")
		return coreusage.Record{}
	}
}

func assertNoPluginExecutorUsage(t *testing.T, records <-chan coreusage.Record) {
	t.Helper()
	select {
	case record := <-records:
		t.Fatalf("unexpected usage record: %#v", record)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestPluginRawResponseModelObservedBeforeTranslation(t *testing.T) {
	for _, tc := range []struct{ provider, format, payload, want string }{
		{"gemini", "openai-response", `{"model":"served-model","usage":{"total_tokens":1}}`, "served-model"},
		{"gemini", "openai-response", `{"type":"response.completed","response":{"model":"served-model"}}`, "served-model"},
		{"codex", "claude", `{"type":"message","model":"claude-served"}`, "claude-served"},
		{"claude", "gemini", `{"modelVersion":"gemini-served"}`, "gemini-served"},
		{"claude", "antigravity", `{"response":{"modelVersion":"gemini-served"}}`, "gemini-served"},
	} {
		reporter := helps.NewUsageReporter(context.Background(), tc.provider, "requested", nil)
		reporter.SetResponseModelFormat(tc.format)
		observePluginResponseModel(reporter, []byte(tc.payload))
		if reporter.ResponseModel() != tc.want {
			t.Fatalf("%s/%s model=%q, want %q", tc.provider, tc.format, reporter.ResponseModel(), tc.want)
		}
	}
}

type modelAuditPluginRecorder struct {
	records  chan coreusage.Record
	provider string
}

func (r *modelAuditPluginRecorder) HandleUsage(_ context.Context, record coreusage.Record) {
	if record.Provider == r.provider {
		r.records <- record
	}
}

func TestPluginModelAuditUsesNegotiatedProtocolAndCompleteSSEEvents(t *testing.T) {
	for _, tc := range []struct {
		name, provider string
		format         sdktranslator.Format
		chunks         []string
		want           string
		terminal       error
	}{
		{name: "fragmented multiline terminal", provider: "gemini", format: sdktranslator.FormatOpenAIResponse, chunks: []string{
			"data: {\"type\":\"response.created\",\"response\":{\"model\":\"early\"}}\r\n\r\n",
			"event: response.completed\r\ndata: {\"type\":\"response.completed\",\r\ndata: ",
			"\"response\":{\"model\":\"served-model\"}}\r\n\r\n",
		}, want: "served-model"},
		{name: "flush terminal at EOF", provider: "gemini", format: sdktranslator.FormatOpenAIResponse, chunks: []string{
			"data: {\"type\":\"response.completed\",\ndata: \"response\":{\"model\":\"served-model\"}}",
		}, want: "served-model"},
		{name: "flush on failure", provider: "gemini", format: sdktranslator.FormatOpenAIResponse, chunks: []string{
			"data: {\"type\":\"response.completed\",\ndata: \"response\":{\"model\":\"served-model\"}}",
		}, want: "served-model", terminal: pluginExecutorStatusError{status: 502}},
		{name: "raw JSON chunks", provider: "codex", format: sdktranslator.FormatClaude, chunks: []string{
			`{"type":"message_start","message":{"model":"served-model","usage":{"input_tokens":1}}}`,
			`{"type":"message_delta","usage":{"output_tokens":2}}`, `{"type":"message_stop"}`,
		}, want: "served-model"},
		{name: "partial Claude message must not finalize early", provider: "codex", format: sdktranslator.FormatClaude, chunks: []string{
			"data: {\"type\":\"message\",\ndata: \"model\":\"served-model\",\"usage\":{\"input_tokens\":1}}\n\n",
		}, want: "served-model"},
		{name: "oversized frame recovery", provider: "gemini", format: sdktranslator.FormatOpenAIResponse, chunks: []string{
			"data: {\"ignored\":\"" + strings.Repeat("x", 70*1024) + "\"}\n\n",
			"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"served-model\"}}\n\n",
		}, want: "served-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &modelAuditPluginRecorder{provider: tc.provider, records: make(chan coreusage.Record, 4)}
			coreusage.RegisterNamedPlugin("model-audit-stream", recorder)
			defer coreusage.UnregisterNamedPlugin("model-audit-stream", recorder)
			chunks := make(chan pluginapi.ExecutorStreamChunk, len(tc.chunks)+1)
			for _, payload := range tc.chunks {
				chunks <- pluginapi.ExecutorStreamChunk{Payload: []byte(payload)}
			}
			if tc.terminal != nil {
				chunks <- pluginapi.ExecutorStreamChunk{Err: tc.terminal}
			}
			close(chunks)
			host := newHostWithRecords(normalizeTestCapabilityRecord(capabilityRecord{id: "model-audit-stream"}))
			adapter := newCurrentExecutorAdapterForTest(host, "model-audit-stream", &fakeExecutor{
				identifier: tc.provider,
				executeStream: func(context.Context, pluginapi.ExecutorRequest) (pluginapi.ExecutorStreamResponse, error) {
					return pluginapi.ExecutorStreamResponse{Chunks: chunks}, nil
				},
			}, []sdktranslator.Format{tc.format}, []sdktranslator.Format{tc.format})
			adapter.provider = tc.provider
			result, err := adapter.ExecuteStream(context.Background(), &coreauth.Auth{ID: "audit-auth"}, coreexecutor.Request{Model: "requested", Format: tc.format, Payload: []byte(`{"model":"requested"}`)}, coreexecutor.Options{SourceFormat: tc.format, ResponseFormat: tc.format})
			if err != nil {
				t.Fatal(err)
			}
			for range result.Chunks {
			}
			got := waitForPluginExecutorUsage(t, recorder.records)
			if got.Provider != tc.provider || got.Model != "requested" || got.UpstreamModel != "requested" || got.ResponseModel != tc.want || got.ModelMatchStatus != "mismatch" || got.Failed != (tc.terminal != nil) {
				t.Fatalf("model audit=%+v", got)
			}
			assertNoPluginExecutorUsage(t, recorder.records)
		})
	}
}

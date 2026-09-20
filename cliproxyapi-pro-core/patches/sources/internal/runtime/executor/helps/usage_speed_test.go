package helps

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

type captureUsageSpeedPlugin struct {
	records chan usage.Record
}

func (p *captureUsageSpeedPlugin) HandleUsage(_ context.Context, record usage.Record) {
	select {
	case p.records <- record:
	default:
	}
}

func TestParseClaudeUsageIncludesResponseSpeed(t *testing.T) {
	detail := ParseClaudeUsage([]byte(`{"usage":{"input_tokens":10,"output_tokens":2,"speed":"fast"}}`))
	if detail.ResponseSpeed != "fast" {
		t.Fatalf("ResponseSpeed = %q, want fast", detail.ResponseSpeed)
	}
}

func TestParseClaudeStreamUsageIncludesResponseSpeed(t *testing.T) {
	detail, ok := ParseClaudeStreamUsage([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"speed":"standard"}}}`))
	if !ok || detail.ResponseSpeed != "standard" {
		t.Fatalf("ParseClaudeStreamUsage() = (%+v, %v), want standard speed", detail, ok)
	}
}

func TestUsageReporterBuildRecordIncludesSpeed(t *testing.T) {
	ctx := usage.WithSpeed(context.Background(), "fast")
	reporter := NewUsageReporter(ctx, "claude", "claude-opus-test", nil)
	record := reporter.buildRecord(usage.Detail{TotalTokens: 3, ResponseSpeed: "standard"}, false)
	if record.Speed != "fast" || record.ResponseSpeed != "standard" {
		t.Fatalf("record speeds = %q/%q, want fast/standard", record.Speed, record.ResponseSpeed)
	}
}

func TestStreamUsageBufferPreservesResponseSpeedFromMessageStart(t *testing.T) {
	var buffer StreamUsageBuffer
	start, startOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"speed":"fast"}}}`))
	final, finalOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_delta","usage":{"output_tokens":2}}`))
	buffer.ObserveClaude(start, startOK)
	buffer.ObserveClaude(final, finalOK)
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 10 || detail.OutputTokens != 2 || detail.TotalTokens != 12 || detail.ResponseSpeed != "fast" {
		t.Fatalf("buffer detail = (%+v, %v), want merged input/output tokens with preserved fast speed", detail, ok)
	}
}

func TestStreamUsageBufferMergesClaudeUsageWithoutResponseSpeed(t *testing.T) {
	var buffer StreamUsageBuffer
	start, startOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`))
	final, finalOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_delta","usage":{"output_tokens":2}}`))
	buffer.ObserveClaude(start, startOK)
	buffer.ObserveClaude(final, finalOK)
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 10 || detail.OutputTokens != 2 || detail.CacheReadTokens != 3 ||
		detail.CacheCreationTokens != 4 || detail.CachedTokens != 3 || detail.TotalTokens != 19 {
		t.Fatalf("buffer detail = (%+v, %v), want merged input/cache/output tokens without response speed", detail, ok)
	}
}

func TestStreamUsageBufferMergesClaudeCacheCreationWithoutCacheRead(t *testing.T) {
	var buffer StreamUsageBuffer
	start, startOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":4}}}`))
	final, finalOK := ParseClaudeStreamUsage([]byte(`data: {"type":"message_delta","usage":{"output_tokens":2}}`))
	buffer.ObserveClaude(start, startOK)
	buffer.ObserveClaude(final, finalOK)
	detail, ok := buffer.Detail()
	if !ok || detail.CacheCreationTokens != 4 || detail.CachedTokens != 4 || detail.TotalTokens != 16 {
		t.Fatalf("buffer detail = (%+v, %v), want preserved cache creation tokens", detail, ok)
	}
}

func TestStreamUsageBufferGenericObserveKeepsLatestUsageAuthoritative(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.Observe(usage.Detail{InputTokens: 10, TotalTokens: 10}, true)
	buffer.Observe(usage.Detail{OutputTokens: 2, TotalTokens: 2}, true)
	detail, ok := buffer.Detail()
	if !ok || detail.InputTokens != 0 || detail.OutputTokens != 2 || detail.TotalTokens != 2 {
		t.Fatalf("buffer detail = (%+v, %v), want final generic usage to remain authoritative", detail, ok)
	}
}

func TestStreamUsageBufferPublishFailurePreservesObservedUsage(t *testing.T) {
	plugin := &captureUsageSpeedPlugin{records: make(chan usage.Record, 4)}
	pluginName := "usage-speed-stream-failure"
	usage.RegisterNamedPlugin(pluginName, plugin)
	defer usage.UnregisterNamedPlugin(pluginName, plugin)

	ctx := context.Background()
	reporter := NewUsageReporter(ctx, "claude", "claude-stream-failure-test", nil)
	var buffer StreamUsageBuffer
	if buffer.PublishFailure(ctx, reporter, errors.New("before usage")) {
		t.Fatal("PublishFailure() = true before usage was observed")
	}
	buffer.ObserveClaude(usage.Detail{
		InputTokens:     10,
		CacheReadTokens: 3,
		TotalTokens:     13,
		ResponseSpeed:   "fast",
	}, true)
	if !buffer.PublishFailure(ctx, reporter, errors.New("stream canceled")) {
		t.Fatal("PublishFailure() = false after usage was observed")
	}

	deadline := time.After(2 * time.Second)
	var record usage.Record
	for record.Model != "claude-stream-failure-test" {
		select {
		case record = <-plugin.records:
		case <-deadline:
			t.Fatal("timed out waiting for failed usage record")
		}
	}
	if !record.Failed || record.Fail.Body != "stream canceled" {
		t.Fatalf("record failure = (%v, %q), want failed stream cancellation", record.Failed, record.Fail.Body)
	}
	if record.Detail.InputTokens != 10 || record.Detail.CacheReadTokens != 3 || record.ResponseSpeed != "fast" || record.Detail.ResponseSpeed != "fast" {
		t.Fatalf("record detail = %+v, response speed = %q; want preserved input/cache usage and fast speed", record.Detail, record.ResponseSpeed)
	}
	select {
	case duplicate := <-plugin.records:
		if duplicate.Model == "claude-stream-failure-test" {
			t.Fatalf("received duplicate usage record: %+v", duplicate)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUsageReporterModelAuditUsesOutboundPayloadAndKeepsAccountingModel(t *testing.T) {
	for _, tc := range []struct{ response, status string }{
		{"gpt-6-astra", "match"}, {"gpt-6-astra-2026-09-01", "variant"},
		{"gpt-5.6-luna", "mismatch"}, {"", "unknown"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			reporter := NewUsageReporter(context.Background(), "codex", "accounting-model", nil)
			reporter.ObserveUpstreamRequestModel([]byte(`{"model":"gpt-6-astra","reasoning":{"effort":"high"}}`))
			reporter.SetResponseModel(tc.response)
			record := reporter.buildRecord(usage.Detail{TotalTokens: 3}, false)
			if record.Model != "accounting-model" || record.UpstreamModel != "gpt-6-astra" || record.ResponseModel != tc.response || record.ModelMatchStatus != tc.status {
				t.Fatalf("model audit = %+v", record)
			}
			side := reporter.buildRecordForModel("image-tool", usage.Detail{}, false, usage.Failure{})
			if side.UpstreamModel != "" || side.ResponseModel != "" || side.ModelMatchStatus != "unknown" {
				t.Fatalf("side model inherited audit: %+v", side)
			}
		})
	}
}

func TestUsageReporterModelAuditTerminalResponseAndRetryIsolation(t *testing.T) {
	first := NewUsageReporter(context.Background(), "codex", "gpt-6-astra", nil)
	first.ObserveUpstreamRequestModel([]byte(`{"model":"gpt-6-astra"}`))
	first.ObserveResponseModel([]byte(`data: {"type":"response.created","response":{"model":"gpt-6-astra"}}`))
	first.ObserveResponseModel([]byte(`data: {"type":"response.completed","response":{"model":"gpt-5.6-luna"}}`))
	first.ObserveResponseModel([]byte(`data: {"type":"response.created","response":{"model":"gpt-6-astra"}}`))
	if got := first.buildRecord(usage.Detail{}, false); got.ResponseModel != "gpt-5.6-luna" || got.ModelMatchStatus != "mismatch" {
		t.Fatalf("terminal audit = %+v", got)
	}
	retry := NewUsageReporter(context.Background(), "codex", "gpt-6-astra", nil)
	if got := retry.buildRecord(usage.Detail{}, true); got.ResponseModel != "" || got.ModelMatchStatus != "unknown" {
		t.Fatalf("retry inherited response: %+v", got)
	}
}

func TestModelAuditIgnoresIntermediateTranslation(t *testing.T) {
	r := NewUsageReporter(context.Background(), "antigravity", "client-alias", nil)
	r.SetTranslatedReasoningEffort([]byte(`{"model":"intermediate-model"}`), "antigravity")
	r.SetResponseModel("served-model")
	if got := r.buildRecord(usage.Detail{}, false); got.UpstreamModel != "" || got.ModelMatchStatus != "unknown" {
		t.Fatalf("inferred outbound model: %+v", got)
	}
}

type modelAuditTransport func(*http.Request) (*http.Response, error)

func (f modelAuditTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestModelAuditReadsFinalRequestWithoutConsumingBody(t *testing.T) {
	for _, tc := range []struct{ name, url, body, want string }{
		{"JSON body", "https://example.test/v1/responses", `{"model":"final-model"}`, "final-model"},
		{"URL takes precedence", "https://example.test/v1beta/models/gemini-real:generateContent", `{"model":"ignored-model"}`, "gemini-real"},
		{"Vertex URL", "https://example.test/v1/projects/p/locations/l/publishers/google/models/gemini-real:streamGenerateContent", `{"model":"ignored-model"}`, "gemini-real"},
		{"large body", "https://example.test/v1/responses", `{"model":"final-model","input":"` + strings.Repeat("x", 128*1024) + `"}`, "final-model"},
		{"model beyond bound", "https://example.test/v1/responses", `{"input":"` + strings.Repeat("x", 128*1024) + `","model":"late-model"}`, ""},
		{"partial string", "https://example.test/v1/responses", `{"model":"` + strings.Repeat("x", 128*1024) + `"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewUsageReporter(context.Background(), "test-provider", "accounting-model", nil)
			r.SetTranslatedReasoningEffort([]byte(`{"model":"intermediate"}`), "openai")
			req, err := http.NewRequest(http.MethodPost, tc.url, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			client := r.TrackHTTPClient(&http.Client{Transport: modelAuditTransport(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatal(err)
				}
				if string(body) != tc.body {
					t.Fatal("audit consumed or modified request body")
				}
				if r.UpstreamModel() != tc.want {
					t.Fatalf("model at transport = %q, want %q", r.UpstreamModel(), tc.want)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
			})})
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		})
	}
}

func TestModelAuditNonReplayableBodyIsNotConsumed(t *testing.T) {
	r := NewUsageReporter(context.Background(), "test", "alias", nil)
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/responses", io.NopCloser(strings.NewReader(`{"model":"actual"}`)))
	r.ObserveUpstreamHTTPRequest(req)
	remaining, _ := io.ReadAll(req.Body)
	if string(remaining) != `{"model":"actual"}` || r.UpstreamModel() != "" {
		t.Fatal("unreplayable body was consumed or inferred")
	}
}

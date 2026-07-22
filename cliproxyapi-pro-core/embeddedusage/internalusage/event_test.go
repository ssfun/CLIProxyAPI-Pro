package internalusage

import (
	"strings"
	"testing"
)

func TestNormalizeRawExtractsDiagnosticsAndRedactsSecrets(t *testing.T) {
	event, err := NormalizeRaw([]byte(`{
		"timestamp":"2026-06-13T00:00:00Z",
		"request_id":"req-1",
		"endpoint":"POST /v1/chat/completions",
		"provider":"antigravity",
		"executor_type":"AntigravityExecutor",
		"model":"gpt-test",
		"alias":"client-gpt",
		"api_key":"sk-secret",
		"tokens":{"input_tokens":10,"output_tokens":20,"cache_read_tokens":7,"cache_creation_tokens":3},
		"latency_ms":1234,
		"ttft_ms":321,
		"stream":true,
		"reasoning_effort":"high",
		"service_tier":"priority",
		"failed":true,
		"fail":{"status_code":429,"body":"{\"error\":{\"message\":\"too many requests\"}}"},
		"response_headers":{"set_cookie":"secret-cookie","X-Upstream-Request-Id":["upstream-req-1"],"Retry-After":["30"]}
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw() error = %v", err)
	}
	if event.TTFTMS == nil || *event.TTFTMS != 321 || event.StatusCode == nil || *event.StatusCode != 429 {
		t.Fatalf("diagnostics = ttft:%v status:%v, want 321/429", event.TTFTMS, event.StatusCode)
	}
	if event.ErrorCode != "" || event.ErrorMessage != "too many requests" {
		t.Fatalf("error fields = %q/%q, want empty/too many requests", event.ErrorCode, event.ErrorMessage)
	}
	if !event.Stream || event.ReasoningEffort != "high" || event.ServiceTier != "priority" {
		t.Fatalf("request fields = stream:%t reasoning:%q tier:%q, want true/high/priority", event.Stream, event.ReasoningEffort, event.ServiceTier)
	}
	if event.Provider != "antigravity" || event.ExecutorType != "AntigravityExecutor" || event.Alias != "client-gpt" {
		t.Fatalf("provider fields = %q/%q/%q, want antigravity/AntigravityExecutor/client-gpt", event.Provider, event.ExecutorType, event.Alias)
	}
	if event.UpstreamRequestID != "upstream-req-1" || event.RetryAfter != "30" {
		t.Fatalf("upstream diagnostics = %q/%q, want upstream-req-1/30", event.UpstreamRequestID, event.RetryAfter)
	}
	if event.CacheTokens != 10 || event.TotalTokens != 40 {
		t.Fatalf("cache/total tokens = %d/%d, want 10/40", event.CacheTokens, event.TotalTokens)
	}
	if event.CacheReadTokens != 7 || event.CacheWriteTokens != 3 {
		t.Fatalf("cache read/write tokens = %d/%d, want 7/3", event.CacheReadTokens, event.CacheWriteTokens)
	}
	if strings.Contains(event.RawJSON, "secret-cookie") || strings.Contains(event.RawJSON, "sk-secret") {
		t.Fatalf("RawJSON was not redacted: %s", event.RawJSON)
	}
}

func TestNormalizeRawIgnoresLegacyAliases(t *testing.T) {
	event, err := NormalizeRaw([]byte(`{
		"timestamp":"2026-06-13T00:00:00Z",
		"requestId":"legacy-request",
		"api":"POST /legacy",
		"modelName":"legacy-model",
		"apiKey":"sk-secret",
		"latencyMs":1234,
		"statusCode":429,
		"failed":true,
		"tokens":{"inputTokens":10,"outputTokens":20,"cacheTokens":5}
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw() error = %v", err)
	}
	if event.RequestID != "" || event.Endpoint != "-" || event.Model != "-" {
		t.Fatalf("legacy aliases were accepted: request_id=%q endpoint=%q model=%q", event.RequestID, event.Endpoint, event.Model)
	}
	if event.LatencyMS != nil || event.StatusCode != nil || event.TotalTokens != 0 {
		t.Fatalf("legacy diagnostics were accepted: latency=%v status=%v total=%d", event.LatencyMS, event.StatusCode, event.TotalTokens)
	}
}

func TestNormalizeRawPreservesExportedHashes(t *testing.T) {
	event, err := NormalizeRaw([]byte(`{
		"event_hash":"event-hash-exported",
		"timestamp_ms":1781308800000,
		"timestamp":"2026-06-13T00:00:00Z",
		"model":"gpt-test",
		"source":"m:abcd...wxyz",
		"source_hash":"source-hash-exported",
		"api_key_hash":"api-key-hash-exported",
		"input_tokens":10,
		"output_tokens":20,
		"total_tokens":30,
		"cache_read_tokens":4,
		"cache_write_tokens":2,
		"estimated_cost":0.125,
		"price_rule_id":9,
		"cost_breakdown_json":"{\"source\":\"models.dev\"}",
		"failed":false
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw() error = %v", err)
	}
	if event.EventHash != "event-hash-exported" {
		t.Fatalf("event hash = %q, want exported hash", event.EventHash)
	}
	if event.SourceHash != "source-hash-exported" {
		t.Fatalf("source hash = %q, want exported hash", event.SourceHash)
	}
	if event.APIKeyHash != "api-key-hash-exported" {
		t.Fatalf("api key hash = %q, want exported hash", event.APIKeyHash)
	}
	if event.EstimatedCost == nil || *event.EstimatedCost != 0.125 || event.PriceRuleID != 9 || event.CacheReadTokens != 4 || event.CacheWriteTokens != 2 {
		t.Fatalf("exported pricing fields were not preserved: %+v", event)
	}
}

func TestNormalizeRawEventHashSeparatesAPIKeys(t *testing.T) {
	first, err := NormalizeRaw([]byte(`{
		"timestamp":"2026-06-13T00:00:00Z",
		"request_id":"req-shared",
		"endpoint":"POST /v1/chat/completions",
		"model":"gpt-test",
		"api_key":"sk-first",
		"tokens":{"input_tokens":10,"output_tokens":20}
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw(first) error = %v", err)
	}
	second, err := NormalizeRaw([]byte(`{
		"timestamp":"2026-06-13T00:00:00Z",
		"request_id":"req-shared",
		"endpoint":"POST /v1/chat/completions",
		"model":"gpt-test",
		"api_key":"sk-second",
		"tokens":{"input_tokens":10,"output_tokens":20}
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw(second) error = %v", err)
	}
	if first.EventHash == second.EventHash {
		t.Fatalf("event hashes collide across API keys: %q", first.EventHash)
	}
}

func TestBuildPayloadIncludesUpstreamUsageMetadata(t *testing.T) {
	payload := BuildPayload([]Event{{
		Timestamp:         "2026-06-13T00:00:00Z",
		Provider:          "antigravity",
		ExecutorType:      "AntigravityExecutor",
		Model:             "gemini-claude-opus-4-5-thinking",
		Alias:             "claude-opus-4-5",
		Endpoint:          "POST /v1/chat/completions",
		AuthType:          "oauth",
		UpstreamRequestID: "upstream-req-1",
		RetryAfter:        "30",
		Stream:            true,
		Failed:            false,
	}})

	details := payload.APIs["POST /v1/chat/completions"].Models["gemini-claude-opus-4-5-thinking"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	detail := details[0]
	if detail.Provider != "antigravity" || detail.ExecutorType != "AntigravityExecutor" || detail.Alias != "claude-opus-4-5" || detail.AuthType != "oauth" || detail.UpstreamRequestID != "upstream-req-1" || detail.RetryAfter != "30" || !detail.Stream {
		t.Fatalf("detail metadata = provider:%q executor:%q alias:%q auth:%q upstream:%q retry:%q stream:%t", detail.Provider, detail.ExecutorType, detail.Alias, detail.AuthType, detail.UpstreamRequestID, detail.RetryAfter, detail.Stream)
	}
}

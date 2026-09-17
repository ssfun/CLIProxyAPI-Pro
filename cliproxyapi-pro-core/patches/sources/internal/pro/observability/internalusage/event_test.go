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
		"client_ip":"192.0.2.10",
		"x_forwarded_for":"203.0.113.5, 198.51.100.8",
		"user_agent":"test-client/1.0",
		"tokens":{"input_tokens":10,"output_tokens":20,"cache_read_tokens":7,"cache_creation_tokens":3},
		"latency_ms":1234,
		"ttft_ms":321,
		"attempt_index":1,
		"stream":true,
		"reasoning_effort":"high",
		"service_tier":"fast",
		"response_service_tier":"priority",
		"speed":"fast",
		"response_speed":"standard",
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
	if !event.Stream || event.ReasoningEffort != "high" || event.ServiceTier != "fast" || event.EffectiveServiceTier != "priority" || event.Speed != "fast" || event.EffectiveSpeed != "standard" {
		t.Fatalf("request fields = stream:%t reasoning:%q tier:%q/%q speed:%q/%q", event.Stream, event.ReasoningEffort, event.ServiceTier, event.EffectiveServiceTier, event.Speed, event.EffectiveSpeed)
	}
	if event.AttemptIndex == nil || *event.AttemptIndex != 1 {
		t.Fatalf("attempt index = %v, want 1", event.AttemptIndex)
	}
	if event.Provider != "antigravity" || event.ExecutorType != "AntigravityExecutor" || event.Alias != "client-gpt" {
		t.Fatalf("provider fields = %q/%q/%q, want antigravity/AntigravityExecutor/client-gpt", event.Provider, event.ExecutorType, event.Alias)
	}
	if event.UpstreamRequestID != "upstream-req-1" || event.RetryAfter != "30" {
		t.Fatalf("upstream diagnostics = %q/%q, want upstream-req-1/30", event.UpstreamRequestID, event.RetryAfter)
	}
	if event.ClientIP != "192.0.2.10" || event.XForwardedFor != "203.0.113.5, 198.51.100.8" || event.UserAgent != "test-client/1.0" {
		t.Fatalf("client metadata = %q/%q/%q", event.ClientIP, event.XForwardedFor, event.UserAgent)
	}
	if event.CacheTokens != 10 || event.TotalTokens != 30 || event.InputTokens != 10 || event.UncachedInputTokens != 0 {
		t.Fatalf("normalized tokens = input:%d uncached:%d cache:%d total:%d, want 10/0/10/30", event.InputTokens, event.UncachedInputTokens, event.CacheTokens, event.TotalTokens)
	}
	if event.CacheReadTokens != 7 || event.CacheWriteTokens != 3 {
		t.Fatalf("cache read/write tokens = %d/%d, want 7/3", event.CacheReadTokens, event.CacheWriteTokens)
	}
	if strings.Contains(event.RawJSON, "secret-cookie") || strings.Contains(event.RawJSON, "sk-secret") {
		t.Fatalf("RawJSON was not redacted: %s", event.RawJSON)
	}
}

func TestNormalizeRawScrubsSensitiveValuesAndAllowListsResponseHeaders(t *testing.T) {
	event, err := NormalizeRaw([]byte(`{
		"timestamp":"2026-09-18T00:00:00Z",
		"model":"gpt-test",
		"message":"request used x-api-key=plain-secret-value",
		"failed":true,
		"fail":{"body":"{\"error\":{\"message\":\"upstream rejected Bearer abcdefghijklmnop and sk-abcdefghijklmno\"}}"},
		"response_headers":{
			"X-Api-Key":"header-secret-value",
			"Authorization":"Bearer response-secret-value",
			"X-Upstream-Request-Id":"request-id-1",
			"Retry-After":"30",
			"X-Unreviewed-Header":"must-not-persist"
		}
	}`))
	if err != nil {
		t.Fatalf("NormalizeRaw() error = %v", err)
	}
	for _, secret := range []string{"plain-secret-value", "abcdefghijklmnop", "sk-abcdefghijklmno", "header-secret-value", "response-secret-value", "must-not-persist"} {
		if strings.Contains(event.RawJSON, secret) || strings.Contains(event.ErrorMessage, secret) {
			t.Fatalf("secret %q persisted in event: error=%q raw=%s", secret, event.ErrorMessage, event.RawJSON)
		}
	}
	if !strings.Contains(event.ErrorMessage, "[redacted]") {
		t.Fatalf("error message = %q, want redaction marker", event.ErrorMessage)
	}
	if !strings.Contains(event.RawJSON, "X-Upstream-Request-Id") || !strings.Contains(event.RawJSON, "Retry-After") {
		t.Fatalf("safe response headers were not retained: %s", event.RawJSON)
	}
	if strings.Contains(event.RawJSON, "X-Unreviewed-Header") || strings.Contains(event.RawJSON, "X-Api-Key") {
		t.Fatalf("non-allowlisted response headers persisted: %s", event.RawJSON)
	}
}

func TestScrubDiagnosticPayloadHandlesInvalidJSON(t *testing.T) {
	got := ScrubDiagnosticPayload("parse failed with Authorization: Bearer abcdefghijklmnop and api_key=sk-abcdefghijklmno")
	if strings.Contains(got, "abcdefghijklmnop") || strings.Contains(got, "sk-abcdefghijklmno") {
		t.Fatalf("ScrubDiagnosticPayload() = %q, want secrets removed", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("ScrubDiagnosticPayload() = %q, want redaction marker", got)
	}
}

func TestNormalizeRawUsesCanonicalTokenBreakdownAcrossProviderSemantics(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		wantInput      int64
		wantUncached   int64
		wantCacheRead  int64
		wantCacheWrite int64
		wantOutput     int64
		wantTotal      int64
	}{
		{
			name: "openai cache is an input subset",
			raw: `{
				"timestamp":"2026-07-26T00:00:00Z","provider":"codex","executor_type":"CodexExecutor","model":"gpt-test",
				"tokens":{"input_tokens":100,"output_tokens":30,"cached_tokens":40,"cache_read_tokens":40,"cache_creation_tokens":10,"total_tokens":130},
				"accounting_version":2,
				"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":130,"input":{"total_tokens":100,"uncached_tokens":50,"cache_read_tokens":40,"cache_write_tokens":10},"output":{"total_tokens":30,"non_reasoning_tokens":18,"reasoning_tokens":12},"unclassified_tokens":0}
			}`,
			wantInput: 100, wantUncached: 50, wantCacheRead: 40, wantCacheWrite: 10, wantOutput: 30, wantTotal: 130,
		},
		{
			name: "anthropic cache is independent input",
			raw: `{
				"timestamp":"2026-07-26T00:00:00Z","provider":"claude","executor_type":"ClaudeExecutor","model":"claude-test",
				"tokens":{"input_tokens":30,"output_tokens":5,"cached_tokens":7,"cache_read_tokens":7,"cache_creation_tokens":13,"total_tokens":55},
				"accounting_version":2,
				"token_breakdown":{"schema_version":2,"quality":"complete","total_tokens":55,"input":{"total_tokens":50,"uncached_tokens":30,"cache_read_tokens":7,"cache_write_tokens":13},"output":{"total_tokens":5,"non_reasoning_tokens":5,"reasoning_tokens":0},"unclassified_tokens":0}
			}`,
			wantInput: 50, wantUncached: 30, wantCacheRead: 7, wantCacheWrite: 13, wantOutput: 5, wantTotal: 55,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := NormalizeRaw([]byte(tt.raw))
			if err != nil {
				t.Fatalf("NormalizeRaw() error = %v", err)
			}
			if event.AccountingVersion != 2 || event.AccountingQuality != "complete" || !event.TokenBreakdown.Valid() {
				t.Fatalf("accounting metadata = version:%d quality:%q breakdown:%+v", event.AccountingVersion, event.AccountingQuality, event.TokenBreakdown)
			}
			if event.InputTokens != tt.wantInput || event.UncachedInputTokens != tt.wantUncached ||
				event.CacheReadTokens != tt.wantCacheRead || event.CacheWriteTokens != tt.wantCacheWrite ||
				event.CachedTokens != tt.wantCacheRead || event.OutputTokens != tt.wantOutput || event.TotalTokens != tt.wantTotal {
				t.Fatalf("tokens = %+v", event)
			}
		})
	}
}

func TestNormalizeRawLimitsClientRequestMetadata(t *testing.T) {
	clientIP := strings.Repeat("i", clientIPMaxRunes+1)
	xForwardedFor := strings.Repeat("f", xForwardedForMaxRunes+1)
	userAgent := strings.Repeat("u", userAgentMaxRunes+1)
	raw := `{"timestamp":"2026-07-28T00:00:00Z","model":"gpt-test","clientIp":"` + clientIP +
		`","xForwardedFor":"` + xForwardedFor + `","userAgent":"` + userAgent + `"}`
	event, err := NormalizeRaw([]byte(raw))
	if err != nil {
		t.Fatalf("NormalizeRaw() error = %v", err)
	}
	if len([]rune(event.ClientIP)) != clientIPMaxRunes || len([]rune(event.XForwardedFor)) != xForwardedForMaxRunes || len([]rune(event.UserAgent)) != userAgentMaxRunes {
		t.Fatalf("client metadata lengths = %d/%d/%d", len([]rune(event.ClientIP)), len([]rune(event.XForwardedFor)), len([]rune(event.UserAgent)))
	}
	if strings.Contains(event.RawJSON, "clientIp") || strings.Contains(event.RawJSON, "xForwardedFor") || strings.Contains(event.RawJSON, "userAgent") {
		t.Fatalf("RawJSON did not canonicalize client metadata: %s", event.RawJSON)
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

func TestNormalizeRawPreservesFrozenAPIKeyPolicyAttribution(t *testing.T) {
	event, err := NormalizeRaw([]byte(`{
		"event_hash":"policy-event",
		"timestamp_ms":1781308800000,
		"timestamp":"2026-06-13T00:00:00Z",
		"model":"gpt-5",
		"api_key_hash":"key-hash",
		"api_key_policy_id":"policy-a",
		"profile_id":"profile-a",
		"profile_name_snapshot":"Production",
		"policy_mode":"profile",
		"requested_model":"smart",
		"effective_model":"gpt-5"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if event.APIKeyPolicyID != "policy-a" || event.ProfileID != "profile-a" || event.ProfileNameSnapshot != "Production" || event.PolicyMode != "profile" || event.RequestedModel != "smart" || event.EffectiveModel != "gpt-5" {
		t.Fatalf("policy attribution = %#v", event)
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
		ClientIP:          "192.0.2.10",
		XForwardedFor:     "203.0.113.5, 198.51.100.8",
		UserAgent:         "test-client/1.0",
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
	if detail.ClientIP != "192.0.2.10" || detail.XForwardedFor != "203.0.113.5, 198.51.100.8" || detail.UserAgent != "test-client/1.0" {
		t.Fatalf("detail client metadata = %q/%q/%q", detail.ClientIP, detail.XForwardedFor, detail.UserAgent)
	}
}

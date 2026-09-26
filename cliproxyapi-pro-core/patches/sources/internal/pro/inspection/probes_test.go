package inspection

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeepProbeRequestBodies(t *testing.T) {
	t.Run("antigravity", func(t *testing.T) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(BuildAntigravityDeepProbeBody("project-1", "claude-sonnet-4-6")), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["project"] != "project-1" || payload["model"] != "claude-sonnet-4-6" {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("xai official health", func(t *testing.T) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(BuildXAIOfficialHealthBody(" grok-4.5 ")), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "grok-4.5" || payload["stream"] != false || payload["max_tokens"] != float64(1) {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("xai responses", func(t *testing.T) {
		var payload map[string]any
		if err := json.Unmarshal([]byte(BuildXAIDeepProbeBody(" grok-4.5 ")), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "grok-4.5" || payload["stream"] != true || payload["store"] != false || payload["max_output_tokens"] != float64(1) {
			t.Fatalf("payload = %#v", payload)
		}
	})
}

func TestXAIOfficialAPIQuotaDecision(t *testing.T) {
	active := XAIOfficialAPIQuotaDecision(false, `{"error":"credits exhausted"}`)
	if active.Action != ActionDisable || !active.IsQuota || !strings.Contains(active.ErrorDetail, "credits exhausted") {
		t.Fatalf("active decision = %#v", active)
	}
	disabled := XAIOfficialAPIQuotaDecision(true, `{"error":"credits exhausted"}`)
	if disabled.Action != ActionKeep || !disabled.IsQuota {
		t.Fatalf("disabled decision = %#v", disabled)
	}
}

func TestClassifyAntigravityDeepProbeResponse(t *testing.T) {
	tests := []struct {
		name string
		resp ProbeResponse
		want DeepProbeStatus
	}{
		{name: "success", resp: ProbeResponse{StatusCode: http.StatusOK, Body: `{"response":{"candidates":[{}]}}`}, want: DeepProbeSuccess},
		{name: "empty success", resp: ProbeResponse{StatusCode: http.StatusOK, Body: `{}`}, want: DeepProbeTransientError},
		{name: "quota before auth", resp: ProbeResponse{StatusCode: http.StatusForbidden, Body: `{"error":{"status":"RESOURCE_EXHAUSTED","message":"quota exhausted"}}`}, want: DeepProbeQuota},
		{name: "auth", resp: ProbeResponse{StatusCode: http.StatusUnauthorized, Body: `{"error":{"message":"invalid token"}}`}, want: DeepProbeAuthError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := ClassifyAntigravityDeepProbeResponse(tt.resp)
			if got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyXAIDeepProbeResponse(t *testing.T) {
	tests := []struct {
		name string
		resp ProbeResponse
		want DeepProbeStatus
	}{
		{name: "completed sse", resp: ProbeResponse{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.completed\"}\n\n"}, want: DeepProbeSuccess},
		{name: "intentional output cap", resp: ProbeResponse{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n"}, want: DeepProbeSuccess},
		{name: "content filter", resp: ProbeResponse{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n"}, want: DeepProbeTransientError},
		{name: "quota", resp: ProbeResponse{StatusCode: http.StatusForbidden, Body: `{"error":{"message":"out of credits"}}`}, want: DeepProbeQuota},
		{name: "auth", resp: ProbeResponse{StatusCode: http.StatusUnauthorized, Body: `{"error":{"message":"invalid token"}}`}, want: DeepProbeAuthError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := ClassifyXAIDeepProbeResponse(tt.resp)
			if got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunXAIDeepProbeWithRetry(t *testing.T) {
	t.Run("recovers from transport error", func(t *testing.T) {
		attempts := 0
		_, status, _, err := RunXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (ProbeResponse, error) {
			attempts++
			if attempts == 1 {
				return ProbeResponse{}, errors.New("temporary transport failure")
			}
			return ProbeResponse{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.completed\"}\n\n"}, nil
		})
		if err != nil || status != DeepProbeSuccess || attempts != 2 {
			t.Fatalf("status=%q attempts=%d err=%v", status, attempts, err)
		}
	})

	t.Run("does not retry content filter", func(t *testing.T) {
		attempts := 0
		_, status, message, err := RunXAIDeepProbeWithRetry(context.Background(), 0, 0, func() (ProbeResponse, error) {
			attempts++
			return ProbeResponse{StatusCode: http.StatusOK, Body: "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n"}, nil
		})
		if err != nil || status != DeepProbeTransientError || attempts != 1 || !strings.Contains(message, "content_filter") {
			t.Fatalf("status=%q message=%q attempts=%d err=%v", status, message, attempts, err)
		}
	})
}

func TestHTTPErrorHelpers(t *testing.T) {
	want := strings.TrimSpace(strings.Repeat("capacity unavailable ", 20))
	body, err := json.Marshal(map[string]any{"error": map[string]any{"message": want}})
	if err != nil {
		t.Fatal(err)
	}
	if got := SummarizeHTTPBody(string(body)); got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if got := HTTPErrorDetail("  " + string(body) + "\n"); got != string(body) {
		t.Fatalf("detail = %q, want %q", got, string(body))
	}
}

func TestHTTPErrorDetailRedactsAndTruncates(t *testing.T) {
	detail := HTTPErrorDetail(`{"error":{"message":"failed","access_token":"secret-token"},"authorization":"Bearer abc"}`)
	if strings.Contains(detail, "secret-token") || strings.Contains(detail, "Bearer abc") || !strings.Contains(detail, "[REDACTED]") {
		t.Fatalf("detail was not redacted: %q", detail)
	}
	plain := HTTPErrorDetail("request failed api_key=plain-secret password:guess Authorization: Bearer abc.def")
	if strings.Contains(plain, "plain-secret") || strings.Contains(plain, "guess") || strings.Contains(plain, "abc.def") {
		t.Fatalf("plain detail was not redacted: %q", plain)
	}
	brokenJSON := HTTPErrorDetail(`{"access_token":"broken-secret","authorization":"Bearer malformed","broken":`)
	if strings.Contains(brokenJSON, "broken-secret") || strings.Contains(brokenJSON, "Bearer malformed") || !strings.Contains(brokenJSON, "[REDACTED]") {
		t.Fatalf("broken JSON detail was not redacted: %q", brokenJSON)
	}
	html := HTTPErrorDetail(`<pre>"api_key": "html-secret" cookie='session-secret'</pre>`)
	if strings.Contains(html, "html-secret") || strings.Contains(html, "session-secret") {
		t.Fatalf("HTML-like detail was not redacted: %q", html)
	}
	for _, secret := range []string{"token=plain-token", "id_token=id-secret", "session_token=session-secret", "credential=credential-secret"} {
		if redacted := HTTPErrorDetail(secret); strings.Contains(redacted, strings.Split(secret, "=")[1]) {
			t.Fatalf("HTTPErrorDetail(%q) leaked secret: %q", secret, redacted)
		}
	}
	if summary := SummarizeHTTPBody(`{"error":{"message":"failed token=summary-secret"}}`); strings.Contains(summary, "summary-secret") {
		t.Fatalf("SummarizeHTTPBody() leaked secret: %q", summary)
	}
	large := HTTPErrorDetail(strings.Repeat("x", 20*1024))
	if len(large) > 17*1024 || !strings.HasSuffix(large, "[truncated]") {
		t.Fatalf("large detail length/suffix = %d, %q", len(large), large[len(large)-20:])
	}
}

func TestXAIDeepProbeCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool
		delay  time.Duration
	}{
		{"before first attempt", true, 0},
		{"between immediate attempts", false, 0},
		{"during retry wait", false, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.before {
				cancel()
			}
			attempts := 0
			_, _, _, err := RunXAIDeepProbeWithRetry(ctx, 2, tc.delay, func() (ProbeResponse, error) {
				attempts++
				cancel()
				return ProbeResponse{}, errors.New("transport interrupted")
			})
			want := 1
			if tc.before {
				want = 0
			}
			if !errors.Is(err, context.Canceled) || attempts != want {
				t.Fatalf("attempts=%d want=%d err=%v", attempts, want, err)
			}
		})
	}
}

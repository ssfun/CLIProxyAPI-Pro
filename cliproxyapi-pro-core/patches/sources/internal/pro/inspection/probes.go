package inspection

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const xaiDeepProbeMinAttempts = 2

// ProbeResponse is the transport-neutral response shape consumed by inspection
// protocol classifiers. Host adapters remain responsible for performing I/O.
type ProbeResponse struct {
	StatusCode int
	Body       string
}

func SelectAntigravityDeepProbeModel(preferredModel string) string {
	if model := strings.TrimSpace(preferredModel); model != "" {
		return model
	}
	return "claude-sonnet-4-6"
}

func BuildAntigravityDeepProbeBody(projectID, model string) string {
	raw, _ := json.Marshal(map[string]any{
		"project": projectID,
		"model":   model,
		"request": map[string]any{
			"contents": []map[string]any{{
				"role":  "user",
				"parts": []map[string]string{{"text": "ping"}},
			}},
			"generationConfig": map[string]any{"maxOutputTokens": 1},
		},
	})
	return string(raw)
}

func ClassifyAntigravityDeepProbeResponse(resp ProbeResponse) (DeepProbeStatus, string) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if HasAntigravityGenerateContent(resp.Body) {
			return DeepProbeSuccess, ""
		}
		return DeepProbeTransientError, "Antigravity 深度检测响应为空或格式异常"
	}
	message := SummarizeHTTPBody(resp.Body)
	if message == "" {
		message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if isQuotaHTTPStatus(resp.StatusCode) || IsAntigravityQuotaFailure(resp.Body) {
		return DeepProbeQuota, message
	}
	if IsAccountErrorStatus(resp.StatusCode) {
		return DeepProbeAuthError, message
	}
	return DeepProbeTransientError, message
}

func HasAntigravityGenerateContent(body string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	if candidates, ok := nestedMap(payload, "response")["candidates"].([]any); ok && len(candidates) > 0 {
		return true
	}
	if candidates, ok := payload["candidates"].([]any); ok && len(candidates) > 0 {
		return true
	}
	return false
}

func BuildXAIOfficialHealthBody(model string) string {
	raw, _ := json.Marshal(map[string]any{
		"model":      strings.TrimSpace(model),
		"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"stream":     false,
	})
	return string(raw)
}

func XAIOfficialAPIQuotaDecision(disabled bool, body string) Decision {
	reason := "xAI 官方 API 额度不足，建议建立额度保护"
	action := ActionDisable
	if disabled {
		reason = "xAI 官方 API 额度不足，但账号已禁用"
		action = ActionKeep
	}
	return Decision{
		Action:       action,
		ActionReason: reason,
		IsQuota:      true,
		ErrorDetail:  SummarizeHTTPBody(body),
	}
}

func BuildXAIDeepProbeBody(model string) string {
	raw, _ := json.Marshal(map[string]any{
		"model": strings.TrimSpace(model),
		"input": []map[string]any{{
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": "ping",
			}},
		}},
		"instructions":      "You are a helpful assistant. Reply briefly.",
		"max_output_tokens": 1,
		"stream":            true,
		"store":             false,
	})
	return string(raw)
}

func ClassifyXAIDeepProbeResponse(resp ProbeResponse) (DeepProbeStatus, string) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return ClassifyXAIDeepProbeSuccessBody(resp.Body)
	}
	message := SummarizeHTTPBody(resp.Body)
	if message == "" {
		message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if isQuotaHTTPStatus(resp.StatusCode) || IsXAIQuotaFailure(resp.Body) {
		return DeepProbeQuota, message
	}
	if IsAccountErrorStatus(resp.StatusCode) {
		return DeepProbeAuthError, message
	}
	return DeepProbeTransientError, message
}

func ClassifyXAIDeepProbeSuccessBody(body string) (DeepProbeStatus, string) {
	lastEvent := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(line), &payload) != nil {
			continue
		}
		eventType := strings.ToLower(strings.TrimSpace(stringFromProviderValue(payload["type"])))
		response := nestedMap(payload, "response")
		if eventType == "" {
			switch strings.ToLower(strings.TrimSpace(stringFromProviderValue(payload["status"]))) {
			case "completed":
				eventType = "response.completed"
			case "incomplete":
				eventType = "response.incomplete"
				response = payload
			case "failed":
				eventType = "response.failed"
				response = payload
			}
		}
		if eventType != "" {
			lastEvent = eventType
		}
		switch eventType {
		case "response.completed":
			return DeepProbeSuccess, ""
		case "response.incomplete":
			reason := strings.ToLower(strings.TrimSpace(stringFromProviderValue(nestedMap(response, "incomplete_details")["reason"])))
			if reason == "max_output_tokens" {
				return DeepProbeSuccess, ""
			}
			if reason == "" {
				reason = "unknown reason"
			}
			return DeepProbeTransientError, "xAI 深度检测响应未完成：" + reason
		case "response.failed", "error":
			message := nestedString(nestedMap(response, "error"), "message", "")
			if message == "" {
				message = nestedString(nestedMap(payload, "error"), "message", "")
			}
			if message == "" {
				message = "unknown response failure"
			}
			return DeepProbeTransientError, "xAI 深度检测响应失败：" + message
		}
	}
	if lastEvent != "" {
		return DeepProbeTransientError, "xAI 深度检测缺少终态事件，最后事件：" + lastEvent
	}
	return DeepProbeTransientError, "xAI 深度检测响应为空或格式异常"
}

func RunXAIDeepProbeWithRetry(
	ctx context.Context,
	retries int,
	retryDelay time.Duration,
	task func() (ProbeResponse, error),
) (ProbeResponse, DeepProbeStatus, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := retries + 1
	if attempts < xaiDeepProbeMinAttempts {
		attempts = xaiDeepProbeMinAttempts
	}
	if attempts > MaxRetries+1 {
		attempts = MaxRetries + 1
	}
	var last ProbeResponse
	var lastStatus DeepProbeStatus
	var lastMessage string
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return last, lastStatus, lastMessage, err
		}
		last, lastErr = task()
		if lastErr == nil {
			lastStatus, lastMessage = ClassifyXAIDeepProbeResponse(last)
			if last.StatusCode == http.StatusTooManyRequests {
				return last, lastStatus, lastMessage, nil
			}
			if !ShouldRetryXAIDeepProbe(lastStatus, lastMessage) {
				return last, lastStatus, lastMessage, nil
			}
		}
		if attempt+1 >= attempts {
			break
		}
		if retryDelay <= 0 {
			continue
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return last, lastStatus, lastMessage, ctx.Err()
		case <-timer.C:
		}
	}
	return last, lastStatus, lastMessage, lastErr
}

func ShouldRetryXAIDeepProbe(status DeepProbeStatus, message string) bool {
	return status == DeepProbeTransientError && !strings.Contains(strings.ToLower(message), "content_filter")
}

func SummarizeHTTPBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if message := nestedString(nestedMap(payload, "error"), "message", ""); message != "" {
			return redactSensitiveText(strings.TrimSpace(message))
		}
		if message := stringFromProviderValue(payload["error"]); message != "" {
			return redactSensitiveText(strings.TrimSpace(message))
		}
		if message := stringFromProviderValue(payload["message"]); message != "" {
			return redactSensitiveText(strings.TrimSpace(message))
		}
	}
	return truncateSensitiveSummary(body)
}

func truncateSensitiveSummary(value string) string {
	const maxSummaryBytes = 240
	value = redactSensitiveText(strings.TrimSpace(value))
	if len(value) <= maxSummaryBytes {
		return value
	}
	cut := maxSummaryBytes
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	return value[:cut]
}

func HTTPErrorDetail(body string) string {
	const maxErrorDetailBytes = 16 * 1024
	detail := strings.TrimSpace(body)
	if detail == "" {
		return ""
	}
	var payload any
	if json.Unmarshal([]byte(detail), &payload) == nil {
		redactSensitiveJSON(payload)
		if encoded, err := json.Marshal(payload); err == nil {
			detail = string(encoded)
		}
	}
	detail = redactSensitiveText(detail)
	if len(detail) <= maxErrorDetailBytes {
		return detail
	}
	cut := maxErrorDetailBytes
	for cut > 0 && !utf8.ValidString(detail[:cut]) {
		cut--
	}
	return detail[:cut] + "\n[truncated]"
}

func redactSensitiveJSON(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
			if strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "password") || strings.Contains(normalized, "credential") || normalized == "authorization" || normalized == "api_key" || normalized == "apikey" || normalized == "cookie" || normalized == "set_cookie" {
				typed[key] = "[REDACTED]"
				continue
			}
			redactSensitiveJSON(child)
		}
	case []any:
		for _, child := range typed {
			redactSensitiveJSON(child)
		}
	}
}

func redactSensitiveText(value string) string {
	value = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`).ReplaceAllString(value, "Bearer [REDACTED]")
	return regexp.MustCompile(`(?i)("?(?:authorization|api[_-]?key|(?:[a-z0-9]+[_-])*token|password|secret|credential|cookie)"?)(\s*[:=]\s*)(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,;&}]+)`).ReplaceAllString(value, "${1}${2}[REDACTED]")
}

func WithHTTPErrorDetail(decision Decision, body string) Decision {
	decision.ErrorDetail = HTTPErrorDetail(body)
	return decision
}

func FirstStatus(statuses ...*int) *int {
	for _, status := range statuses {
		if status != nil {
			return status
		}
	}
	return nil
}

func FirstNonZeroStatus(values ...int) *int {
	for _, value := range values {
		if value != 0 {
			valueCopy := value
			return &valueCopy
		}
	}
	return nil
}

func isQuotaHTTPStatus(status int) bool {
	return status == http.StatusPaymentRequired
}

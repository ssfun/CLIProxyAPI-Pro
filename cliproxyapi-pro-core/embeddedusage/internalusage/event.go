package internalusage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	ID                int64    `json:"id,omitempty"`
	RequestID         string   `json:"request_id,omitempty"`
	EventHash         string   `json:"event_hash"`
	TimestampMS       int64    `json:"timestamp_ms"`
	Timestamp         string   `json:"timestamp"`
	Provider          string   `json:"provider,omitempty"`
	ExecutorType      string   `json:"executor_type,omitempty"`
	Model             string   `json:"model"`
	Alias             string   `json:"alias,omitempty"`
	Endpoint          string   `json:"endpoint,omitempty"`
	Method            string   `json:"method,omitempty"`
	Path              string   `json:"path,omitempty"`
	AuthType          string   `json:"auth_type,omitempty"`
	AuthIndex         string   `json:"auth_index,omitempty"`
	Source            string   `json:"source,omitempty"`
	SourceHash        string   `json:"source_hash,omitempty"`
	APIKeyHash        string   `json:"api_key_hash,omitempty"`
	InputTokens       int64    `json:"input_tokens"`
	OutputTokens      int64    `json:"output_tokens"`
	ReasoningTokens   int64    `json:"reasoning_tokens"`
	CachedTokens      int64    `json:"cached_tokens"`
	CacheTokens       int64    `json:"cache_tokens"`
	CacheReadTokens   int64    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens  int64    `json:"cache_write_tokens,omitempty"`
	TotalTokens       int64    `json:"total_tokens"`
	LatencyMS         *int64   `json:"latency_ms,omitempty"`
	TTFTMS            *int64   `json:"ttft_ms,omitempty"`
	StatusCode        *int     `json:"status_code,omitempty"`
	ErrorCode         string   `json:"error_code,omitempty"`
	ErrorMessage      string   `json:"error_message,omitempty"`
	UpstreamRequestID string   `json:"upstream_request_id,omitempty"`
	RetryAfter        string   `json:"retry_after,omitempty"`
	Stream            bool     `json:"stream"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	ServiceTier       string   `json:"service_tier,omitempty"`
	EstimatedCost     *float64 `json:"estimated_cost,omitempty"`
	PriceRuleID       int64    `json:"price_rule_id,omitempty"`
	CostBreakdownJSON string   `json:"cost_breakdown_json,omitempty"`
	Failed            bool     `json:"failed"`
	RawJSON           string   `json:"raw_json,omitempty"`
	CreatedAtMS       int64    `json:"created_at_ms"`
}

type Tokens struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
	CacheTokens      int64 `json:"cache_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens"`
}

type Detail struct {
	ID                int64           `json:"id,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	Timestamp         string          `json:"timestamp"`
	Source            string          `json:"source"`
	AuthIndex         string          `json:"auth_index,omitempty"`
	APIKeyHash        string          `json:"api_key_hash,omitempty"`
	Provider          string          `json:"provider,omitempty"`
	ExecutorType      string          `json:"executor_type,omitempty"`
	Alias             string          `json:"alias,omitempty"`
	AuthType          string          `json:"auth_type,omitempty"`
	LatencyMS         *int64          `json:"latency_ms,omitempty"`
	TTFTMS            *int64          `json:"ttft_ms,omitempty"`
	StatusCode        *int            `json:"status_code,omitempty"`
	ErrorCode         string          `json:"error_code,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
	UpstreamRequestID string          `json:"upstream_request_id,omitempty"`
	RetryAfter        string          `json:"retry_after,omitempty"`
	Stream            bool            `json:"stream"`
	ReasoningEffort   string          `json:"reasoning_effort,omitempty"`
	ServiceTier       string          `json:"service_tier,omitempty"`
	EstimatedCost     *float64        `json:"estimated_cost,omitempty"`
	PriceRuleID       int64           `json:"price_rule_id,omitempty"`
	CostBreakdown     json.RawMessage `json:"cost_breakdown,omitempty"`
	Tokens            Tokens          `json:"tokens"`
	Failed            bool            `json:"failed"`
}

type ModelAggregate struct {
	Details []Detail `json:"details"`
}

type APIAggregate struct {
	Models map[string]*ModelAggregate `json:"models"`
}

type Payload struct {
	TotalRequests  int64                    `json:"total_requests"`
	SuccessCount   int64                    `json:"success_count"`
	FailureCount   int64                    `json:"failure_count"`
	TotalTokens    int64                    `json:"total_tokens"`
	LatestID       int64                    `json:"latest_id"`
	Generation     int64                    `json:"generation"`
	ResetAtMS      int64                    `json:"reset_at_ms,omitempty"`
	DetailsCount   int64                    `json:"details_count,omitempty"`
	DetailsLimit   int64                    `json:"details_limit,omitempty"`
	DetailsLimited bool                     `json:"details_limited,omitempty"`
	MatchedTotal   int64                    `json:"matched_total,omitempty"`
	SnapshotMaxID  int64                    `json:"snapshot_max_id,omitempty"`
	PageCursor     string                   `json:"page_cursor,omitempty"`
	NextCursor     string                   `json:"next_cursor,omitempty"`
	HasMore        bool                     `json:"has_more,omitempty"`
	APIs           map[string]*APIAggregate `json:"apis"`
}

var endpointPattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|OPTIONS|HEAD)\s+(\S+)`)

func NormalizeRaw(raw []byte) (Event, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Event{}, err
	}
	record, ok := payload.(map[string]any)
	if !ok {
		return Event{}, fmt.Errorf("usage payload is not a JSON object")
	}

	rawJSON := ""
	if _, exported := record["event_hash"]; !exported {
		redacted := redactValue(payload)
		redactedJSON, _ := json.Marshal(redacted)
		rawJSON = string(redactedJSON)
	}

	_, exported := record["event_hash"]
	timestampMS, timestamp := readTimestamp(record, exported)
	method := strings.ToUpper(readString(record, "method"))
	path := readString(record, "path")
	endpoint := readString(record, "endpoint")
	if endpoint == "" && method != "" && path != "" {
		endpoint = method + " " + path
	}
	if endpoint != "" {
		if match := endpointPattern.FindStringSubmatch(endpoint); len(match) == 3 {
			if method == "" {
				method = strings.ToUpper(match[1])
			}
			if path == "" {
				path = match[2]
			}
		}
	}
	if endpoint == "" {
		endpoint = "-"
	}

	inputTokens, outputTokens, reasoningTokens, cachedTokens, cacheTokens, cacheReadTokens, cacheWriteTokens, totalTokens := readTokenFields(record, exported)
	if totalTokens <= 0 {
		totalTokens = inputTokens + outputTokens + reasoningTokens + maxInt64(cachedTokens, cacheTokens)
	}

	latencyMS := readOptionalInt(record, "latency_ms")
	ttftMS := readOptionalInt(record, "ttft_ms")
	statusCode := readOptionalInt32(record, "status_code")
	fail := firstMap(record, "fail")
	if statusCode == nil && fail != nil {
		statusCode = readOptionalInt32(fail, "status_code")
	}
	errorCode := readString(record, "error_code")
	errorMessage := readString(record, "error_message")
	if fail != nil {
		if errorMessage == "" {
			errorMessage = readString(fail, "body")
		}
	}
	errorMessage = summarizeErrorMessage(errorMessage)
	upstreamRequestID := readString(record, "upstream_request_id")
	if upstreamRequestID == "" {
		upstreamRequestID = readHeaderValue(record, "x-upstream-request-id", "x-request-id", "openai-request-id", "anthropic-request-id", "cf-ray")
	}
	retryAfter := readString(record, "retry_after")
	if retryAfter == "" {
		retryAfter = readHeaderValue(record, "retry-after")
	}
	failed := readFailed(record)
	sourceRaw := readString(record, "source")
	source := maskSource(sourceRaw)
	apiKey := readString(record, "api_key")
	authIndex := readString(record, "auth_index")
	sourceHash := hashString(sourceRaw)
	apiKeyHash := hashString(apiKey)
	if exported {
		if value := readString(record, "source_hash"); value != "" {
			sourceHash = value
		}
		if value := readString(record, "api_key_hash"); value != "" {
			apiKeyHash = value
		}
	}

	event := Event{
		RequestID:         readString(record, "request_id"),
		TimestampMS:       timestampMS,
		Timestamp:         timestamp,
		Provider:          readString(record, "provider"),
		ExecutorType:      readString(record, "executor_type"),
		Model:             readString(record, "model"),
		Alias:             readString(record, "alias"),
		Endpoint:          endpoint,
		Method:            method,
		Path:              path,
		AuthType:          readString(record, "auth_type"),
		AuthIndex:         authIndex,
		Source:            source,
		SourceHash:        sourceHash,
		APIKeyHash:        apiKeyHash,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		ReasoningTokens:   reasoningTokens,
		CachedTokens:      cachedTokens,
		CacheTokens:       cacheTokens,
		CacheReadTokens:   cacheReadTokens,
		CacheWriteTokens:  cacheWriteTokens,
		TotalTokens:       totalTokens,
		LatencyMS:         latencyMS,
		TTFTMS:            ttftMS,
		StatusCode:        statusCode,
		ErrorCode:         errorCode,
		ErrorMessage:      errorMessage,
		UpstreamRequestID: upstreamRequestID,
		RetryAfter:        retryAfter,
		Stream:            readBool(record, "stream"),
		ReasoningEffort:   readString(record, "reasoning_effort"),
		ServiceTier:       readString(record, "service_tier"),
		Failed:            failed,
		RawJSON:           rawJSON,
		CreatedAtMS:       time.Now().UnixMilli(),
	}
	if exported {
		if value, ok := readOptionalFloat(record, "estimated_cost"); ok {
			event.EstimatedCost = &value
		}
		event.PriceRuleID = readInt(record, "price_rule_id")
		event.CostBreakdownJSON = readString(record, "cost_breakdown_json")
	}
	if event.Model == "" {
		event.Model = "-"
	}
	event.EventHash = readString(record, "event_hash")
	if event.EventHash == "" {
		event.EventHash = buildEventHash(event)
	}
	return event, nil
}

func BuildPayload(events []Event) Payload {
	payload := Payload{DetailsCount: int64(len(events)), APIs: map[string]*APIAggregate{}}
	for _, event := range events {
		payload.TotalRequests++
		if event.Failed {
			payload.FailureCount++
		} else {
			payload.SuccessCount++
		}
		payload.TotalTokens += event.TotalTokens
		if event.ID > payload.LatestID {
			payload.LatestID = event.ID
		}

		endpoint := event.Endpoint
		if endpoint == "" {
			endpoint = "-"
		}
		apiEntry := payload.APIs[endpoint]
		if apiEntry == nil {
			apiEntry = &APIAggregate{Models: map[string]*ModelAggregate{}}
			payload.APIs[endpoint] = apiEntry
		}
		model := event.Model
		if model == "" {
			model = "-"
		}
		modelEntry := apiEntry.Models[model]
		if modelEntry == nil {
			modelEntry = &ModelAggregate{}
			apiEntry.Models[model] = modelEntry
		}
		var costBreakdown json.RawMessage
		if json.Valid([]byte(event.CostBreakdownJSON)) {
			costBreakdown = json.RawMessage(event.CostBreakdownJSON)
		}
		modelEntry.Details = append(modelEntry.Details, Detail{
			ID:                event.ID,
			RequestID:         event.RequestID,
			Timestamp:         event.Timestamp,
			Source:            event.Source,
			AuthIndex:         event.AuthIndex,
			APIKeyHash:        event.APIKeyHash,
			Provider:          event.Provider,
			ExecutorType:      event.ExecutorType,
			Alias:             event.Alias,
			AuthType:          event.AuthType,
			LatencyMS:         event.LatencyMS,
			TTFTMS:            event.TTFTMS,
			StatusCode:        event.StatusCode,
			ErrorCode:         event.ErrorCode,
			ErrorMessage:      event.ErrorMessage,
			UpstreamRequestID: event.UpstreamRequestID,
			RetryAfter:        event.RetryAfter,
			Stream:            event.Stream,
			ReasoningEffort:   event.ReasoningEffort,
			ServiceTier:       event.ServiceTier,
			EstimatedCost:     event.EstimatedCost,
			PriceRuleID:       event.PriceRuleID,
			CostBreakdown:     costBreakdown,
			Failed:            event.Failed,
			Tokens: Tokens{
				InputTokens:      event.InputTokens,
				OutputTokens:     event.OutputTokens,
				ReasoningTokens:  event.ReasoningTokens,
				CachedTokens:     event.CachedTokens,
				CacheTokens:      event.CacheTokens,
				CacheReadTokens:  event.CacheReadTokens,
				CacheWriteTokens: event.CacheWriteTokens,
				TotalTokens:      event.TotalTokens,
			},
		})
	}
	return payload
}

func readOptionalFloat(record map[string]any, key string) (float64, bool) {
	value, exists := record[key]
	if !exists || value == nil {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func readTimestamp(record map[string]any, exported bool) (int64, string) {
	raw := record["timestamp"]
	if raw == nil && exported {
		raw = record["timestamp_ms"]
	}
	now := time.Now()
	if raw == nil {
		return now.UnixMilli(), now.UTC().Format(time.RFC3339Nano)
	}
	switch value := raw.(type) {
	case float64:
		ms := int64(value)
		if ms < 10_000_000_000 {
			ms *= 1000
		}
		return ms, time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
	case string:
		trimmed := strings.TrimSpace(value)
		if number, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			if number < 10_000_000_000 {
				number *= 1000
			}
			return number, time.UnixMilli(number).UTC().Format(time.RFC3339Nano)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed.UnixMilli(), parsed.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return now.UnixMilli(), now.UTC().Format(time.RFC3339Nano)
}

func readTokenFields(record map[string]any, exported bool) (int64, int64, int64, int64, int64, int64, int64, int64) {
	tokens := map[string]any{}
	if nested, ok := record["tokens"].(map[string]any); ok {
		tokens = nested
	}
	input := readIntFrom(tokens, "input_tokens")
	if input == 0 && exported {
		input = readInt(record, "input_tokens")
	}
	output := readIntFrom(tokens, "output_tokens")
	if output == 0 && exported {
		output = readInt(record, "output_tokens")
	}
	reasoning := readIntFrom(tokens, "reasoning_tokens")
	if reasoning == 0 && exported {
		reasoning = readInt(record, "reasoning_tokens")
	}
	cached := readIntFrom(tokens, "cached_tokens")
	if cached == 0 && exported {
		cached = readInt(record, "cached_tokens")
	}
	cacheRead := readIntFrom(tokens, "cache_read_tokens")
	cacheWrite := readIntFrom(tokens, "cache_creation_tokens")
	cache := cacheRead + cacheWrite
	if cache == 0 && exported {
		cache = readInt(record, "cache_tokens")
	}
	if cacheRead == 0 && exported {
		cacheRead = readInt(record, "cache_read_tokens")
	}
	if cacheWrite == 0 && exported {
		cacheWrite = readInt(record, "cache_write_tokens")
	}
	if cacheRead == 0 && cacheWrite == 0 {
		cacheRead = maxInt64(cached, cache)
	}
	total := readIntFrom(tokens, "total_tokens")
	if total == 0 && exported {
		total = readInt(record, "total_tokens")
	}
	return input, output, reasoning, cached, cache, cacheRead, cacheWrite, total
}

func readFailed(record map[string]any) bool {
	if value, ok := record["failed"].(bool); ok {
		return value
	}
	status := readInt(record, "status_code")
	if status >= 400 {
		return true
	}
	if fail := firstMap(record, "fail"); fail != nil {
		return readInt(fail, "status_code") >= 400
	}
	return record["error_message"] != nil
}

func readBool(record map[string]any, keys ...string) bool {
	raw := first(record, keys...)
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
		return parsed
	case json.Number:
		parsed, _ := strconv.ParseBool(value.String())
		return parsed
	case float64:
		return value != 0
	default:
		return false
	}
}

func readOptionalInt32(record map[string]any, keys ...string) *int {
	value := readInt(record, keys...)
	if value == 0 && first(record, keys...) == nil {
		return nil
	}
	converted := int(value)
	return &converted
}

func readOptionalInt(record map[string]any, keys ...string) *int64 {
	value := readInt(record, keys...)
	if value == 0 && first(record, keys...) == nil {
		return nil
	}
	return &value
}

func readString(record map[string]any, keys ...string) string {
	raw := first(record, keys...)
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func readHeaderValue(record map[string]any, names ...string) string {
	headers := firstMap(record, "response_headers")
	if headers == nil {
		return ""
	}
	normalizedNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		normalized := normalizeHeaderName(name)
		if normalized != "" {
			normalizedNames[normalized] = struct{}{}
		}
	}
	for key, raw := range headers {
		if _, ok := normalizedNames[normalizeHeaderName(key)]; !ok {
			continue
		}
		switch value := raw.(type) {
		case string:
			return strings.TrimSpace(value)
		case []any:
			for _, item := range value {
				text := strings.TrimSpace(fmt.Sprint(item))
				if text != "" {
					return text
				}
			}
		case []string:
			for _, item := range value {
				if text := strings.TrimSpace(item); text != "" {
					return text
				}
			}
		default:
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeHeaderName(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "-"))
}

func readInt(record map[string]any, keys ...string) int64 {
	return readIntFrom(record, keys...)
}

func readIntFrom(record map[string]any, keys ...string) int64 {
	raw := first(record, keys...)
	switch value := raw.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case json.Number:
		number, _ := value.Int64()
		return number
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func first(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func firstMap(record map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := record[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func summarizeErrorMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err == nil {
		if message := readString(firstMap(payload, "error"), "message"); message != "" {
			value = message
		} else if message := readString(payload, "message", "error"); message != "" {
			value = message
		}
	}
	if len(value) > 240 {
		return value[:240]
	}
	return value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func hashString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}

func buildEventHash(event Event) string {
	parts := []string{
		event.RequestID,
		event.Timestamp,
		event.Endpoint,
		event.Model,
		event.AuthIndex,
		event.SourceHash,
		event.APIKeyHash,
		strconv.FormatInt(event.InputTokens, 10),
		strconv.FormatInt(event.OutputTokens, 10),
		strconv.FormatInt(event.ReasoningTokens, 10),
		strconv.FormatInt(maxInt64(event.CachedTokens, event.CacheTokens), 10),
		strconv.FormatBool(event.Failed),
	}
	if event.LatencyMS != nil {
		parts = append(parts, strconv.FormatInt(*event.LatencyMS, 10))
	}
	return hashString(strings.Join(parts, "|"))
}

func maskSource(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "@") {
		parts := strings.SplitN(trimmed, "@", 2)
		prefix := parts[0]
		if len(prefix) > 3 {
			prefix = prefix[:3]
		}
		return prefix + "***@" + parts[1]
	}
	if looksSecret(trimmed) {
		if len(trimmed) <= 8 {
			return "m:****"
		}
		return "m:" + trimmed[:4] + "..." + trimmed[len(trimmed)-4:]
	}
	return trimmed
}

func looksSecret(value string) bool {
	if strings.ContainsAny(value, " /\\") {
		return false
	}
	return strings.HasPrefix(value, "sk-") || strings.HasPrefix(value, "AIza") || len(value) >= 32
}

func redactValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for key, child := range item {
			if isSecretKey(key) {
				result[key] = "[redacted]"
				continue
			}
			result[key] = redactValue(child)
		}
		return result
	case []any:
		result := make([]any, 0, len(item))
		for _, child := range item {
			result = append(result, redactValue(child))
		}
		return result
	default:
		return value
	}
}

func isSecretKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	return normalized == "api_key" ||
		normalized == "apikey" ||
		normalized == "authorization" ||
		normalized == "cookie" ||
		normalized == "set_cookie" ||
		normalized == "access_token" ||
		normalized == "refresh_token" ||
		normalized == "token" ||
		strings.Contains(normalized, "secret")
}

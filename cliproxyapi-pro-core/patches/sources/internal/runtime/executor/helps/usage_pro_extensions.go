package helps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	apikeypolicy "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func prepareUsageRecordForPublish(ctx context.Context, record *usage.Record) {
	if record == nil {
		return
	}
	record.ResponseHeaders = internallogging.GetResponseHeaders(ctx)
	if attemptIndex, ok := usage.AttemptIndexFromContext(ctx); ok {
		record.AttemptIndex = &attemptIndex
	}
	quotaDetail := usage.EnsureTokenBreakdownForProvider(record.Detail, record.Provider, record.ExecutorType)
	totalTokens := quotaDetail.TokenBreakdown.TotalTokens
	if totalTokens == 0 {
		totalTokens = quotaDetail.TotalTokens
	}
	attemptIndex := int64(-1)
	if record.AttemptIndex != nil {
		attemptIndex = *record.AttemptIndex
	}
	eventID := fmt.Sprintf("usage:%d:provider=%q:executor=%q:model=%q:alias=%q:attempt=%d", record.RequestedAt.UnixNano(), record.Provider, record.ExecutorType, record.Model, record.Alias, attemptIndex)
	if err := apikeypolicy.SettleQuotaUsage(ctx, eventID, apikeypolicy.QuotaUsageDelta{
		Provider: record.Provider, AuthType: record.AuthType, Model: record.Model,
		InputTokens: quotaDetail.InputTokens, OutputTokens: quotaDetail.OutputTokens,
		ReasoningTokens: quotaDetail.ReasoningTokens, CachedTokens: quotaDetail.CachedTokens,
		CacheReadTokens: quotaDetail.CacheReadTokens, CacheWriteTokens: quotaDetail.CacheCreationTokens,
		TotalTokens: totalTokens, ServiceTier: record.ServiceTier,
		EffectiveServiceTier: record.ResponseServiceTier, Speed: record.Speed,
		EffectiveSpeed: record.ResponseSpeed,
	}); err != nil {
		log.WithError(err).Error("failed to settle API key quota usage")
	}
}

func (b *StreamUsageBuffer) ObserveClaude(detail usage.Detail, ok bool) {
	if b == nil || !ok {
		return
	}
	preservedInput := b.detail.InputTokens
	preservedCacheRead := b.detail.CacheReadTokens
	preservedCacheCreation := b.detail.CacheCreationTokens
	b.Observe(detail, true)
	merged := false
	if detail.InputTokens == 0 && preservedInput != 0 {
		b.detail.InputTokens = preservedInput
		merged = true
	}
	if detail.CacheReadTokens == 0 {
		b.detail.CacheReadTokens = preservedCacheRead
		merged = merged || preservedCacheRead != 0
	}
	if detail.CacheCreationTokens == 0 {
		b.detail.CacheCreationTokens = preservedCacheCreation
		merged = merged || preservedCacheCreation != 0
	}
	if !merged {
		return
	}
	b.detail.CachedTokens = b.detail.CacheReadTokens
	if b.detail.CachedTokens == 0 {
		b.detail.CachedTokens = b.detail.CacheCreationTokens
	}
	b.detail.TotalTokens = b.detail.InputTokens + b.detail.OutputTokens + b.detail.CacheReadTokens + b.detail.CacheCreationTokens
	b.detail.TokenBreakdown = usage.NewIndependentTokenBreakdown(
		b.detail.InputTokens,
		b.detail.CacheReadTokens,
		b.detail.CacheCreationTokens,
		max(b.detail.OutputTokens-b.detail.ReasoningTokens, 0),
		b.detail.ReasoningTokens,
		b.detail.TotalTokens,
	)
}

// ObserveUpstreamRequestModel observes a final JSON request/frame, never a
// pre-normalization payload. It also supports bounded prefixes of replayable HTTP
// bodies, provided the model string itself is complete.
func (r *UsageReporter) ObserveUpstreamRequestModel(payload []byte) {
	if r == nil {
		return
	}
	for _, path := range []string{"model", "request.model"} {
		value := gjson.GetBytes(payload, path)
		if value.Type == gjson.String && json.Valid([]byte(value.Raw)) {
			model := strings.TrimSpace(value.String())
			if model != "" && len(model) <= maxResponseModelLength {
				r.SetUpstreamModel(model)
				return
			}
		}
	}
}

// ObserveUpstreamHTTPRequest reads a separate replay reader, never req.Body.
// The bounded prefix avoids copying large image/audio bodies for diagnostics.
// If the model is unavailable it stays unknown instead of becoming an alias.
func (r *UsageReporter) ObserveUpstreamHTTPRequest(req *http.Request) {
	if r == nil || req == nil {
		return
	}
	if req.URL != nil {
		path := req.URL.Path
		if index := strings.LastIndex(path, "/models/"); index >= 0 {
			name, action, ok := strings.Cut(path[index+len("/models/"):], ":")
			if ok && name != "" && !strings.Contains(name, "/") {
				switch action {
				case "generateContent", "streamGenerateContent", "countTokens", "predict", "streamRawPredict":
					r.SetUpstreamModel(name)
					return
				}
			}
		}
	}
	if req.GetBody == nil {
		return
	}
	replay, err := req.GetBody()
	if err != nil || replay == nil {
		return
	}
	defer replay.Close()
	const maxModelPrefix = 64 * 1024
	payload, err := io.ReadAll(io.LimitReader(replay, maxModelPrefix))
	if err == nil {
		r.ObserveUpstreamRequestModel(payload)
	}
}

// SetResponseModelFormat selects a plugin's negotiated output protocol without
// changing provider identity used by accounting. Native reporters leave it unset.
func (r *UsageReporter) SetResponseModelFormat(format string) {
	if r == nil {
		return
	}
	provider := "openai"
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "claude":
		provider = "claude"
	case "gemini", "antigravity":
		provider = "gemini"
	}
	r.responseModelMu.Lock()
	r.responseModelFormat = provider
	r.responseModelMu.Unlock()
}

func (r *UsageReporter) auditUpstreamModel(model string) string {
	// Side-model/tool records do not describe the main upstream request.
	if r == nil || model != r.model {
		return ""
	}
	if upstream := r.UpstreamModel(); upstream != "" {
		return upstream
	}
	return ""
}

func modelMatchStatus(sent, response string) string {
	if strings.TrimSpace(sent) == "" || strings.TrimSpace(response) == "" {
		return "unknown"
	}
	if normalizeModelName(sent) == normalizeModelName(response) {
		return "match"
	}
	if !IsModelSubstituted(sent, response) {
		return "variant"
	}
	return "mismatch"
}

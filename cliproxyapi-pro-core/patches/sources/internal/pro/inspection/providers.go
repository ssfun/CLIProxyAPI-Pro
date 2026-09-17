package inspection

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func stringFromProviderValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func emptyStringAsNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func quotaResetAtMS(candidates ...any) (int64, bool) {
	for _, candidate := range candidates {
		if text := stringFromProviderValue(candidate); text != "" {
			normalized := regexpMustCompile(`(\.\d{6})\d+`).ReplaceAllString(text, "$1")
			if parsed, err := time.Parse(time.RFC3339Nano, normalized); err == nil {
				return parsed.UnixMilli(), true
			}
		}
		if numeric, ok := floatFromAny(candidate); ok && numeric > 0 {
			if numeric < 1e11 {
				numeric *= 1000
			}
			return int64(numeric), true
		}
	}
	return 0, false
}

func quotaResetAfterMS(value any, now time.Time) (int64, bool) {
	seconds, ok := floatFromAny(value)
	if !ok || seconds <= 0 {
		return 0, false
	}
	return now.UnixMilli() + int64(seconds*1000), true
}

func quotaPeriodHoursFromSeconds(value any) (float64, bool) {
	seconds, ok := floatFromAny(value)
	if !ok || seconds <= 0 {
		return 0, false
	}
	return seconds / 3600, true
}

func antigravityPeriodHours(window string) (float64, bool) {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "5h", "five-hour", "five_hour":
		return 5, true
	case "weekly", "week":
		return 24 * 7, true
	default:
		return 0, false
	}
}

func BuildAntigravityGroups(body string) ([]map[string]any, error) {
	payload, err := ParseAntigravityQuotaPayload(body)
	if err != nil {
		return nil, err
	}
	if groups := buildAntigravitySummaryGroups(payload); len(groups) > 0 {
		return groups, nil
	}
	return nil, fmt.Errorf("empty antigravity quota groups")
}

var antigravityPlanByTierID = map[string]string{
	"free-tier":          "free",
	"g1-pro-tier":        "pro",
	"g1-ultra-tier":      "ultra",
	"g1-ultra-lite-tier": "ultra-lite",
}

func BuildAntigravitySubscription(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	rawCurrentTier := firstAny(payload, "currentTier", "current_tier")
	rawPaidTier := firstAny(payload, "paidTier", "paid_tier")
	currentTier := normalizeAntigravityTier(rawCurrentTier)
	paidTier := normalizeAntigravityTier(rawPaidTier)
	effectiveTier := currentTier
	source := "current"
	if stringFromProviderValue(paidTier["id"]) != "" {
		effectiveTier = paidTier
		source = "paid"
	}
	tierID := stringFromProviderValue(effectiveTier["id"])
	tierName := stringFromProviderValue(effectiveTier["name"])
	if tierID == "" && tierName == "" {
		return nil
	}
	plan := antigravityPlanByTierID[tierID]
	if plan == "" {
		plan = "unknown"
	}
	subscription := map[string]any{
		"plan":     plan,
		"tierId":   emptyStringAsNil(tierID),
		"tierName": emptyStringAsNil(tierName),
		"source":   source,
	}
	if currentTier != nil {
		subscription["currentTier"] = currentTier
	}
	if paidTier != nil {
		subscription["paidTier"] = paidTier
		if paidTierPayload, ok := rawPaidTier.(map[string]any); ok {
			credits := normalizeAntigravityCredits(firstAny(paidTierPayload, "availableCredits", "available_credits"))
			if len(credits) > 0 {
				subscription["availableCredits"] = credits
			}
		}
	}
	if _, ok := subscription["availableCredits"]; !ok {
		if currentTierPayload, ok := rawCurrentTier.(map[string]any); ok {
			credits := normalizeAntigravityCredits(firstAny(currentTierPayload, "availableCredits", "available_credits"))
			if len(credits) > 0 {
				subscription["availableCredits"] = credits
			}
		}
	}
	return subscription
}

func normalizeAntigravityTier(value any) map[string]any {
	tier, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	id := stringFromProviderValue(tier["id"])
	name := stringFromProviderValue(tier["name"])
	if id == "" && name == "" {
		return nil
	}
	return map[string]any{
		"id":   emptyStringAsNil(id),
		"name": emptyStringAsNil(name),
	}
}

func normalizeAntigravityCredits(value any) []map[string]any {
	items := anySlice(value)
	if len(items) == 0 {
		return nil
	}
	credits := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		credit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		creditType := stringFromProviderValue(firstAny(credit, "creditType", "credit_type"))
		creditAmount := normalizeAntigravityCreditValue(firstAny(credit, "creditAmount", "credit_amount"))
		minimum := normalizeAntigravityCreditValue(firstAny(credit, "minimumCreditAmountForUsage", "minimum_credit_amount_for_usage"))
		if creditType == "" && creditAmount == nil {
			continue
		}
		credits = append(credits, map[string]any{
			"creditType":                  emptyStringAsNil(creditType),
			"creditAmount":                creditAmount,
			"minimumCreditAmountForUsage": minimum,
		})
	}
	return credits
}

func normalizeAntigravityCreditValue(value any) any {
	if number, ok := floatFromAny(value); ok {
		return number
	}
	if text := stringFromProviderValue(value); text != "" {
		return text
	}
	return nil
}

func ParseAntigravityQuotaPayload(body string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, err
	}
	if len(anySlice(payload["groups"])) > 0 {
		return payload, nil
	}
	bodyValue, ok := payload["body"]
	if !ok {
		return payload, nil
	}
	switch value := bodyValue.(type) {
	case string:
		var nested map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &nested); err != nil {
			return payload, nil
		}
		return nested, nil
	case map[string]any:
		return value, nil
	default:
		return payload, nil
	}
}

func buildAntigravitySummaryGroups(payload map[string]any) []map[string]any {
	rawGroups := anySlice(payload["groups"])
	if len(rawGroups) == 0 {
		return nil
	}
	groups := make([]map[string]any, 0, len(rawGroups))
	for groupIndex, rawGroup := range rawGroups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		label := firstNonEmptyStringValue(stringFromProviderValue(firstAny(group, "displayName", "display_name")), fmt.Sprintf("Quota Group %d", groupIndex+1))
		description := firstNonEmptyStringValue(stringFromProviderValue(group["description"]))
		groupID := canonicalAntigravityGroupID(label, description)
		if groupID == "" {
			groupID = fmt.Sprintf("quota-group-%d", groupIndex+1)
		}
		rawBuckets := anySlice(group["buckets"])
		buckets := make([]map[string]any, 0, len(rawBuckets))
		for bucketIndex, rawBucket := range rawBuckets {
			bucket, ok := rawBucket.(map[string]any)
			if !ok {
				continue
			}
			remaining, ok := floatFromAny(firstAny(bucket, "remainingFraction", "remaining_fraction"))
			if !ok {
				continue
			}
			window := firstNonEmptyStringValue(stringFromProviderValue(bucket["window"]))
			fallbackID := fmt.Sprintf("%s-bucket-%d", groupID, bucketIndex+1)
			if window != "" {
				fallbackID = groupID + "-" + normalizeWindowID(window)
			}
			bucketID := firstNonEmptyStringValue(stringFromProviderValue(firstAny(bucket, "bucketId", "bucket_id")), fallbackID)
			bucketLabel := firstNonEmptyStringValue(stringFromProviderValue(firstAny(bucket, "displayName", "display_name")), bucketID)
			parsed := map[string]any{
				"id":                bucketID,
				"label":             bucketLabel,
				"remainingFraction": normalizeFraction(remaining),
				"resetAtMs":         nil,
				"periodHours":       nil,
			}
			if window != "" {
				parsed["window"] = window
				if periodHours, ok := antigravityPeriodHours(window); ok {
					parsed["periodHours"] = periodHours
				}
			}
			if resetTime := firstNonEmptyStringValue(stringFromProviderValue(firstAny(bucket, "resetTime", "reset_time"))); resetTime != "" {
				parsed["resetTime"] = resetTime
				if resetAtMS, ok := quotaResetAtMS(resetTime); ok {
					parsed["resetAtMs"] = resetAtMS
				}
			}
			if description := firstNonEmptyStringValue(stringFromProviderValue(bucket["description"])); description != "" {
				parsed["description"] = description
			}
			buckets = append(buckets, parsed)
		}
		if len(buckets) == 0 {
			continue
		}
		sort.SliceStable(buckets, func(i, j int) bool {
			leftOrder := antigravityBucketWindowOrder(stringFromProviderValue(buckets[i]["window"]))
			rightOrder := antigravityBucketWindowOrder(stringFromProviderValue(buckets[j]["window"]))
			if leftOrder != rightOrder {
				return leftOrder < rightOrder
			}
			return stringFromProviderValue(buckets[i]["label"]) < stringFromProviderValue(buckets[j]["label"])
		})
		parsedGroup := map[string]any{"id": groupID, "label": label, "buckets": buckets}
		if description != "" {
			parsedGroup["description"] = description
		}
		groups = append(groups, parsedGroup)
	}
	return groups
}

func canonicalAntigravityGroupID(label string, description string) string {
	normalizedLabel := normalizeWindowID(label)
	normalizedDescription := normalizeWindowID(description)
	combined := normalizedLabel + "-" + normalizedDescription
	switch {
	case strings.Contains(combined, "claude") && (strings.Contains(combined, "gpt") || strings.Contains(combined, "gpt-oss") || strings.Contains(combined, "openai")):
		return "claude-gpt"
	case strings.Contains(combined, "gemini"):
		return "gemini"
	default:
		return normalizedLabel
	}
}

func antigravityBucketWindowOrder(window string) int {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "weekly", "week":
		return 0
	case "5h", "five-hour", "five_hour":
		return 1
	default:
		return math.MaxInt
	}
}

func minRemainingFractionFromBuckets(buckets []map[string]any) *float64 {
	values := make([]float64, 0, len(buckets))
	for _, bucket := range buckets {
		if remaining, ok := floatFromAny(bucket["remainingFraction"]); ok {
			values = append(values, normalizeFraction(remaining))
		}
	}
	if len(values) == 0 {
		return nil
	}
	minValue := values[0]
	for _, value := range values[1:] {
		if value < minValue {
			minValue = value
		}
	}
	return &minValue
}

func anyMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func AntigravityUsedPercent(groups []map[string]any, mode AntigravityQuotaMode) *float64 {
	if mode == AntigravityQuotaModeMaxUsed {
		return antigravityMaxUsedPercent(groups)
	}
	if used := antigravityClaudeGptUsedPercent(groups); used != nil {
		return used
	}
	return antigravityMaxUsedPercent(groups)
}

func antigravityMaxUsedPercent(groups []map[string]any) *float64 {
	values := make([]float64, 0, len(groups))
	for _, group := range groups {
		if used := antigravityGroupUsedPercent(group); used != nil {
			values = append(values, *used)
		}
	}
	return maxFloatPtr(values)
}

func antigravityGroupUsedPercent(group map[string]any) *float64 {
	remaining, ok := antigravityGroupRemainingFraction(group)
	if !ok {
		return nil
	}
	used := math.Max(0, math.Min(100, (1-normalizeFraction(remaining))*100))
	return &used
}

func AntigravityGroupUsedPercent(group map[string]any) *float64 {
	return antigravityGroupUsedPercent(group)
}

func antigravityGroupRemainingFraction(group map[string]any) (float64, bool) {
	if remaining := minRemainingFractionFromBuckets(anyMapSlice(group["buckets"])); remaining != nil {
		return *remaining, true
	}
	return 0, false
}

func antigravityClaudeGptUsedPercent(groups []map[string]any) *float64 {
	for _, group := range groups {
		if !isAntigravityClaudeGptGroup(group) {
			continue
		}
		return antigravityGroupUsedPercent(group)
	}
	return nil
}

func isAntigravityClaudeGptGroup(group map[string]any) bool {
	id := normalizeWindowID(stringFromProviderValue(group["id"]))
	label := normalizeWindowID(stringFromProviderValue(group["label"]))
	if id == "claude-gpt" || label == "claude-gpt" {
		return true
	}
	combined := id + "-" + label
	return strings.Contains(combined, "claude") && (strings.Contains(combined, "gpt") || strings.Contains(combined, "openai"))
}

func BuildClaudeWindows(body string) ([]map[string]any, any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	defs := []struct{ Key, ID, LabelKey string }{
		{"five_hour", "five-hour", "claude_quota.five_hour"},
		{"seven_day", "seven-day", "claude_quota.seven_day"},
		{"seven_day_oauth_apps", "seven-day-oauth-apps", "claude_quota.seven_day_oauth_apps"},
		{"seven_day_opus", "seven-day-opus", "claude_quota.seven_day_opus"},
		{"seven_day_sonnet", "seven-day-sonnet", "claude_quota.seven_day_sonnet"},
		{"seven_day_cowork", "seven-day-cowork", "claude_quota.seven_day_cowork"},
		{"iguana_necktie", "iguana-necktie", "claude_quota.iguana_necktie"},
	}
	windows := make([]map[string]any, 0)
	for _, def := range defs {
		window, ok := payload[def.Key].(map[string]any)
		if !ok {
			continue
		}
		used, ok := floatFromAny(window["utilization"])
		if !ok {
			continue
		}
		resetLabel := stringFromProviderValue(window["resets_at"])
		var resetAtMS any
		if parsed, ok := quotaResetAtMS(window["resets_at"]); ok {
			resetAtMS = parsed
		}
		periodHours := float64(24 * 7)
		if def.Key == "five_hour" {
			periodHours = 5
		}
		windows = append(windows, map[string]any{
			"id":          def.ID,
			"label":       def.LabelKey,
			"labelKey":    def.LabelKey,
			"usedPercent": used,
			"resetLabel":  resetLabel,
			"resetAtMs":   resetAtMS,
			"periodHours": periodHours,
		})
	}
	return windows, payload["extra_usage"], nil
}

func ResolveClaudePlan(body string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	if account, ok := payload["account"].(map[string]any); ok {
		hasMax, hasMaxOK := boolValue(account["has_claude_max"])
		if hasMax {
			return "plan_max"
		}
		hasPro, hasProOK := boolValue(account["has_claude_pro"])
		if hasPro {
			return "plan_pro"
		}
		if hasMaxOK && hasProOK && !hasMax && !hasPro {
			return "plan_free"
		}
	}
	if org, ok := payload["organization"].(map[string]any); ok {
		if strings.EqualFold(stringFromProviderValue(org["organization_type"]), "claude_team") && strings.EqualFold(stringFromProviderValue(org["subscription_status"]), "active") {
			return "plan_team"
		}
	}
	return ""
}

func BuildCodexWindows(body string) (map[string]any, []map[string]any, *float64) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, nil
	}
	windows := make([]map[string]any, 0)
	rateLimit, _ := firstAny(payload, "rate_limit", "rateLimit").(map[string]any)
	codeReviewLimit, _ := firstAny(payload, "code_review_rate_limit", "codeReviewRateLimit").(map[string]any)
	now := time.Now()

	addCodexWindow := func(id string, labelKey string, labelParams map[string]any, window map[string]any, limitReached any, allowed any) {
		if window == nil {
			return
		}
		used, hasUsed := floatFromAny(firstAny(window, "used_percent", "usedPercent"))
		if !hasUsed {
			if (boolFromAny(limitReached) || allowed == false) && codexResetLabel(window) != "-" {
				used = 100
				hasUsed = true
			}
		}
		var usedValue any
		if hasUsed {
			usedValue = used
		} else {
			usedValue = nil
		}
		var resetAtMS any
		if parsed, ok := quotaResetAtMS(firstAny(window, "reset_at", "resetAt")); ok {
			resetAtMS = parsed
		} else if parsed, ok := quotaResetAfterMS(firstAny(window, "reset_after_seconds", "resetAfterSeconds"), now); ok {
			resetAtMS = parsed
		}
		var periodHours any
		if parsed, ok := quotaPeriodHoursFromSeconds(firstAny(window, "limit_window_seconds", "limitWindowSeconds")); ok {
			periodHours = parsed
		}
		item := map[string]any{
			"id":          id,
			"label":       labelKey,
			"labelKey":    labelKey,
			"usedPercent": usedValue,
			"resetLabel":  codexResetLabel(window),
			"resetAtMs":   resetAtMS,
			"periodHours": periodHours,
		}
		if labelParams != nil {
			item["labelParams"] = labelParams
		}
		windows = append(windows, item)
	}

	fiveHour, weekly := codexClassifiedWindows(rateLimit, true)
	addCodexWindow("five-hour", "codex_quota.primary_window", nil, fiveHour, firstAny(rateLimit, "limit_reached", "limitReached"), rateLimit["allowed"])
	secondaryID, secondaryLabel := codexSecondaryWindowMeta(weekly, "weekly", "codex_quota.secondary_window", "monthly", "codex_quota.team_secondary_window")
	addCodexWindow(secondaryID, secondaryLabel, nil, weekly, firstAny(rateLimit, "limit_reached", "limitReached"), rateLimit["allowed"])

	codeReviewFiveHour, codeReviewWeekly := codexClassifiedWindows(codeReviewLimit, true)
	addCodexWindow("code-review-five-hour", "codex_quota.code_review_primary_window", nil, codeReviewFiveHour, firstAny(codeReviewLimit, "limit_reached", "limitReached"), codeReviewLimit["allowed"])
	codeReviewSecondaryID, codeReviewSecondaryLabel := codexSecondaryWindowMeta(codeReviewWeekly, "code-review-weekly", "codex_quota.code_review_secondary_window", "code-review-monthly", "codex_quota.code_review_team_secondary_window")
	addCodexWindow(codeReviewSecondaryID, codeReviewSecondaryLabel, nil, codeReviewWeekly, firstAny(codeReviewLimit, "limit_reached", "limitReached"), codeReviewLimit["allowed"])

	for index, raw := range anySlice(firstAny(payload, "additional_rate_limits", "additionalRateLimits")) {
		limitItem, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rateInfo, _ := firstAny(limitItem, "rate_limit", "rateLimit").(map[string]any)
		if rateInfo == nil {
			continue
		}
		limitName := firstNonEmptyStringValue(stringFromProviderValue(firstAny(limitItem, "limit_name", "limitName")), stringFromProviderValue(firstAny(limitItem, "metered_feature", "meteredFeature")), fmt.Sprintf("additional-%d", index+1))
		idPrefix := normalizeWindowID(limitName)
		if idPrefix == "" {
			idPrefix = fmt.Sprintf("additional-%d", index+1)
		}
		additionalFiveHour, additionalLong := codexClassifiedWindows(rateInfo, true)
		params := map[string]any{"name": limitName}
		addCodexWindow(fmt.Sprintf("%s-five-hour-%d", idPrefix, index), "codex_quota.additional_primary_window", params, additionalFiveHour, firstAny(rateInfo, "limit_reached", "limitReached"), rateInfo["allowed"])
		additionalSecondaryID, additionalSecondaryLabel := codexSecondaryWindowMeta(additionalLong, "weekly", "codex_quota.additional_secondary_window", "monthly", "codex_quota.additional_team_secondary_window")
		addCodexWindow(fmt.Sprintf("%s-%s-%d", idPrefix, additionalSecondaryID, index), additionalSecondaryLabel, params, additionalLong, firstAny(rateInfo, "limit_reached", "limitReached"), rateInfo["allowed"])
	}

	used := MaxUsedPercentFromWindows(windows)
	return payload, windows, used
}

func codexClassifiedWindows(limitInfo map[string]any, allowOrderFallback bool) (map[string]any, map[string]any) {
	if limitInfo == nil {
		return nil, nil
	}
	primary, _ := firstAny(limitInfo, "primary_window", "primaryWindow").(map[string]any)
	secondary, _ := firstAny(limitInfo, "secondary_window", "secondaryWindow").(map[string]any)
	var fiveHour map[string]any
	var weekly map[string]any
	fiveHourSlot := -1
	weeklySlot := -1
	for slot, window := range []map[string]any{primary, secondary} {
		seconds, ok := floatFromAny(firstAny(window, "limit_window_seconds", "limitWindowSeconds"))
		if !ok {
			continue
		}
		if int(seconds) == 18000 && fiveHour == nil {
			fiveHour = window
			fiveHourSlot = slot
		} else if (int(seconds) == 604800 || isCodexMonthlyWindow(window)) && weekly == nil {
			weekly = window
			weeklySlot = slot
		}
	}
	if allowOrderFallback {
		if fiveHour == nil && primary != nil && weeklySlot != 0 {
			fiveHour = primary
			fiveHourSlot = 0
		}
		if weekly == nil && secondary != nil && fiveHourSlot != 1 {
			weekly = secondary
			weeklySlot = 1
		}
	}
	return fiveHour, weekly
}

func isCodexMonthlyWindow(window map[string]any) bool {
	if window == nil {
		return false
	}
	seconds, ok := floatFromAny(firstAny(window, "limit_window_seconds", "limitWindowSeconds"))
	if !ok {
		return false
	}
	return seconds >= 28*24*60*60 && seconds <= 31*24*60*60
}

func codexSecondaryWindowMeta(window map[string]any, weeklyID string, weeklyLabelKey string, monthlyID string, monthlyLabelKey string) (string, string) {
	if isCodexMonthlyWindow(window) {
		return monthlyID, monthlyLabelKey
	}
	return weeklyID, weeklyLabelKey
}

func codexResetLabel(window map[string]any) string {
	if window == nil {
		return "-"
	}
	if resetAt, ok := floatFromAny(firstAny(window, "reset_at", "resetAt")); ok && resetAt > 0 {
		return formatUnixSeconds(int64(resetAt))
	}
	if resetAfter, ok := floatFromAny(firstAny(window, "reset_after_seconds", "resetAfterSeconds")); ok && resetAfter > 0 {
		return formatUnixSeconds(time.Now().Unix() + int64(resetAfter))
	}
	return "-"
}

func formatUnixSeconds(seconds int64) string {
	return time.Unix(seconds, 0).Format("01/02, 15:04")
}

func normalizeWindowID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func BuildKimiRows(body string) ([]map[string]any, *float64, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	rows := make([]map[string]any, 0)
	now := time.Now()
	if usage, ok := payload["usage"].(map[string]any); ok {
		if row := toKimiUsageRow(usage, map[string]any{"labelKey": "kimi_quota.weekly_limit"}, 0, nil, now); row != nil {
			row["id"] = "summary"
			rows = append(rows, row)
		}
	}
	for i, raw := range anySlice(payload["limits"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		detail := firstMap(item, "detail")
		if detail == nil {
			detail = item
		}
		window := firstMap(item, "window")
		if window == nil {
			window = map[string]any{}
		}
		duration, _ := firstInt(window, item, detail, "duration")
		timeUnit := firstAnyFromMaps([]map[string]any{window, item, detail}, "timeUnit")
		if row := toKimiUsageRow(detail, kimiLimitLabel(item, detail, window, i), duration, timeUnit, now); row != nil {
			row["id"] = "limit-" + strconv.Itoa(i)
			rows = append(rows, row)
		}
	}
	usedValues := make([]float64, 0, len(rows))
	for _, row := range rows {
		used, okUsed := floatFromAny(row["used"])
		limit, okLimit := floatFromAny(row["limit"])
		if okUsed && okLimit && limit > 0 {
			usedValues = append(usedValues, math.Max(0, math.Min(100, (used/limit)*100)))
		}
	}
	return rows, maxFloatPtr(usedValues), nil
}

func kimiLimitLabel(item map[string]any, detail map[string]any, window map[string]any, index int) map[string]any {
	for _, key := range []string{"name", "title", "scope"} {
		if value := firstNonEmptyStringValue(stringFromProviderValue(item[key]), stringFromProviderValue(detail[key])); value != "" {
			return map[string]any{"label": value}
		}
	}
	duration, ok := firstInt(window, item, detail, "duration")
	if ok && duration > 0 {
		return map[string]any{"labelKey": "kimi_quota.limit_window", "labelParams": map[string]any{"duration": kimiDurationToken(duration, firstAnyFromMaps([]map[string]any{window, item, detail}, "timeUnit"))}}
	}
	return map[string]any{"labelKey": "kimi_quota.limit_index", "labelParams": map[string]any{"index": index + 1}}
}

func toKimiUsageRow(data map[string]any, fallbackLabel map[string]any, duration int, timeUnit any, now time.Time) map[string]any {
	limit, okLimit := intFromAny(data["limit"])
	used, okUsed := intFromAny(data["used"])
	if !okUsed {
		if remaining, okRemaining := intFromAny(data["remaining"]); okRemaining && okLimit {
			used = limit - remaining
			okUsed = true
		}
	}
	if !okLimit && !okUsed {
		return nil
	}
	row := make(map[string]any, len(fallbackLabel)+4)
	for key, value := range fallbackLabel {
		row[key] = value
	}
	if label := firstNonEmptyStringValue(stringFromProviderValue(data["name"]), stringFromProviderValue(data["title"])); label != "" {
		row["label"] = label
		delete(row, "labelKey")
		delete(row, "labelParams")
	}
	if okUsed {
		row["used"] = used
	} else {
		row["used"] = 0
	}
	if okLimit {
		row["limit"] = limit
	} else {
		row["limit"] = 0
	}
	row["resetHint"] = emptyStringAsNil(kimiResetHint(data))
	row["resetAtMs"] = nil
	if resetAtMS, ok := kimiResetAtMS(data, now); ok {
		row["resetAtMs"] = resetAtMS
	}
	row["periodHours"] = nil
	label := firstNonEmptyStringValue(stringFromProviderValue(row["label"]), stringFromProviderValue(row["labelKey"]))
	if periodHours, ok := kimiPeriodHours(label, duration, timeUnit); ok {
		row["periodHours"] = periodHours
	}
	return row
}

func kimiResetAtMS(data map[string]any, now time.Time) (int64, bool) {
	if resetAtMS, ok := quotaResetAtMS(data["reset_at"], data["resetAt"], data["reset_time"], data["resetTime"]); ok {
		return resetAtMS, true
	}
	for _, key := range []string{"reset_in", "resetIn", "ttl"} {
		if resetAtMS, ok := quotaResetAfterMS(data[key], now); ok {
			return resetAtMS, true
		}
	}
	return 0, false
}

func kimiPeriodHours(label string, duration int, rawTimeUnit any) (float64, bool) {
	if duration > 0 {
		switch strings.ToUpper(strings.TrimSpace(stringFromProviderValue(rawTimeUnit))) {
		case "SECONDS", "SECOND":
			return float64(duration) / 3600, true
		case "HOURS", "HOUR":
			return float64(duration), true
		case "DAYS", "DAY":
			return float64(duration * 24), true
		case "WEEKS", "WEEK":
			return float64(duration * 7 * 24), true
		default:
			return float64(duration) / 60, true
		}
	}
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "daily"), strings.Contains(lower, "day"):
		return 24, true
	case strings.Contains(lower, "weekly"), strings.Contains(lower, "week"):
		return 24 * 7, true
	case strings.Contains(lower, "monthly"), strings.Contains(lower, "month"):
		return 24 * 30, true
	case strings.Contains(lower, "5h"), strings.Contains(lower, "hour"):
		return 5, true
	default:
		return 0, false
	}
}

func firstAnyFromMaps(sources []map[string]any, key string) any {
	for _, source := range sources {
		if source == nil {
			continue
		}
		if value, ok := source[key]; ok {
			return value
		}
	}
	return nil
}

func firstInt(a map[string]any, b map[string]any, c map[string]any, key string) (int, bool) {
	for _, source := range []map[string]any{a, b, c} {
		if source == nil {
			continue
		}
		if value, ok := intFromAny(source[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func intFromAny(value any) (int, bool) {
	parsed, ok := floatFromAny(value)
	if !ok {
		return 0, false
	}
	return int(parsed), true
}

func kimiDurationToken(duration int, rawTimeUnit any) string {
	unit := strings.ToUpper(strings.TrimSpace(stringFromProviderValue(rawTimeUnit)))
	switch unit {
	case "MINUTES":
		if duration%60 == 0 {
			return fmt.Sprintf("%dh", duration/60)
		}
		return fmt.Sprintf("%dm", duration)
	case "HOURS":
		return fmt.Sprintf("%dh", duration)
	case "DAYS":
		return fmt.Sprintf("%dd", duration)
	default:
		return fmt.Sprintf("%ds", duration)
	}
}

func kimiResetHint(data map[string]any) string {
	for _, key := range []string{"reset_at", "resetAt", "reset_time", "resetTime"} {
		raw := stringFromProviderValue(data[key])
		if raw == "" {
			continue
		}
		truncated := regexpMustCompile(`(\.\d{6})\d+`).ReplaceAllString(raw, "$1")
		date, err := time.Parse(time.RFC3339Nano, truncated)
		if err != nil {
			continue
		}
		return kimiDurationHint(time.Until(date))
	}
	for _, key := range []string{"reset_in", "resetIn", "ttl"} {
		seconds, ok := intFromAny(data[key])
		if ok && seconds > 0 {
			return kimiDurationHint(time.Duration(seconds) * time.Second)
		}
	}
	return ""
}

func kimiDurationHint(delta time.Duration) string {
	if delta <= 0 {
		return ""
	}
	totalMinutes := int(delta / time.Minute)
	hours := totalMinutes / 60
	minutes := totalMinutes % 60
	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return "<1m"
}

func regexpMustCompile(expr string) *regexp.Regexp {
	return regexp.MustCompile(expr)
}

func MaxUsedPercentFromWindows(windows []map[string]any) *float64 {
	values := make([]float64, 0, len(windows))
	for _, window := range windows {
		used, ok := floatFromAny(window["usedPercent"])
		if !ok {
			continue
		}
		values = append(values, used)
	}
	return maxFloatPtr(values)
}

func maxFloatPtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return &maxValue
}

func firstAny(data map[string]any, keys ...string) any {
	if data == nil {
		return nil
	}
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func firstMap(data map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := data[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func nestedMap(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}

func nestedString(data map[string]any, key string, child string) string {
	if child == "" {
		return stringFromProviderValue(data[key])
	}
	return stringFromProviderValue(nestedMap(data, key)[child])
}

func firstNonEmptyStringValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func anySlice(value any) []any {
	switch items := value.(type) {
	case []any:
		return items
	case []map[string]any:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func floatFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(v))
		if trimmed == "true" || trimmed == "1" || trimmed == "yes" || trimmed == "y" || trimmed == "on" {
			return true, true
		}
		if trimmed == "false" || trimmed == "0" || trimmed == "no" || trimmed == "n" || trimmed == "off" {
			return false, true
		}
	case float64:
		return v != 0, true
	case int:
		return v != 0, true
	}
	return false, false
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	case float64:
		return v != 0
	default:
		return false
	}
}

func normalizeFraction(value float64) float64 {
	if value > 1 && value <= 100 {
		value = value / 100
	}
	return math.Max(0, math.Min(1, value))
}

// WithQuotaWindows derives recovery only from windows that actually block this
// decision. An unknown blocking reset leaves recovery to bounded probing.
func WithQuotaWindows(decision Decision, windows []map[string]any, threshold float64) Decision {
	decision.QuotaKnown = decision.UsedPercent != nil
	for _, window := range windows {
		used, ok := windowUsedPercent(window)
		if !ok || used < threshold {
			continue
		}
		reset, ok := floatFromAny(window["resetAtMs"])
		if !ok || reset <= float64(time.Now().UnixMilli()) {
			decision.QuotaResetAt = 0
			return decision
		}
		if int64(reset) > decision.QuotaResetAt {
			decision.QuotaResetAt = int64(reset)
		}
	}
	return decision
}

func AntigravityBlockingWindows(groups []map[string]any, mode AntigravityQuotaMode) []map[string]any {
	if mode == AntigravityQuotaModeClaudeGPT {
		for _, group := range groups {
			if isAntigravityClaudeGptGroup(group) {
				return anyMapSlice(group["buckets"])
			}
		}
	}
	var windows []map[string]any
	for _, group := range groups {
		windows = append(windows, anyMapSlice(group["buckets"])...)
	}
	return windows
}

// Only provider-defined, unambiguous model windows narrow the routing scope.
func ClaudeQuotaModel(windows []map[string]any, threshold float64) string {
	return QuotaModelScope("claude", windows, threshold)
}

func QuotaModelScope(provider string, windows []map[string]any, threshold float64) string {
	exhausted := exhaustedQuotaWindows(windows, threshold)
	if len(exhausted) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return claudeQuotaModelFromWindows(exhausted)
	case "codex":
		return codexQuotaModelFromWindows(exhausted)
	case "kimi":
		return kimiQuotaModelFromWindows(exhausted)
	default:
		return ""
	}
}

func AntigravityQuotaModel(groups []map[string]any, mode AntigravityQuotaMode, threshold float64) string {
	var patterns []string
	seen := map[string]struct{}{}
	for _, group := range groups {
		if mode == AntigravityQuotaModeClaudeGPT && !isAntigravityClaudeGptGroup(group) {
			continue
		}
		if len(exhaustedQuotaWindows(anyMapSlice(group["buckets"]), threshold)) == 0 {
			continue
		}
		id := normalizeWindowID(stringFromProviderValue(group["id"]))
		if id == "" {
			id = canonicalAntigravityGroupID(stringFromProviderValue(group["label"]), stringFromProviderValue(group["description"]))
		}
		pattern := antigravityGroupQuotaPattern(id)
		if pattern == "" {
			return ""
		}
		if _, ok := seen[pattern]; ok {
			continue
		}
		seen[pattern] = struct{}{}
		patterns = append(patterns, pattern)
	}
	return strings.Join(patterns, ",")
}

func antigravityGroupQuotaPattern(id string) string {
	switch normalizeWindowID(id) {
	case "claude-gpt":
		return "claude-*,gpt-*"
	case "gemini":
		return "gemini-*"
	default:
		return ""
	}
}

func exhaustedQuotaWindows(windows []map[string]any, threshold float64) []map[string]any {
	out := make([]map[string]any, 0)
	for _, window := range windows {
		used, ok := windowUsedPercent(window)
		if !ok || used < threshold {
			continue
		}
		out = append(out, window)
	}
	return out
}

func windowUsedPercent(window map[string]any) (float64, bool) {
	used, ok := floatFromAny(window["usedPercent"])
	if ok {
		return used, true
	}
	if remaining, found := floatFromAny(window["remainingFraction"]); found {
		return (1 - remaining) * 100, true
	}
	limit, found := floatFromAny(window["limit"])
	if !found || limit <= 0 {
		return 0, false
	}
	value, found := floatFromAny(window["used"])
	if !found {
		return 0, false
	}
	return value / limit * 100, true
}

func claudeQuotaModelFromWindows(windows []map[string]any) string {
	var scopes []string
	for _, window := range windows {
		switch stringFromProviderValue(window["id"]) {
		case "seven-day-opus":
			scopes = append(scopes, "claude-opus-*")
		case "seven-day-sonnet":
			scopes = append(scopes, "claude-sonnet-*")
		default:
			return ""
		}
	}
	return uniqueJoinedPatterns(scopes)
}

func codexQuotaModelFromWindows(windows []map[string]any) string {
	var scopes []string
	for _, window := range windows {
		pattern := codexWindowQuotaPattern(window)
		if pattern == "" {
			return ""
		}
		scopes = append(scopes, pattern)
	}
	return uniqueJoinedPatterns(scopes)
}

func kimiQuotaModelFromWindows(windows []map[string]any) string {
	var scopes []string
	for _, window := range windows {
		pattern := modelPatternFromLabel(firstNonEmptyStringValue(
			stringFromProviderValue(window["label"]),
			stringFromProviderValue(window["name"]),
			stringFromProviderValue(window["id"]),
		))
		if pattern == "" {
			return ""
		}
		scopes = append(scopes, pattern)
	}
	return uniqueJoinedPatterns(scopes)
}

func codexWindowQuotaPattern(window map[string]any) string {
	id := stringFromProviderValue(window["id"])
	if isCredentialQuotaWindowID(id) {
		return ""
	}
	params, _ := window["labelParams"].(map[string]any)
	name := firstNonEmptyStringValue(
		stringFromProviderValue(firstAny(params, "name")),
		strings.TrimSuffix(strings.TrimSuffix(id, "-weekly"), "-monthly"),
	)
	if isCredentialQuotaWindowID(name) {
		return ""
	}
	return modelPatternFromLabel(name)
}

func isCredentialQuotaWindowID(value string) bool {
	id := normalizeWindowID(value)
	switch id {
	case "", "five-hour", "weekly", "monthly", "seven-day", "seven-day-oauth-apps", "seven-day-cowork", "iguana-necktie", "code-review-five-hour", "code-review-weekly", "code-review-monthly":
		return true
	}
	if strings.HasPrefix(id, "code-review-") {
		return true
	}
	if strings.Contains(id, "five-hour") || strings.Contains(id, "primary-window") || strings.Contains(id, "secondary-window") {
		if modelPatternFromLabel(id) == "" {
			return true
		}
	}
	return false
}

func modelPatternFromLabel(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return ""
	}
	normalized := normalizeWindowID(raw)
	if isPeriodQuotaLabel(normalized) || isPeriodQuotaLabel(raw) {
		return ""
	}
	if strings.Contains(normalized, "five-hour") || strings.Contains(normalized, "code-review") {
		return ""
	}
	if !(strings.Contains(raw, "gpt") || strings.Contains(raw, "codex") || strings.Contains(raw, "o1") || strings.Contains(raw, "o3") || strings.Contains(raw, "o4") || strings.Contains(raw, "kimi") || strings.Contains(raw, "moonshot") || strings.Contains(raw, "claude") || strings.Contains(raw, "gemini") || strings.Contains(raw, "grok")) {
		return ""
	}
	name := strings.Trim(raw, " *")
	if name == "" {
		return ""
	}
	if strings.ContainsAny(name, " *?") {
		return name
	}
	return name + "*"
}

func isPeriodQuotaLabel(value string) bool {
	switch normalizeWindowID(value) {
	case "weekly", "daily", "monthly", "5h", "5-hour", "five-hour", "3-hour", "hour", "hours", "day", "days", "week", "weeks", "month", "months":
		return true
	}
	return false
}

func uniqueJoinedPatterns(values []string) string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return strings.Join(out, ",")
}

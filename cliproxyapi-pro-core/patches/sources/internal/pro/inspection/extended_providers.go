package inspection

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

var devinPercentPattern = regexp.MustCompile(`^\d+(?:\.\d+)?$`)
var devinResetPattern = regexp.MustCompile(`^\d+$`)

// BuildDevinQuotaState projects the live Connect-RPC response into the same
// allowlisted fields used by manual quota refresh. It never accepts auth data.
func BuildDevinQuotaState(body string) (map[string]any, []map[string]any, *float64, error) {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil || payload == nil {
		return nil, nil, nil, fmt.Errorf("invalid Devin quota response")
	}
	status := extendedProviderMap(extendedProviderMap(payload, "userStatus"), "planStatus")
	windows := make([]map[string]any, 0, 2)
	decisionWindows := make([]map[string]any, 0, 2)
	var used *float64
	observed := false
	for _, id := range []string{"daily", "weekly"} {
		window := map[string]any{"id": id, "remainingPercent": nil, "resetAtMs": nil, "periodHours": 24}
		if id == "weekly" {
			window["periodHours"] = 168
		}
		decisionWindow := map[string]any{"id": id}
		raw := status[id+"QuotaRemainingPercent"]
		remaining, ok := extendedProviderNumber(raw)
		if text, isString := raw.(string); isString && !devinPercentPattern.MatchString(strings.TrimSpace(text)) {
			ok = false
		}
		if ok && remaining >= 0 && remaining <= 100 {
			window["remainingPercent"] = remaining
			decisionWindow["usedPercent"] = 100 - remaining
			observed = true
			if used == nil || 100-remaining > *used {
				value := 100 - remaining
				used = &value
			}
		}
		reset, ok := extendedProviderNumber(status[id+"QuotaResetAtUnix"])
		if text, isString := status[id+"QuotaResetAtUnix"].(string); isString && !devinResetPattern.MatchString(strings.TrimSpace(text)) {
			ok = false
		}
		if ok && reset > 0 && reset == math.Trunc(reset) && reset <= 8640000000000 {
			window["resetAtMs"] = reset * 1000
			decisionWindow["resetAtMs"] = reset * 1000
			observed = true
		}
		windows = append(windows, window)
		decisionWindows = append(decisionWindows, decisionWindow)
	}
	if !observed {
		return nil, nil, nil, fmt.Errorf("empty Devin quota observation")
	}
	var plan any
	if value, ok := extendedProviderMap(status, "planInfo")["planName"].(string); ok && strings.TrimSpace(value) != "" {
		plan = strings.TrimSpace(value)
	}
	return map[string]any{"windows": windows, "observedAtMs": time.Now().UnixMilli(), "plan": plan, "planStartMs": extendedProviderInstant(status["planStart"]), "planEndMs": extendedProviderInstant(status["planEnd"])}, decisionWindows, used, nil
}

// BuildMetaQuotaState discards api_key, PII and all other source fields. An
// object without subs_usage is a successful observation of unknown quota.
func BuildMetaQuotaState(body string) (map[string]any, []map[string]any, *float64, error) {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil || payload == nil {
		return nil, nil, nil, fmt.Errorf("invalid Meta quota response")
	}
	usage := extendedProviderMap(payload, "subs_usage")
	data := map[string]any{}
	plan, _ := payload["subs_tier_name"].(string)
	if strings.TrimSpace(plan) == "" {
		plan, _ = usage["tier"].(string)
	}
	if plan = strings.TrimSpace(plan); plan != "" {
		data["planName"] = plan
	}
	if active, ok := payload["is_subs_active"].(bool); ok {
		data["isSubscriptionActive"] = active
	}
	windows := make([]map[string]any, 0, 2)
	decisionWindows := make([]map[string]any, 0, 2)
	var used *float64
	for _, id := range []string{"window", "weekly"} {
		raw := extendedProviderMap(usage, id)
		window := map[string]any{"id": id, "usedPercent": nil}
		decisionWindow := map[string]any{"id": id}
		if percent, ok := extendedProviderNumber(raw["used_percent"]); ok {
			percent = math.Min(100, math.Max(0, percent))
			window["usedPercent"] = percent
			decisionWindow["usedPercent"] = percent
			if used == nil || percent > *used {
				value := percent
				used = &value
			}
		}
		if reset, ok := extendedProviderNumber(raw["resets_at"]); ok && reset > 0 && reset <= 8640000000000 {
			window["resetAt"] = reset
			decisionWindow["resetAtMs"] = reset * 1000
		}
		if id == "window" {
			if duration, ok := extendedProviderNumber(raw["window_duration_mins"]); ok && duration > 0 {
				window["durationMinutes"] = duration
			}
		}
		windows = append(windows, window)
		decisionWindows = append(decisionWindows, decisionWindow)
	}
	data["windows"] = windows
	return map[string]any{"data": data}, decisionWindows, used, nil
}

func extendedProviderMap(data map[string]any, key string) map[string]any {
	value, _ := data[key].(map[string]any)
	return value
}

func extendedProviderNumber(value any) (float64, bool) {
	parsed, ok := floatFromAny(value)
	return parsed, ok && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func extendedProviderInstant(raw any) any {
	text, ok := raw.(string)
	if !ok {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || parsed.UnixMilli() <= 0 {
		return nil
	}
	return parsed.UnixMilli()
}

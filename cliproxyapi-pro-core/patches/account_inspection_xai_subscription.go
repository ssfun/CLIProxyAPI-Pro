package management

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// Subscription is display metadata. Failures here cannot supply quota or auth
// evidence; omit unavailable fields so MergeXAIState retains the previous plan.
func (s *accountInspectionScheduler) enrichXAIInspectionSubscription(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, billing map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := settings.Timeout
	if timeout <= 0 || timeout > 8000 {
		timeout = 8000
	}
	var user, preferences map[string]any
	var requests sync.WaitGroup
	requests.Add(2)
	headers := xaiRequestHeaders(account.Auth)
	go func() {
		defer requests.Done()
		user = s.fetchXAIInspectionSubscriptionRecord(ctx, account, "https://cli-chat-proxy.grok.com/v1/user?include=subscription", headers, timeout)
	}()
	go func() {
		defer requests.Done()
		preferences = s.fetchXAIInspectionSubscriptionRecord(ctx, account, "https://cli-chat-proxy.grok.com/v1/settings", headers, timeout)
	}()
	requests.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if account.Auth != nil && account.Auth.ID != "" && s.inspectionAuthManager() != nil && !accountInspectionResultMatchesAuth(account.baseResult(), s.h.authByIndex(account.AuthIndex)) {
		return coreauth.ErrInspectionAuthChanged
	}
	tier := xaiInspectionSubscriptionField(user, "subscriptionTier", "subscription_tier")
	display := xaiInspectionSubscriptionField(preferences, "subscription_tier_display", "subscriptionTierDisplay")
	label := display
	if label == "" {
		label = tier
	}
	if label == "" {
		return nil
	}
	// Match Management's resolveXaiSubscriptionPlan normalization, including
	// punctuation removal before recognizing SuperGrok and Heavy tiers.
	key := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(display+" "+tier))
	planTier := "standard"
	if strings.Contains(key, "heavy") {
		planTier = "elite"
	} else if strings.Contains(key, "supergrok") || strings.Contains(key, "premium") {
		planTier = "premium"
	}
	billing["planLabel"], billing["planTier"] = label, planTier
	return nil
}

func (s *accountInspectionScheduler) fetchXAIInspectionSubscriptionRecord(ctx context.Context, account accountInspectionAccount, endpoint string, headers map[string]string, timeout int) map[string]any {
	result, err := s.apiCall(ctx, account.Auth, http.MethodGet, endpoint, headers, "", timeout)
	if err != nil || result.StatusCode < 200 || result.StatusCode >= 300 {
		return nil
	}
	var record map[string]any
	if json.Unmarshal([]byte(result.Body), &record) != nil {
		return nil
	}
	return record
}

func xaiInspectionSubscriptionField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		// The UI accepts strings and finite numbers, never bools or objects.
		switch value := record[key].(type) {
		case string:
			if value := strings.TrimSpace(value); value != "" {
				return value
			}
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	return ""
}

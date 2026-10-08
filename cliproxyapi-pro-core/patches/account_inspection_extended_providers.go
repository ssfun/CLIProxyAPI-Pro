package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func (s *accountInspectionScheduler) inspectDevin(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	token := coreauth.InspectionAccessToken(account.Auth)
	if token == "" {
		token = tokenValueForAuth(account.Auth)
	}
	if strings.TrimSpace(token) == "" {
		return accountInspectionDecision{}, nil, fmt.Errorf("missing Devin authentication token")
	}
	// Marshal credential material rather than interpolating it into JSON.
	body, err := json.Marshal(map[string]any{"metadata": map[string]any{"ideName": "chisel", "ideVersion": "3000.10.21", "apiKey": token, "locale": "en", "os": "darwin", "extensionVersion": "3000.10.21", "clientName": "chisel"}})
	if err != nil {
		return accountInspectionDecision{}, nil, err
	}
	return s.inspectExtendedProviderQuota(ctx, account, settings, "https://server.codeium.com/exa.seat_management_pb.SeatManagementService/GetUserStatus", map[string]string{"Content-Type": "application/json", "Connect-Protocol-Version": "1"}, string(body), proinspection.BuildDevinQuotaState)
}

func (s *accountInspectionScheduler) inspectMeta(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	// The LLM API key cannot authorize Muse key/quota requests. Match manual
	// refresh's strict persisted DCA field, never a generic token fallback.
	token, _ := account.Auth.Metadata["dca_token"].(string)
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "dca:") || len(token) <= 4 || strings.ContainsAny(token, " \t\r\n\v\f") {
		return accountInspectionDecision{}, nil, fmt.Errorf("missing Meta DCA token")
	}
	return s.inspectExtendedProviderQuota(ctx, account, settings, "https://api.meta.ai/muse-code/key", map[string]string{"Accept": "application/json", "Content-Type": "application/json", "Authorization": "Bearer " + token, "x-api-version": "1.0.0"}, "{}", proinspection.BuildMetaQuotaState)
}

type inspectionExtendedQuotaParser func(string) (map[string]any, []map[string]any, *float64, error)

func (s *accountInspectionScheduler) inspectExtendedProviderQuota(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, url string, headers map[string]string, body string, parse inspectionExtendedQuotaParser) (accountInspectionDecision, *int, error) {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, url, headers, body, settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if err := ctx.Err(); err != nil {
		return accountInspectionDecision{}, status, err
	}
	// Meta's endpoint can echo credentials even in error bodies. Neither errors
	// nor persisted state may contain those bodies.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if proinspection.IsAccountErrorStatus(resp.StatusCode) {
			return authErrorDecision(account, resp.StatusCode), status, nil
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return rateLimitedDecision(""), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	values, windows, used, err := parse(resp.Body)
	if err != nil {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "额度数据无法解析，保留账号", QuotaUnknown: true, Error: err.Error()}, status, nil
	}
	if err := ctx.Err(); err != nil {
		return accountInspectionDecision{}, status, err
	}
	if manager := s.inspectionAuthManager(); manager != nil {
		current := s.h.authByIndex(account.AuthIndex)
		if !accountInspectionResultMatchesAuth(account.baseResult(), current) || coreauth.CredentialsChanged(account.Auth, current) {
			return accountInspectionDecision{}, status, coreauth.ErrInspectionAuthChanged
		}
		if account.Provider == "meta" {
			// Upstream's credential-change contract covers the LLM key but not
			// the independent DCA token used to authorize this quota request.
			observedDCA, _ := account.Auth.Metadata["dca_token"].(string)
			currentDCA, _ := current.Metadata["dca_token"].(string)
			if strings.TrimSpace(observedDCA) != strings.TrimSpace(currentDCA) {
				return accountInspectionDecision{}, status, coreauth.ErrInspectionAuthChanged
			}
		}
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(values))
	decision := proinspection.WithQuotaWindows(quotaDecision(account, used, used != nil, settings.UsedPercentThreshold), windows, settings.UsedPercentThreshold)
	return decision, status, nil
}

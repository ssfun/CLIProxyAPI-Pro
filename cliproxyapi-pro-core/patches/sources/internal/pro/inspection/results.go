package inspection

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Result struct {
	AuthID                string   `json:"-"`
	AccessTokenSHA256     string   `json:"-"`
	Key                   string   `json:"key"`
	Provider              string   `json:"provider"`
	FileName              string   `json:"fileName"`
	DisplayName           string   `json:"displayName"`
	Email                 string   `json:"email"`
	Name                  string   `json:"name"`
	AuthIndex             string   `json:"authIndex"`
	Disabled              bool     `json:"disabled"`
	Action                Action   `json:"action"`
	ActionReason          string   `json:"actionReason"`
	StatusCode            *int     `json:"statusCode"`
	UsedPercent           *float64 `json:"usedPercent"`
	IsQuota               bool     `json:"isQuota"`
	Error                 string   `json:"error"`
	ErrorDetail           string   `json:"errorDetail,omitempty"`
	ErrorCode             string   `json:"errorCode"`
	DeepProbeTriggered    bool     `json:"deepProbeTriggered"`
	DeepProbeStatus       string   `json:"deepProbeStatus"`
	DeepProbeError        string   `json:"deepProbeError"`
	TokenRefreshTriggered bool     `json:"tokenRefreshTriggered"`
	TokenRefreshStatus    string   `json:"tokenRefreshStatus"`
	TokenRefreshError     string   `json:"tokenRefreshError"`
	NextRefreshAt         int64    `json:"nextRefreshAt"`
	Executed              bool     `json:"executed"`
	ExecuteError          string   `json:"executeError"`
}

type Summary struct {
	TotalFiles           int `json:"totalFiles"`
	ProbeSetCount        int `json:"probeSetCount"`
	SampledCount         int `json:"sampledCount"`
	DisabledCount        int `json:"disabledCount"`
	EnabledCount         int `json:"enabledCount"`
	DeleteCount          int `json:"deleteCount"`
	DisableCount         int `json:"disableCount"`
	EnableCount          int `json:"enableCount"`
	KeepCount            int `json:"keepCount"`
	ErrorCount           int `json:"errorCount"`
	ExecutedDeleteCount  int `json:"executedDeleteCount"`
	ExecutedDisableCount int `json:"executedDisableCount"`
	ExecutedEnableCount  int `json:"executedEnableCount"`
}

type HealthCounts struct {
	Total           int `json:"total"`
	Healthy         int `json:"healthy"`
	Disabled        int `json:"disabled"`
	AuthInvalid     int `json:"authInvalid"`
	QuotaExhausted  int `json:"quotaExhausted"`
	InspectionError int `json:"inspectionError"`
	Recoverable     int `json:"recoverable"`
}

type PageInfo struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"pageSize"`
	Total      int  `json:"total"`
	TotalPages int  `json:"totalPages"`
	HasMore    bool `json:"hasMore"`
}

type HealthBucket string

const (
	HealthHealthy         HealthBucket = "healthy"
	HealthDisabled        HealthBucket = "disabled"
	HealthAuthInvalid     HealthBucket = "authInvalid"
	HealthQuotaExhausted  HealthBucket = "quotaExhausted"
	HealthInspectionError HealthBucket = "inspectionError"
	HealthRecoverable     HealthBucket = "recoverable"
)

func ResultHealthCounts(results []Result) HealthCounts {
	counts := HealthCounts{Total: len(results)}
	for _, result := range results {
		counts = adjustHealthBucket(counts, HealthBucketOf(result), 1)
	}
	return counts
}

func ResultProviderHealthCounts(results []Result) map[string]HealthCounts {
	counts := make(map[string]HealthCounts)
	for _, result := range results {
		provider := strings.ToLower(strings.TrimSpace(result.Provider))
		if provider == "" {
			provider = "unknown"
		}
		counts[provider] = AdjustHealthCountsForResult(counts[provider], result, 1)
	}
	return counts
}

func AdjustHealthCountsForResult(counts HealthCounts, result Result, delta int) HealthCounts {
	counts.Total += delta
	return adjustHealthBucket(counts, HealthBucketOf(result), delta)
}

func adjustHealthBucket(counts HealthCounts, bucket HealthBucket, delta int) HealthCounts {
	switch bucket {
	case HealthAuthInvalid:
		counts.AuthInvalid += delta
	case HealthInspectionError:
		counts.InspectionError += delta
	case HealthQuotaExhausted:
		counts.QuotaExhausted += delta
	case HealthRecoverable:
		counts.Recoverable += delta
	case HealthDisabled:
		counts.Disabled += delta
	default:
		counts.Healthy += delta
	}
	return counts
}

func HealthBucketOf(result Result) HealthBucket {
	switch {
	case IsQuotaResult(result):
		return HealthQuotaExhausted
	case IsAccountInvalidResult(result):
		return HealthAuthInvalid
	case IsRequestErrorResult(result):
		return HealthInspectionError
	case result.Action == ActionEnable:
		return HealthRecoverable
	case result.Disabled:
		return HealthDisabled
	default:
		return HealthHealthy
	}
}

func IsQuotaResult(result Result) bool {
	if result.IsQuota {
		return true
	}
	message := strings.Join([]string{result.Error, result.ErrorDetail, result.DeepProbeError}, "\n")
	switch strings.ToLower(strings.TrimSpace(result.Provider)) {
	case "antigravity":
		return IsAntigravityQuotaFailure(message)
	case "xai":
		return IsXAIQuotaFailure(message)
	default:
		return false
	}
}

func NormalizeResultSemantics(result Result) Result {
	if !result.IsQuota && IsQuotaResult(result) {
		result.IsQuota = true
		result.ErrorCode = ""
	}
	return result
}

func ResultMatchesFilter(result Result, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	switch filter {
	case "", "all":
		return true
	case "attention", "needs-attention", "needs_attention":
		return HealthBucketOf(result) != HealthHealthy
	case "accountissues", "account-issues", "account_issues":
		bucket := HealthBucketOf(result)
		return bucket == HealthAuthInvalid || bucket == HealthInspectionError
	case "quotachanges", "quota-changes", "quota_changes":
		bucket := HealthBucketOf(result)
		return bucket == HealthQuotaExhausted || bucket == HealthRecoverable
	case "pending":
		return result.Action != ActionKeep && !result.Executed
	case "accountinvalid", "account-invalid", "account_invalid", "authinvalid", "auth-invalid", "auth_invalid":
		return HealthBucketOf(result) == HealthAuthInvalid
	case "requesterror", "request-error", "request_error", "inspectionerror", "inspection-error", "inspection_error":
		return HealthBucketOf(result) == HealthInspectionError
	case "quotaexhausted", "quota-exhausted", "quota_exhausted":
		return HealthBucketOf(result) == HealthQuotaExhausted
	case "recoverable":
		return HealthBucketOf(result) == HealthRecoverable
	case "highavailable", "high-available", "high_available", "healthy":
		return HealthBucketOf(result) == HealthHealthy
	default:
		return true
	}
}

func ResultMatchesProvider(result Result, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return provider == "" || provider == ProviderAll || strings.EqualFold(result.Provider, provider)
}

func ResultMatchesSearch(result Result, search string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	for _, value := range []string{result.Key, result.FileName, result.DisplayName, result.Email, result.Name, result.AuthIndex} {
		if strings.Contains(strings.ToLower(value), search) {
			return true
		}
	}
	return false
}

func ResultPageInfo(total, page, pageSize int) PageInfo {
	page = normalizePage(page)
	if pageSize <= 0 {
		pageSize = 1
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	start := (page - 1) * pageSize
	return PageInfo{Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages, HasMore: start+pageSize < total}
}

func PaginateResults(results []Result, page, pageSize, maxPageSize int, filter string, pendingOnly bool, provider, search string) ([]Result, PageInfo) {
	page = normalizePage(page)
	pageSize = normalizePageSize(pageSize, 100, maxPageSize)
	filtered := make([]Result, 0, len(results))
	for _, result := range results {
		if ResultMatchesFilter(result, filter) &&
			(!pendingOnly || ResultMatchesFilter(result, "pending")) &&
			ResultMatchesProvider(result, provider) &&
			ResultMatchesSearch(result, search) {
			filtered = append(filtered, result)
		}
	}
	total := len(filtered)
	info := ResultPageInfo(total, page, pageSize)
	start := (page - 1) * pageSize
	if start >= total {
		return []Result{}, info
	}
	end := min(total, start+pageSize)
	return append([]Result(nil), filtered[start:end]...), info
}

func SameResult(left, right Result) bool {
	return left.Key == right.Key || (left.FileName == right.FileName && left.AuthIndex == right.AuthIndex)
}

func CompletedResults(results []Result) []Result {
	out := make([]Result, 0, len(results))
	for _, result := range results {
		if result.Key != "" {
			out = append(out, result)
		}
	}
	return out
}

func AutoActionConfirmationKey(result Result, action Action) string {
	key := result.Key
	if key == "" {
		key = result.FileName + ":" + result.AuthIndex
	}
	if key == "" || action == "" {
		return ""
	}
	category := "action"
	switch {
	case IsAccountInvalidResult(result):
		category = "account-invalid"
	case IsRequestErrorResult(result):
		category = "request-error"
	case result.IsQuota:
		category = "quota"
	case action == ActionEnable:
		category = "recovery"
	}
	return key + "|" + string(action) + "|" + category
}

func AutoActionForError(result Result, action Action) Action {
	if action == ActionDelete {
		return ActionDelete
	}
	if action == ActionDisable && !result.Disabled {
		return ActionDisable
	}
	return ActionNone
}

func IsAccountInvalidResult(result Result) bool {
	if IsQuotaResult(result) {
		return false
	}
	status := 0
	if result.StatusCode != nil {
		status = *result.StatusCode
	}
	code := strings.TrimSpace(result.ErrorCode)
	if code != "" {
		return code == "inspection_http_error" && IsAccountErrorStatus(status)
	}
	if result.DeepProbeStatus == string(DeepProbeTransientError) {
		return false
	}
	return IsAccountErrorStatus(status)
}

func IsRequestErrorResult(result Result) bool {
	if IsQuotaResult(result) || IsAccountInvalidResult(result) {
		return false
	}
	return strings.TrimSpace(result.ErrorCode) != "" ||
		result.DeepProbeStatus == string(DeepProbeTransientError) ||
		result.TokenRefreshStatus == "failed" ||
		result.Error != ""
}

func AutoActionForResult(result Result, settings Settings) Action {
	if result.ErrorCode == "inspection_identity_changed" {
		return ActionNone
	}
	if IsAccountInvalidResult(result) {
		return AutoActionForError(result, settings.AutoExecuteAccountInvalidAction)
	}
	if IsRequestErrorResult(result) {
		return AutoActionForError(result, settings.AutoExecuteRequestErrorAction)
	}
	if result.Action == ActionDisable && result.IsQuota && settings.AutoExecuteQuotaLimitDisable {
		return ActionDisable
	}
	if result.Action == ActionEnable && settings.AutoExecuteQuotaRecoveryEnable {
		return ActionEnable
	}
	return ActionNone
}

func SummarizeResults(totalFiles, probeSetCount, disabledCount, enabledCount int, results []Result) Summary {
	summary := Summary{
		TotalFiles: totalFiles, ProbeSetCount: probeSetCount,
		DisabledCount: disabledCount, EnabledCount: enabledCount,
	}
	for _, result := range results {
		summary = AdjustSummaryForResult(summary, result, 1)
	}
	return summary
}

func AdjustSummaryForResult(summary Summary, result Result, delta int) Summary {
	summary.SampledCount += delta
	switch result.Action {
	case ActionDelete:
		summary.DeleteCount += delta
	case ActionDisable:
		summary.DisableCount += delta
	case ActionEnable:
		summary.EnableCount += delta
	default:
		summary.KeepCount += delta
	}
	if result.Error != "" {
		summary.ErrorCount += delta
	}
	if result.Executed {
		switch result.Action {
		case ActionDelete:
			summary.ExecutedDeleteCount += delta
		case ActionDisable:
			summary.ExecutedDisableCount += delta
		case ActionEnable:
			summary.ExecutedEnableCount += delta
		}
	}
	return summary
}

func SortResults(results []Result) []Result {
	sorted := append([]Result(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Provider != sorted[j].Provider {
			return sorted[i].Provider < sorted[j].Provider
		}
		return resultIdentity(sorted[i]) < resultIdentity(sorted[j])
	})
	return sorted
}

func ResultIdentity(result Result) string { return resultIdentity(result) }

func resultIdentity(result Result) string {
	label := ""
	for _, value := range []string{result.Email, result.Name, result.DisplayName} {
		if value = strings.TrimSpace(value); value != "" {
			label = value
			break
		}
	}
	if label != "" && label != "-" {
		if result.FileName != "" {
			return fmt.Sprintf("%s[%s]", label, result.FileName)
		}
		return label
	}
	return result.FileName
}

func IsAccountErrorStatus(status int) bool {
	return status == 400 || status == 401 || status == 403 || status == 404
}

func IsXAIQuotaFailure(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "free-usage-exhausted") ||
		strings.Contains(lower, "quota_exhausted") ||
		strings.Contains(lower, "quota exhausted") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "included free usage") ||
		strings.Contains(lower, "out of credits") ||
		strings.Contains(lower, "grok subscription")
}

func IsAntigravityQuotaFailure(body string) bool {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "quota_exhausted") || strings.Contains(lower, "quota exhausted") || strings.Contains(lower, "limit reached") {
		return true
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return false
	}
	errorPayload, _ := payload["error"].(map[string]any)
	status, _ := errorPayload["status"].(string)
	if !strings.EqualFold(strings.TrimSpace(status), "RESOURCE_EXHAUSTED") {
		return false
	}
	details, _ := errorPayload["details"].([]any)
	for _, raw := range details {
		detail, _ := raw.(map[string]any)
		reason, _ := detail["reason"].(string)
		if strings.EqualFold(strings.TrimSpace(reason), "QUOTA_EXHAUSTED") {
			return true
		}
	}
	return false
}

func normalizePage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func normalizePageSize(size, fallback, maxSize int) int {
	if size <= 0 {
		size = fallback
	}
	if size > maxSize {
		return maxSize
	}
	return size
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

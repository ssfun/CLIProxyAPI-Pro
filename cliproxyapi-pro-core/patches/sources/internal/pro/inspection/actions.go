package inspection

import "strings"

type ActionItem struct {
	AuthID            string   `json:"-"`
	AccessTokenSHA256 string   `json:"-"`
	Key               string   `json:"key"`
	ResultRef         string   `json:"resultRef"`
	Suggested         bool     `json:"suggested"`
	RecommendedAction Action   `json:"-"`
	ObservedSettings  Settings `json:"-"`
	QuotaResetAt      int64    `json:"-"`
	QuotaModel        string   `json:"-"`
	QuotaKnown        bool     `json:"-"`
	QuotaCooling      bool     `json:"-"`
	QuotaRetryAt      int64    `json:"-"`
	QuotaRevision     int64    `json:"-"`
	IsQuota           bool     `json:"-"`
	Provider          string   `json:"provider"`
	FileName          string   `json:"fileName"`
	DisplayName       string   `json:"displayName"`
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	AuthIndex         string   `json:"authIndex"`
	Disabled          bool     `json:"disabled"`
	Action            Action   `json:"action"`
}

type ActionRequest struct {
	Items []ActionItem `json:"items"`
}

type OneRequest struct {
	Item ActionItem `json:"item"`
}

type ManyRequest struct {
	Items []ActionItem `json:"items"`
}

type InspectionOutcome struct {
	Key         string  `json:"key"`
	FileName    string  `json:"fileName"`
	DisplayName string  `json:"displayName"`
	Email       string  `json:"email"`
	Name        string  `json:"name"`
	Provider    string  `json:"provider"`
	AuthIndex   string  `json:"authIndex"`
	Success     bool    `json:"success"`
	Error       string  `json:"error"`
	Result      *Result `json:"result,omitempty"`
}

type RefreshTokenRequest struct {
	Item ActionItem `json:"item"`
}

type ActionOutcome struct {
	Action      Action `json:"action"`
	FileName    string `json:"fileName"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	AuthIndex   string `json:"authIndex"`
	Success     bool   `json:"success"`
	Error       string `json:"error"`
}

type ActionOutcomeSummary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

func AccountKey(fileName, authIndex string) string {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		authIndex = "-"
	}
	return fileName + "::" + authIndex
}

func (item ActionItem) ToResult() Result {
	return Result{
		AuthID:            item.AuthID,
		AccessTokenSHA256: item.AccessTokenSHA256,
		Key:               item.Key,
		ResultRef:         item.ResultRef,
		ObservedSettings:  item.ObservedSettings,
		QuotaResetAt:      item.QuotaResetAt,
		QuotaModel:        item.QuotaModel,
		QuotaKnown:        item.QuotaKnown,
		QuotaCooling:      item.QuotaCooling,
		QuotaRetryAt:      item.QuotaRetryAt,
		QuotaRevision:     item.QuotaRevision,
		IsQuota:           item.IsQuota,
		Provider:          item.Provider,
		FileName:          item.FileName,
		DisplayName:       item.DisplayName,
		Email:             item.Email,
		Name:              item.Name,
		AuthIndex:         item.AuthIndex,
		Disabled:          item.Disabled,
		Action:            item.Action,
	}
}

func ActionItemFromResult(result Result, action Action) ActionItem {
	return ActionItem{
		AuthID:            result.AuthID,
		AccessTokenSHA256: result.AccessTokenSHA256,
		Key:               result.Key,
		ResultRef:         result.ResultRef,
		Suggested:         false,
		RecommendedAction: result.Action,
		ObservedSettings:  result.ObservedSettings,
		QuotaResetAt:      result.QuotaResetAt,
		QuotaModel:        result.QuotaModel,
		QuotaKnown:        result.QuotaKnown,
		QuotaCooling:      result.QuotaCooling,
		QuotaRetryAt:      result.QuotaRetryAt,
		QuotaRevision:     result.QuotaRevision,
		IsQuota:           result.IsQuota,
		Provider:          result.Provider,
		FileName:          result.FileName,
		DisplayName:       result.DisplayName,
		Email:             result.Email,
		Name:              result.Name,
		AuthIndex:         result.AuthIndex,
		Disabled:          result.Disabled,
		Action:            action,
	}
}

func DedupeActionItems(items []ActionItem) []ActionItem {
	seen := make(map[string]struct{})
	out := make([]ActionItem, 0, len(items))
	for _, item := range items {
		if item.Action == ActionNone || item.Action == ActionKeep || item.Action == "" || item.FileName == "" {
			continue
		}
		key := item.Key
		if key == "" {
			key = AccountKey(item.FileName, item.AuthIndex)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if item.Key == "" {
			item.Key = key
		}
		out = append(out, item)
	}
	return out
}

func SummarizeActionOutcomes(outcomes []ActionOutcome) ActionOutcomeSummary {
	summary := ActionOutcomeSummary{Total: len(outcomes)}
	for _, outcome := range outcomes {
		if outcome.Success {
			summary.Success++
		} else {
			summary.Failed++
		}
	}
	return summary
}

func MergeManualActionResult(current, executed Result) (Result, bool) {
	current.AuthID = executed.AuthID
	current.AccessTokenSHA256 = executed.AccessTokenSHA256
	current.Provider = executed.Provider
	current.FileName = executed.FileName
	current.DisplayName = executed.DisplayName
	current.Email = executed.Email
	current.Name = executed.Name
	current.AuthIndex = executed.AuthIndex
	current.Disabled = executed.Disabled
	current.QuotaCooling = executed.QuotaCooling
	current.QuotaRetryAt = executed.QuotaRetryAt
	current.Executed = current.Executed || executed.Executed
	current.OperationAction = executed.Action
	if executed.ExecutedAt > 0 {
		current.ExecutedAction = executed.Action
		current.ExecutedEffect = executed.ExecutedEffect
		current.ExecutedAt = executed.ExecutedAt
		current.ExecutedSuggested = executed.ExecutedSuggested
	}
	current.ExecuteError = executed.ExecuteError
	return current, true
}

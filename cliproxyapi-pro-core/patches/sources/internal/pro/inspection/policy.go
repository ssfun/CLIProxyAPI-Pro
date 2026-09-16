package inspection

import (
	"fmt"
	"strings"
)

type DeepProbeStatus string

const (
	DeepProbeSuccess        DeepProbeStatus = "success"
	DeepProbeQuota          DeepProbeStatus = "quota"
	DeepProbeAuthError      DeepProbeStatus = "auth_error"
	DeepProbeTransientError DeepProbeStatus = "transient_error"
	DeepProbeSkipped        DeepProbeStatus = "skipped"
)

type Action string

const (
	ActionNone    Action = "none"
	ActionKeep    Action = "keep"
	ActionDelete  Action = "delete"
	ActionDisable Action = "disable"
	ActionEnable  Action = "enable"
)

type Decision struct {
	QuotaResetAt    int64
	QuotaModel      string
	QuotaKnown      bool
	Action          Action
	ActionReason    string
	UsedPercent     *float64
	IsQuota         bool
	Error           string
	ErrorDetail     string
	DeepProbeStatus DeepProbeStatus
	DeepProbeError  string
}

// QuotaRecovered applies the same hysteresis at the recovery boundary that is
// used by the inspection scheduler. At very small thresholds, a zero usage
// reading is the only healthy value that can exist, so the boundary is
// inclusive instead of producing an impossible negative comparison.
func QuotaRecovered(used, threshold float64) bool {
	recoveryThreshold := threshold - 2
	if recoveryThreshold <= 0 {
		return used <= 0
	}
	return used < recoveryThreshold
}

func AuthErrorDecision(disabled bool, status int) Decision {
	if disabled {
		return Decision{Action: ActionKeep, ActionReason: fmt.Sprintf("接口返回 %d，但账号已禁用", status)}
	}
	return Decision{Action: ActionDisable, ActionReason: fmt.Sprintf("接口返回 %d，建议禁用账号", status)}
}

func HealthyDecision(disabled bool) Decision {
	if disabled {
		return Decision{Action: ActionEnable, ActionReason: "账号恢复健康，建议重新启用"}
	}
	return Decision{Action: ActionKeep, ActionReason: "无需处理"}
}

func QuotaDecision(disabled bool, used *float64, hasQuotaData bool, threshold float64) Decision {
	over := used != nil && *used >= threshold
	if (over || !hasQuotaData) && disabled {
		reason := "未获取到可判断额度，保留账号"
		if over {
			reason = "额度达到阈值，但账号已禁用"
		}
		return Decision{Action: ActionKeep, ActionReason: reason, UsedPercent: used, IsQuota: over}
	}
	if over {
		return Decision{Action: ActionDisable, ActionReason: "额度达到阈值，建议禁用账号", UsedPercent: used, IsQuota: true}
	}
	if !hasQuotaData {
		return Decision{Action: ActionKeep, ActionReason: "未获取到可判断额度，保留账号", UsedPercent: used}
	}
	if disabled {
		return Decision{Action: ActionEnable, ActionReason: "额度可用，建议重新启用账号", UsedPercent: used}
	}
	return Decision{Action: ActionKeep, ActionReason: "额度可用，无需处理", UsedPercent: used}
}

func QuotaUnavailableDecision(disabled bool, reason, detail string) Decision {
	action := ActionDisable
	if disabled {
		action = ActionKeep
		reason = strings.TrimSuffix(reason, "，建议禁用账号") + "，但账号已禁用"
	}
	return Decision{Action: action, ActionReason: reason, IsQuota: true, ErrorDetail: detail}
}

func CodexDecision(disabled bool, status int, used *float64, isQuota bool, threshold float64) Decision {
	if isQuota || (used != nil && *used >= threshold) {
		if disabled {
			return Decision{Action: ActionKeep, ActionReason: "额度超阈值，但账号已禁用", UsedPercent: used, IsQuota: true}
		}
		return Decision{Action: ActionDisable, ActionReason: "额度超阈值，建议禁用账号", UsedPercent: used, IsQuota: true}
	}
	if status == 401 {
		return Decision{Action: ActionDelete, ActionReason: "接口返回 401，建议删除失效账号", UsedPercent: used}
	}
	if IsAccountErrorStatus(status) {
		return AuthErrorDecision(disabled, status)
	}
	if status == 200 && disabled {
		return Decision{Action: ActionEnable, ActionReason: "账号恢复健康，建议重新启用", UsedPercent: used}
	}
	return Decision{Action: ActionKeep, ActionReason: "无需处理", UsedPercent: used}
}

func ErrorCode(status *int, fallback string) string {
	if status != nil && IsAccountErrorStatus(*status) {
		return "inspection_http_error"
	}
	return fallback
}

func DecisionErrorCode(provider string, decision Decision, status *int) string {
	if decision.IsQuota {
		return ""
	}
	deepProbeErrorCode := func() string {
		if strings.EqualFold(strings.TrimSpace(provider), "xai") {
			return "xai_deep_probe_error"
		}
		return "antigravity_deep_probe_error"
	}
	if decision.DeepProbeStatus == DeepProbeTransientError {
		return deepProbeErrorCode()
	}
	if status != nil && IsAccountErrorStatus(*status) {
		return "inspection_http_error"
	}
	if decision.DeepProbeStatus == DeepProbeAuthError {
		return deepProbeErrorCode()
	}
	if decision.Error != "" {
		return "inspection_probe_error"
	}
	return ""
}

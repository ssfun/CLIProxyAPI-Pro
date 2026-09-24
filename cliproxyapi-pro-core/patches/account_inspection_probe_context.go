package management

import (
	"context"
	"strconv"
	"time"
)

type inspectionProbeTrigger string

const (
	inspectionTriggerScheduled inspectionProbeTrigger = "scheduled"
	inspectionTriggerManual    inspectionProbeTrigger = "manual"
	inspectionTriggerRecovery  inspectionProbeTrigger = "recovery"
)

type inspectionProbeContext struct {
	Trigger  inspectionProbeTrigger
	Previous *accountInspectionResult
	Now      time.Time
}

type inspectionProbeContextKey struct{}

func withInspectionProbeTrigger(ctx context.Context, trigger inspectionProbeTrigger) context.Context {
	probe := inspectionProbeContextFrom(ctx)
	probe.Trigger = trigger
	return context.WithValue(ctx, inspectionProbeContextKey{}, probe)
}

func inspectionProbeContextFrom(ctx context.Context) inspectionProbeContext {
	if ctx != nil {
		if probe, ok := ctx.Value(inspectionProbeContextKey{}).(inspectionProbeContext); ok {
			return probe
		}
	}
	return inspectionProbeContext{Trigger: inspectionTriggerScheduled}
}

func (s *accountInspectionScheduler) withPreviousInspection(ctx context.Context, account accountInspectionAccount) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	probe := inspectionProbeContextFrom(ctx)
	probe.Now = time.Now()
	if s != nil {
		s.mu.Lock()
		previousResults := s.status.Results
		if len(previousResults) == 0 {
			previousResults = s.previousResults
		}
		for i := range previousResults {
			previous := previousResults[i]
			if previous.AuthID != account.AuthID || previous.AuthIndex != account.AuthIndex || previous.Provider != account.Provider {
				continue
			}
			if account.Auth == nil || account.Auth.RegistrationEpoch == 0 || previous.RegistrationEpoch != strconv.FormatUint(account.Auth.RegistrationEpoch, 10) {
				continue
			}
			probe.Previous = &previous
			break
		}
		s.mu.Unlock()
	}
	return context.WithValue(ctx, inspectionProbeContextKey{}, probe)
}

func shouldConfirmInspection(ctx context.Context, decision accountInspectionDecision) bool {
	if decision.IsQuota || decision.Error != "" || decision.Action == accountInspectionActionDisable {
		return false
	}
	if decision.Action == accountInspectionActionEnable || decision.QuotaUnknown {
		return true
	}
	probe := inspectionProbeContextFrom(ctx)
	if probe.Previous != nil && probe.Previous.IsQuota && (decision.QuotaKnown || decision.UsedPercent != nil) {
		return true
	}
	return probe.Trigger == inspectionTriggerManual && decision.Action == accountInspectionActionKeep
}

func shouldConfirmAntigravityInspection(ctx context.Context, decision accountInspectionDecision) bool {
	if decision.IsQuota || decision.Error != "" || decision.Action == accountInspectionActionDisable {
		return false
	}
	quotaRecovered := !decision.QuotaUnknown && (decision.QuotaKnown || decision.UsedPercent != nil)
	if decision.Action == accountInspectionActionEnable {
		return quotaRecovered
	}
	probe := inspectionProbeContextFrom(ctx)
	if probe.Previous != nil && probe.Previous.IsQuota && quotaRecovered {
		return true
	}
	return probe.Trigger == inspectionTriggerManual && decision.Action == accountInspectionActionKeep
}

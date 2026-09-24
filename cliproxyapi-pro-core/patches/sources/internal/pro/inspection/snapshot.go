package inspection

import (
	"encoding/json"
	"fmt"
	"time"
)

const ResultSnapshotVersion = 1

type RunState string

const (
	RunStateIdle      RunState = "idle"
	RunStateRunning   RunState = "running"
	RunStatePaused    RunState = "paused"
	RunStateStopping  RunState = "stopping"
	RunStateStopped   RunState = "stopped"
	RunStateCompleted RunState = "completed"
	RunStatePartial   RunState = "partial"
	RunStateFailed    RunState = "failed"
)

type OperationRecord struct {
	BatchOperationID string  `json:"batchOperationId,omitempty"`
	OperationID      string  `json:"operationId"`
	Source           string  `json:"source"`
	Action           string  `json:"action"`
	Effect           string  `json:"effect"`
	Status           string  `json:"status"`
	StartedAt        int64   `json:"startedAt"`
	FinishedAt       int64   `json:"finishedAt,omitempty"`
	Before           Result  `json:"before"`
	After            *Result `json:"after,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type ResultSnapshot struct {
	EvidenceGeneration string            `json:"evidenceGeneration,omitempty"`
	Cleared            bool              `json:"cleared,omitempty"`
	History            []Result          `json:"history,omitempty"`
	Operations         []OperationRecord `json:"operations,omitempty"`
	Version            int               `json:"version"`
	State              RunState          `json:"state"`
	LastStartedAt      int64             `json:"lastStartedAt"`
	LastFinishedAt     int64             `json:"lastFinishedAt"`
	LastError          string            `json:"lastError,omitempty"`
	Settings           Settings          `json:"settings"`
	Summary            Summary           `json:"summary"`
	HealthCounts       HealthCounts      `json:"healthCounts"`
	Results            []Result          `json:"results"`
	Confirmations      ConfirmationState `json:"confirmations,omitempty"`
}

func NormalizeSnapshotState(state RunState) RunState {
	switch state {
	case RunStateStopped, RunStateCompleted, RunStatePartial, RunStateFailed:
		return state
	default:
		return RunStateCompleted
	}
}

func DecodeResultSnapshot(raw []byte, now time.Time) (ResultSnapshot, error) {
	var snapshot ResultSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return ResultSnapshot{}, err
	}
	if snapshot.Version != ResultSnapshotVersion {
		return ResultSnapshot{}, fmt.Errorf("unsupported account inspection snapshot version %d", snapshot.Version)
	}
	if snapshot.LastFinishedAt <= 0 {
		return ResultSnapshot{}, fmt.Errorf("account inspection snapshot is missing completion time")
	}
	if snapshot.LastStartedAt <= 0 || snapshot.LastStartedAt > snapshot.LastFinishedAt {
		snapshot.LastStartedAt = snapshot.LastFinishedAt
	}
	snapshot.State = NormalizeSnapshotState(snapshot.State)
	snapshot.Settings = NormalizeSchedule(Schedule{Settings: snapshot.Settings}, now).Settings
	for index := range snapshot.Results {
		snapshot.Results[index] = NormalizeResultSemantics(snapshot.Results[index])
		snapshot.Results[index].ExecutedEffect = EffectiveExecutedEffect(snapshot.Results[index])
	}
	snapshot.Results = SortResults(snapshot.Results)
	snapshot.HealthCounts = ResultHealthCounts(snapshot.Results)
	recomputed := Summary{}
	for _, result := range snapshot.Results {
		recomputed = AdjustSummaryForResult(recomputed, result, 1)
	}
	snapshot.Summary.PendingActionCount = recomputed.PendingActionCount
	snapshot.Summary.PendingDeleteCount = recomputed.PendingDeleteCount
	snapshot.Summary.PendingDisableCount = recomputed.PendingDisableCount
	snapshot.Summary.PendingEnableCount = recomputed.PendingEnableCount
	snapshot.Summary.ExecutedDeleteCount = recomputed.ExecutedDeleteCount
	snapshot.Summary.ExecutedDisableCount = recomputed.ExecutedDisableCount
	snapshot.Summary.ExecutedEnableCount = recomputed.ExecutedEnableCount
	snapshot.Summary.ExecutedQuotaProtectionCount = recomputed.ExecutedQuotaProtectionCount
	snapshot.Summary.ExecutedQuotaRecoveryCount = recomputed.ExecutedQuotaRecoveryCount
	return snapshot, nil
}

package management

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

const inspectionHistoryLimit = 1000
const inspectionOperationLimit = 256

type inspectionEvidence struct {
	EvidenceGeneration string                          `json:"evidenceGeneration,omitempty"`
	Version            int                             `json:"version"`
	History            []accountInspectionResult       `json:"history"`
	Operations         []proinspection.OperationRecord `json:"operations"`
}

func newInspectionEvidenceID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(data[:])
}

// Persist only structured inspection evidence. Upstream bodies, token failures,
// credential fingerprints and free-form errors remain outside the audit journal.
func inspectionEvidenceResult(result accountInspectionResult) accountInspectionResult {
	result.AccessTokenSHA256 = ""
	result.CredentialObserved = ""
	result.CredentialFinal = ""
	result.ObservedSettings = accountInspectionSettings{}
	result.ErrorDetail = ""
	result.DeepProbeError = ""
	result.TokenRefreshError = ""
	if result.Error != "" {
		result.Error = "inspection failed; see errorCode and statusCode"
	}
	if result.ExecuteError != "" {
		result.ExecuteError = "operation failed"
	}
	result.ActionReason = ""
	result.DisplayName = inspectionEvidenceText(result.DisplayName, 256)
	result.Email = inspectionEvidenceText(result.Email, 256)
	result.Name = inspectionEvidenceText(result.Name, 256)
	result.FileName = inspectionEvidenceText(result.FileName, 512)
	result.ErrorCode = inspectionEvidenceText(result.ErrorCode, 128)
	return result
}
func inspectionEvidenceText(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
func (s *accountInspectionScheduler) normalizeEvidenceLocked() {
	if len(s.history) > inspectionHistoryLimit {
		s.history = s.history[len(s.history)-inspectionHistoryLimit:]
	}
	if len(s.operations) > inspectionOperationLimit {
		s.operations = s.operations[len(s.operations)-inspectionOperationLimit:]
	}
	for i := range s.history {
		s.history[i] = inspectionEvidenceResult(s.history[i])
	}
	for i := range s.operations {
		record := &s.operations[i]
		record.Before = inspectionEvidenceResult(record.Before)
		if record.After != nil {
			after := inspectionEvidenceResult(*record.After)
			record.After = &after
		}
		if record.Status == "running" {
			record.Status = "interrupted"
			record.Error = "server restarted; execution outcome is unknown"
			record.FinishedAt = time.Now().UnixMilli()
		}
	}
}
func (s *accountInspectionScheduler) archiveInspectionResultLocked(result accountInspectionResult) {
	if result.Key == "" {
		return
	}
	result = inspectionEvidenceResult(result)
	for _, old := range s.history {
		if result.ResultRef != "" && old.ResultRef == result.ResultRef {
			return
		}
	}
	s.history = append(s.history, result)
	if len(s.history) > inspectionHistoryLimit {
		s.history = s.history[len(s.history)-inspectionHistoryLimit:]
	}
}
func (s *accountInspectionScheduler) saveEvidenceLocked() error {
	// Explicitly constructed schedulers may be memory-only. The production
	// constructor always assigns snapshotPath, so real operations still require
	// their durable intent before performing any mutation.
	if s.snapshotPath == "" {
		return nil
	}
	path := s.snapshotPath + ".evidence.json"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(inspectionEvidence{Version: 1, EvidenceGeneration: s.evidenceGeneration, History: s.history, Operations: s.operations})
	if err != nil {
		return err
	}
	return proinspection.AtomicWriteFile(path, raw, 0600)
}
func (s *accountInspectionScheduler) loadInspectionEvidence() error {
	if s.snapshotPath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.snapshotPath + ".evidence.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var evidence inspectionEvidence
	if err = json.Unmarshal(raw, &evidence); err != nil {
		return err
	}
	if evidence.Version != 1 {
		return errors.New("unsupported inspection evidence version")
	}
	if evidence.EvidenceGeneration != s.evidenceGeneration {
		return nil
	}
	s.history = evidence.History
	s.operations = evidence.Operations
	s.normalizeEvidenceLocked()
	return s.saveEvidenceLocked()
}
func (s *accountInspectionScheduler) beginInspectionOperation(source, effect string, before accountInspectionResult, batchID ...string) (string, error) {
	id := newInspectionEvidenceID()
	if id == "" {
		return "", errors.New("failed to create operation ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previousHistory := append([]accountInspectionResult(nil), s.history...)
	previousOperations := append([]proinspection.OperationRecord(nil), s.operations...)
	s.archiveInspectionResultLocked(before)
	record := proinspection.OperationRecord{OperationID: id, Source: source, Action: effect, Effect: effect, Status: "running", StartedAt: time.Now().UnixMilli(), Before: inspectionEvidenceResult(before)}
	if len(batchID) > 0 {
		record.BatchOperationID = batchID[0]
	}
	if len(s.operations) >= inspectionOperationLimit {
		removable := -1
		for index, existing := range s.operations {
			if existing.Status != "running" {
				removable = index
				break
			}
		}
		if removable < 0 {
			s.history = previousHistory
			return "", errors.New("operation audit capacity reached")
		}
		s.operations = append(s.operations[:removable], s.operations[removable+1:]...)
	}
	s.operations = append(s.operations, record)
	if err := s.saveEvidenceLocked(); err != nil {
		s.operations = previousOperations
		s.history = previousHistory
		return "", err
	}
	return id, nil
}
func (s *accountInspectionScheduler) finishInspectionOperation(id string, after *accountInspectionResult, operationErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.operations {
		record := &s.operations[i]
		if record.OperationID != id {
			continue
		}
		record.FinishedAt = time.Now().UnixMilli()
		record.Status = "succeeded"
		if operationErr != nil {
			record.Status = "failed"
			record.Error = "operation failed; inspect current account state before retry"
		}
		if after != nil {
			copy := inspectionEvidenceResult(*after)
			record.After = &copy
		}
		break
	}
	return s.saveEvidenceLocked()
}

type inspectionBatchAuditContextKey struct{}

func (s *accountInspectionScheduler) currentInspectionEvidence(key string) accountInspectionResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, result := range s.status.Results {
		if result.Key == key {
			return result
		}
	}
	return accountInspectionResult{Key: key}
}
func (s *accountInspectionScheduler) executeRecordedInspectionAction(ctx context.Context, result *accountInspectionResult, settings accountInspectionSettings, action accountInspectionAction, suggested bool, workers int, source string) error {
	// Batch execution has its own enclosing receipt and audit boundary.
	if ctx.Value(inspectionBatchAuditContextKey{}) != nil {
		return s.executeResolvedAction(ctx, result, settings, action, suggested, workers)
	}
	before := *result
	if source == "manual" {
		current := s.currentInspectionEvidence(result.Key)
		if current.ResultRef != "" {
			before = current
		}
	}
	item := proinspection.ActionItemFromResult(before, action)
	item.Suggested = suggested
	effect := inspectionBatchEffect("action", item)
	id, err := s.beginInspectionOperation(source, effect, before)
	if err != nil {
		return err
	}
	err = s.executeResolvedAction(ctx, result, settings, action, suggested, workers)
	executed := *result
	executed.Action = action
	if err == nil {
		quotaSuggestion := before.IsQuota && action == accountInspectionActionDisable || before.QuotaCooling && action == accountInspectionActionEnable
		executed.ExecutedAt = time.Now().UnixMilli()
		executed.ExecutedAction = action
		executed.ExecutedEffect = proinspection.EffectForAction(action, suggested && quotaSuggestion)
		executed.ExecutedSuggested = suggested
		executed.Executed = true
		executed.ExecuteError = ""
		if action == accountInspectionActionDisable && !(suggested && before.IsQuota) {
			executed.Disabled = true
		}
		if action == accountInspectionActionEnable && !(suggested && before.QuotaCooling) {
			executed.Disabled = false
		}
	} else {
		executed.ExecuteError = err.Error()
	}
	after, _ := proinspection.MergeManualActionResult(before, executed)
	// A journal write failure must not turn a completed destructive action into a
	// retryable failure. Its last persisted running record is interrupted on restart.
	if saveErr := s.finishInspectionOperation(id, &after, err); saveErr != nil {
		s.appendLog("error", "operation completed but inspection audit persistence failed")
	}
	return err
}
func (s *accountInspectionScheduler) inspectOne(ctx context.Context, item accountInspectionActionItem) (accountInspectionResult, error) {
	return s.recordInspectionObservation(ctx, item, "inspect", func() (accountInspectionResult, error) { return s.inspectOneUnrecorded(ctx, item) })
}
func (s *accountInspectionScheduler) refreshTokenNow(ctx context.Context, item accountInspectionActionItem) (accountInspectionResult, error) {
	return s.recordInspectionObservation(ctx, item, "token_refresh", func() (accountInspectionResult, error) { return s.refreshTokenNowUnrecorded(ctx, item) })
}
func (s *accountInspectionScheduler) recordInspectionObservation(ctx context.Context, item accountInspectionActionItem, effect string, run func() (accountInspectionResult, error)) (accountInspectionResult, error) {
	before := s.currentInspectionEvidence(item.Key)
	if before.ResultRef == "" || item.ResultRef != "" && before.ResultRef != item.ResultRef {
		return run()
	}
	s.mu.Lock()
	restored := s.status.RestoredSnapshot
	s.mu.Unlock()
	if restored {
		return run()
	}
	id, err := s.beginInspectionOperation("manual", effect, before)
	if err != nil {
		return accountInspectionResult{}, err
	}
	result, runErr := run()
	if effect == "token_refresh" && result.ResultRef != "" {
		result.ObservedAt = time.Now().UnixMilli()
		if result.RegistrationEpoch != "" && result.RegistrationEpoch == before.RegistrationEpoch {
			result.ParentResultRef = before.ResultRef
			result.RunID = before.RunID
		}
	}
	if saveErr := s.finishInspectionOperation(id, &result, runErr); saveErr != nil {
		s.appendLog("error", "observation audit persistence failed")
	}
	return result, runErr
}

// The snapshot is the generation commit point. On an interrupted import the
// previous snapshot remains authoritative and rejects the uncommitted sidecar.
func (s *accountInspectionScheduler) commitImportedInspectionEvidenceLocked(snapshot accountInspectionResultSnapshot) error {
	path := s.snapshotPath + ".evidence.json"
	previous, readErr := os.ReadFile(path)
	existed := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	next, err := json.Marshal(inspectionEvidence{Version: 1, EvidenceGeneration: snapshot.EvidenceGeneration, History: snapshot.History, Operations: snapshot.Operations})
	if err != nil {
		return err
	}
	if err = proinspection.AtomicWriteFile(path, next, 0600); err != nil {
		return err
	}
	if err = s.writeResultSnapshotLocked(snapshot); err != nil {
		var rollbackErr error
		if existed {
			rollbackErr = proinspection.AtomicWriteFile(path, previous, 0600)
		} else {
			rollbackErr = os.Remove(path)
			if os.IsNotExist(rollbackErr) {
				rollbackErr = nil
			}
		}
		return errors.Join(err, rollbackErr)
	}
	return nil
}

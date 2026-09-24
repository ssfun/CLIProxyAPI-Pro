package management

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

// Receipts are persisted for audit and never replayed on restart. A prepared operation
// owns its exact targets; only retry preflight may bind newer result references.
const inspectionBatchRetention = 24 * time.Hour
const inspectionBatchPreparationTTL = 10 * time.Minute
const inspectionBatchCapacity = 256

type inspectionBatchItem struct {
	Key      string                      `json:"key"`
	Status   string                      `json:"status"`
	Effect   string                      `json:"effect"`
	Error    string                      `json:"error,omitempty"`
	Before   *accountInspectionResult    `json:"before,omitempty"`
	Item     accountInspectionActionItem `json:"item"`
	Outcome  any                         `json:"outcome,omitempty"`
	epoch    uint64
	recovery []routingRecoveryRequest
}
type inspectionBatchOperation struct {
	OperationID       string                `json:"operationId"`
	Kind              string                `json:"kind"`
	State             string                `json:"state"`
	CreatedAt         int64                 `json:"createdAt"`
	ExpiresAt         int64                 `json:"expiresAt"`
	Items             []inspectionBatchItem `json:"items"`
	Summary           map[string]int        `json:"summary"`
	RetryID           string                `json:"retryOperationId,omitempty"`
	ParentOperationID string                `json:"parentOperationId,omitempty"`
	PersistenceError  string                `json:"persistenceError,omitempty"`
}
type inspectionBatchStore struct {
	sync.Mutex
	operations map[string]*inspectionBatchOperation
	once       sync.Once
	path       string
	loadErr    error
}

var inspectionBatchStores sync.Map

func inspectionBatches(h *Handler) *inspectionBatchStore {
	value, _ := inspectionBatchStores.LoadOrStore(h, &inspectionBatchStore{operations: make(map[string]*inspectionBatchOperation)})
	store := value.(*inspectionBatchStore)
	store.once.Do(func() {
		if scheduler := schedulerForHandler(h); scheduler != nil {
			store.path = scheduler.snapshotPath + ".batches.json"
			store.loadErr = store.load()
		}
	})
	return store
}
func (operation *inspectionBatchOperation) snapshot() json.RawMessage {
	operation.Summary = map[string]int{"total": len(operation.Items), "ready": 0, "running": 0, "succeeded": 0, "failed": 0, "stale": 0, "unsupported": 0, "interrupted": 0}
	for _, item := range operation.Items {
		operation.Summary[item.Status]++
	}
	data, _ := json.Marshal(operation)
	return data
}
func (h *Handler) RegisterAccountInspectionBatchRoutes(group *gin.RouterGroup) {
	group.POST("/account-inspection/batches/preflight", h.PreflightAccountInspectionBatch)
	group.GET("/account-inspection/batches/:operationId", h.GetAccountInspectionBatch)
	group.POST("/account-inspection/batches/:operationId/execute", h.ExecuteAccountInspectionBatch)
	group.POST("/account-inspection/batches/:operationId/retry", h.RetryAccountInspectionBatch)
}
func (h *Handler) preflightInspectionBatch(kind string, items []accountInspectionActionItem, refresh bool) (*inspectionBatchOperation, error) {
	if kind != "inspect" && kind != "action" && kind != "recover" {
		return nil, errors.New("invalid batch kind")
	}
	if len(items) == 0 || len(items) > 500 {
		return nil, errors.New("batch requires 1 to 500 items")
	}
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		return nil, errors.New("account inspection scheduler unavailable")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	now := time.Now()
	operation := &inspectionBatchOperation{OperationID: hex.EncodeToString(token[:]), Kind: kind, State: "prepared", CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(inspectionBatchPreparationTTL).UnixMilli(), Items: make([]inspectionBatchItem, 0, len(items))}
	seen := make(map[string]bool)
	for _, requested := range items {
		entry := inspectionBatchItem{Key: requested.Key, Item: requested, Status: "ready", Effect: "unknown"}
		suggested := requested.Suggested
		if refresh {
			requested.ResultRef = ""
			requested.Suggested = false
		}
		var bindErr error
		var bound accountInspectionActionItem
		if !refresh && requested.ResultRef == "" {
			bindErr = errAccountInspectionResultStale
		} else {
			bound, bindErr = scheduler.bindActionItemToSnapshot(requested)
		}
		if bindErr != nil {
			entry.Status, entry.Error = "stale", bindErr.Error()
			operation.Items = append(operation.Items, entry)
			continue
		}
		bound.Suggested = suggested
		if refresh && suggested {
			bound.Action = bound.RecommendedAction
			rebound, retryErr := scheduler.bindActionItemToSnapshot(bound)
			if retryErr != nil {
				entry.Status, entry.Error = "stale", retryErr.Error()
				operation.Items = append(operation.Items, entry)
				continue
			}
			bound = rebound
			bound.Suggested = true
		}
		entry.Key, entry.Item = bound.Key, bound
		before := inspectionEvidenceResult(scheduler.currentInspectionEvidence(bound.Key))
		if before.ResultRef != bound.ResultRef {
			entry.Status, entry.Error = "stale", errAccountInspectionResultStale.Error()
			operation.Items = append(operation.Items, entry)
			continue
		}
		entry.Before = &before
		if seen[bound.Key] {
			entry.Status, entry.Error = "unsupported", "duplicate target"
			operation.Items = append(operation.Items, entry)
			continue
		}
		seen[bound.Key] = true
		auth, err := scheduler.actionAuthForResult(bound.ToResult())
		if err != nil {
			entry.Status, entry.Error = "stale", err.Error()
		} else {
			entry.epoch = auth.RegistrationEpoch
			scheduler.mu.Lock()
			restored := scheduler.status.RestoredSnapshot
			scheduler.mu.Unlock()
			if restored {
				entry.Status, entry.Error = "stale", errAccountInspectionRestoredSnapshotReadOnly.Error()
			}
			if kind == "action" && bound.Action != accountInspectionActionDelete && bound.Action != accountInspectionActionDisable && bound.Action != accountInspectionActionEnable {
				entry.Status, entry.Error = "unsupported", "unsupported action"
			}
			if _, supported := accountInspectionSupportedProviders[bound.Provider]; kind == "inspect" && !supported {
				entry.Status, entry.Error = "unsupported", "provider does not support inspection"
			}
			if kind == "recover" || kind == "action" && bound.Suggested && bound.Action == accountInspectionActionEnable && bound.QuotaCooling {
				board := schedulingBoardAccount(auth, now)
				for _, detail := range board.Details {
					if detail.Source != "inspection" && detail.Source != "upstream" {
						continue
					}
					entry.recovery = append(entry.recovery, routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: detail.Source, Model: detail.Model, Revision: detail.Revision})
				}
				if auth.Disabled || len(entry.recovery) == 0 {
					entry.Status, entry.Error = "unsupported", "account has no active recoverable restriction"
				}
			}
		}
		if entry.Status == "ready" {
			entry.Effect = inspectionBatchEffect(kind, bound)
		}
		operation.Items = append(operation.Items, entry)
	}
	return operation, nil
}

// Effect is resolved from the bound server snapshot, never from client quota flags.
func inspectionBatchEffect(kind string, item accountInspectionActionItem) string {
	if kind == "inspect" {
		return "inspect"
	}
	if kind == "recover" {
		return "recovery_check"
	}
	switch item.Action {
	case accountInspectionActionDelete:
		return "delete"
	case accountInspectionActionDisable:
		if item.Suggested && item.IsQuota {
			return "quota_protection"
		}
		return "admin_disable"
	case accountInspectionActionEnable:
		if item.Suggested && item.QuotaCooling {
			return "quota_recovery"
		}
		return "admin_enable"
	default:
		return "unknown"
	}
}

func (store *inspectionBatchStore) insert(operation *inspectionBatchOperation) bool {
	now := time.Now().UnixMilli()
	for id, existing := range store.operations {
		if existing.State != "running" && now-existing.CreatedAt > inspectionBatchRetention.Milliseconds() {
			delete(store.operations, id)
		}
	}
	if len(store.operations) >= inspectionBatchCapacity {
		return false
	}
	store.operations[operation.OperationID] = operation
	return true
}
func (h *Handler) PreflightAccountInspectionBatch(c *gin.Context) {
	var request struct {
		Kind  string                        `json:"kind"`
		Items []accountInspectionActionItem `json:"items"`
		Scope *struct {
			Type        string                        `json:"type"`
			Items       []accountInspectionActionItem `json:"items"`
			Filter      string                        `json:"filter"`
			Provider    string                        `json:"provider"`
			Search      string                        `json:"search"`
			PendingOnly bool                          `json:"pendingOnly"`
			Action      accountInspectionAction       `json:"action"`
			Suggested   bool                          `json:"suggested"`
		} `json:"scope"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if scope := request.Scope; scope != nil {
		switch scope.Type {
		case "selected":
			request.Items = scope.Items
		case "filtered":
			scheduler := schedulerForHandler(h)
			if scheduler == nil {
				c.JSON(503, gin.H{"error": "scheduler unavailable"})
				return
			}
			scheduler.mu.Lock()
			results, info := proinspection.PaginateResults(scheduler.status.Results, 1, 501, 501, scope.Filter, scope.PendingOnly || request.Kind == "action" && scope.Suggested, scope.Provider, scope.Search)
			request.Items = make([]accountInspectionActionItem, 0, len(results))
			for _, result := range results {
				action := scope.Action
				if scope.Suggested {
					action = result.Action
				}
				item := proinspection.ActionItemFromResult(result, action)
				item.Suggested = scope.Suggested
				request.Items = append(request.Items, item)
			}
			scheduler.mu.Unlock()
			if info.Total > 500 {
				c.JSON(400, gin.H{"error": "filtered scope exceeds 500 targets; narrow the filters"})
				return
			}
		default:
			c.JSON(400, gin.H{"error": "invalid scope type"})
			return
		}
	}
	operation, err := h.preflightInspectionBatch(request.Kind, request.Items, false)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	store := inspectionBatches(h)
	store.Lock()
	defer store.Unlock()
	if !store.insert(operation) {
		c.JSON(429, gin.H{"error": "batch receipt capacity reached; retry after retention expires"})
		return
	}
	if err := store.save(); err != nil {
		delete(store.operations, operation.OperationID)
		c.JSON(500, gin.H{"error": "failed to persist operation"})
		return
	}
	c.Data(200, "application/json", operation.snapshot())
}
func (h *Handler) GetAccountInspectionBatch(c *gin.Context) {
	store := inspectionBatches(h)
	store.Lock()
	defer store.Unlock()
	if store.loadErr != nil {
		c.JSON(500, gin.H{"error": "operation receipts could not be loaded"})
		return
	}
	operation := store.operations[c.Param("operationId")]
	if operation == nil {
		c.JSON(404, gin.H{"error": "operation not found; receipt was pruned or is unavailable"})
		return
	}
	c.Data(200, "application/json", operation.snapshot())
}
func (h *Handler) ExecuteAccountInspectionBatch(c *gin.Context) {
	store := inspectionBatches(h)
	store.Lock()
	operation := store.operations[c.Param("operationId")]
	if operation == nil {
		store.Unlock()
		c.JSON(404, gin.H{"error": "operation not found"})
		return
	}
	if operation.State == "interrupted" {
		store.Unlock()
		c.JSON(409, gin.H{"error": "interrupted operation cannot be replayed; inspect current state and prepare explicitly"})
		return
	}
	if operation.State == "prepared" {
		if time.Now().UnixMilli() > operation.ExpiresAt {
			store.Unlock()
			c.JSON(409, gin.H{"error": "preflight expired; prepare a new operation"})
			return
		}
		operation.State = "running"
		if err := store.save(); err != nil {
			operation.State = "prepared"
			store.Unlock()
			c.JSON(500, gin.H{"error": "failed to persist execution intent"})
			return
		}
		go h.runInspectionBatch(store, operation)
	}
	response := operation.snapshot()
	store.Unlock()
	c.Data(http.StatusAccepted, "application/json", response)
}
func (h *Handler) RetryAccountInspectionBatch(c *gin.Context) {
	store := inspectionBatches(h)
	store.Lock()
	defer store.Unlock()
	parent := store.operations[c.Param("operationId")]
	if parent == nil {
		c.JSON(404, gin.H{"error": "operation not found"})
		return
	}
	if parent.State != "completed" && parent.State != "interrupted" {
		c.JSON(409, gin.H{"error": "operation must complete before retry"})
		return
	}
	if previous := store.operations[parent.RetryID]; previous != nil {
		c.Data(200, "application/json", previous.snapshot())
		return
	}
	items := make([]accountInspectionActionItem, 0)
	for _, item := range parent.Items {
		if item.Status == "failed" || item.Status == "stale" {
			items = append(items, item.Item)
		}
	}
	if len(items) == 0 {
		c.JSON(409, gin.H{"error": "operation has no failed items"})
		return
	}
	operation, err := h.preflightInspectionBatch(parent.Kind, items, true)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if !store.insert(operation) {
		c.JSON(429, gin.H{"error": "batch receipt capacity reached"})
		return
	}
	parent.RetryID = operation.OperationID
	operation.ParentOperationID = parent.OperationID
	if err := store.save(); err != nil {
		delete(store.operations, operation.OperationID)
		parent.RetryID = ""
		c.JSON(500, gin.H{"error": "failed to persist operation"})
		return
	}
	c.Data(200, "application/json", operation.snapshot())
}
func (h *Handler) runInspectionBatch(store *inspectionBatchStore, operation *inspectionBatchOperation) {
	// A disconnected HTTP client must not abandon a mutation with an unknown result.
	baseContext := h.lifecycleContext
	if baseContext == nil {
		baseContext = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseContext, accountInspectionMaxRunDuration)
	defer cancel()
	defer func() {
		recovered := recover()
		store.Lock()
		defer store.Unlock()
		for i := range operation.Items {
			if operation.Items[i].Status == "ready" || operation.Items[i].Status == "running" {
				operation.Items[i].Status = "failed"
				operation.Items[i].Error = "operation interrupted"
				if recovered != nil {
					operation.Items[i].Error = "operation failed unexpectedly"
				}
			}
		}
		operation.State = "completed"
		if err := store.save(); err != nil {
			operation.PersistenceError = "failed to persist completed receipt"
		}
	}()
	if operation.Kind == "inspect" {
		h.runInspectionRecheckBatch(ctx, store, operation)
		return
	}
	// Existing scheduler methods serialize manual mutation and enforce lifecycle
	// fences; running items sequentially avoids holding those locks across recovery.
	for index := range operation.Items {
		store.Lock()
		entry := operation.Items[index]
		if entry.Status != "ready" {
			store.Unlock()
			continue
		}
		operation.Items[index].Status = "running"
		if err := store.save(); err != nil {
			operation.Items[index].Status = "failed"
			operation.Items[index].Error = "failed to persist execution intent"
			store.Unlock()
			continue
		}
		store.Unlock()
		scheduler := schedulerForHandler(h)
		before := accountInspectionResult{Key: entry.Key}
		if entry.Before != nil {
			before = *entry.Before
		}
		auditID, auditErr := scheduler.beginInspectionOperation("batch", entry.Effect, before, operation.OperationID)
		var outcome any
		err := auditErr
		if err == nil {
			outcome, err = h.executeInspectionBatchItem(context.WithValue(ctx, inspectionBatchAuditContextKey{}, operation.OperationID), operation.Kind, entry)
			after := scheduler.currentInspectionEvidence(entry.Key)
			if saveErr := scheduler.finishInspectionOperation(auditID, &after, err); saveErr != nil {
				scheduler.appendLog("error", "batch audit persistence failed")
			}
		}
		entry.Outcome = outcome
		entry.Status = "succeeded"
		if err != nil {
			entry.Status = "failed"
			entry.Error = err.Error()
			if errors.Is(err, errAccountInspectionResultStale) {
				entry.Status = "stale"
			}
		}
		store.Lock()
		operation.Items[index] = entry
		if err := store.save(); err != nil {
			operation.PersistenceError = "failed to persist item receipt"
		}
		store.Unlock()
	}
}

// Rechecks share the scheduler's lifecycle and manual-action lock while its
// provider workers perform probes concurrently. Batch store locks never span a
// scheduler call, so intent and receipt persistence cannot invert scheduler locks.
func (h *Handler) runInspectionRecheckBatch(ctx context.Context, store *inspectionBatchStore, operation *inspectionBatchOperation) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		return
	}
	indices := make([]int, 0, len(operation.Items))
	items := make([]accountInspectionActionItem, 0, len(operation.Items))
	store.Lock()
	for index, entry := range operation.Items {
		if entry.Status == "ready" {
			indices = append(indices, index)
			items = append(items, entry.Item)
		}
	}
	store.Unlock()
	auditIDs := make([]string, len(items))
	hooks := &inspectionManyHooks{
		before: func(index int) error {
			store.Lock()
			entry := operation.Items[indices[index]]
			operation.Items[indices[index]].Status = "running"
			if err := store.save(); err != nil {
				store.Unlock()
				return errors.New("failed to persist execution intent")
			}
			store.Unlock()
			if err := scheduler.inspectionBatchProcessingStateUnchanged(entry.Before); err != nil {
				return err
			}
			bound, err := scheduler.bindActionItemToSnapshot(entry.Item)
			if err != nil {
				return err
			}
			auth, err := scheduler.actionAuthForResult(bound.ToResult())
			if err != nil || auth.RegistrationEpoch != entry.epoch {
				return errAccountInspectionResultStale
			}
			before := accountInspectionResult{Key: entry.Key}
			if entry.Before != nil {
				before = *entry.Before
			}
			auditIDs[index], err = scheduler.beginInspectionOperation("batch", entry.Effect, before, operation.OperationID)
			return err
		},
		after: func(index int, outcome accountInspectionOutcome) {
			var outcomeErr error
			if !outcome.Success {
				outcomeErr = errors.New(outcome.Error)
			}
			if auditIDs[index] != "" {
				after := scheduler.currentInspectionEvidence(items[index].Key)
				if err := scheduler.finishInspectionOperation(auditIDs[index], &after, outcomeErr); err != nil {
					scheduler.appendLog("error", "batch audit persistence failed")
				}
			}
			store.Lock()
			defer store.Unlock()
			entry := &operation.Items[indices[index]]
			entry.Outcome = outcome
			entry.Status = "succeeded"
			if !outcome.Success {
				entry.Status, entry.Error = "failed", outcome.Error
				if outcome.Error == errAccountInspectionResultStale.Error() {
					entry.Status = "stale"
				}
			}
			if err := store.save(); err != nil {
				operation.PersistenceError = "failed to persist item receipt"
			}
		},
	}
	_, err := scheduler.inspectManyWithHooks(ctx, items, hooks)
	if err != nil {
		store.Lock()
		for _, index := range indices {
			if operation.Items[index].Status == "ready" || operation.Items[index].Status == "running" {
				operation.Items[index].Status, operation.Items[index].Error = "failed", err.Error()
			}
		}
		store.Unlock()
	}
}

// A result reference binds the observation, while these fields bind its
// processing decision. Both must still match the preflight evidence.
func (s *accountInspectionScheduler) inspectionBatchProcessingStateUnchanged(before *accountInspectionResult) error {
	if before == nil || before.Key == "" || before.ResultRef == "" {
		return errAccountInspectionResultStale
	}
	current := s.currentInspectionEvidence(before.Key)
	if current.ResultRef != before.ResultRef ||
		current.Executed != before.Executed ||
		current.ExecutedAt != before.ExecutedAt ||
		current.ExecutedAction != before.ExecutedAction ||
		current.ExecutedEffect != before.ExecutedEffect ||
		current.ExecutedSuggested != before.ExecutedSuggested ||
		current.OperationAction != before.OperationAction ||
		(current.ExecuteError == "") != (before.ExecuteError == "") ||
		current.Disabled != before.Disabled ||
		current.QuotaCooling != before.QuotaCooling ||
		current.QuotaRetryAt != before.QuotaRetryAt ||
		current.QuotaRevision != before.QuotaRevision {
		return errAccountInspectionResultStale
	}
	return nil
}

func (h *Handler) executeInspectionBatchItem(ctx context.Context, kind string, entry inspectionBatchItem) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		return nil, errors.New("scheduler unavailable")
	}
	if err := scheduler.inspectionBatchProcessingStateUnchanged(entry.Before); err != nil {
		return nil, err
	}
	bound, err := scheduler.bindActionItemToSnapshot(entry.Item)
	if err != nil {
		return nil, err
	}
	auth, err := scheduler.actionAuthForResult(bound.ToResult())
	if err != nil || auth.RegistrationEpoch != entry.epoch {
		return nil, errAccountInspectionResultStale
	}
	if kind == "action" && entry.Item.Suggested && entry.Item.Action == accountInspectionActionEnable && entry.Item.QuotaCooling {
		kind = "recover"
	}
	switch kind {
	case "inspect":
		outcomes, err := scheduler.inspectMany(ctx, []accountInspectionActionItem{entry.Item})
		if err != nil {
			return outcomes, err
		}
		if len(outcomes) != 1 {
			return outcomes, errors.New("inspection returned no receipt")
		}
		if !outcomes[0].Success {
			return outcomes[0], errors.New(outcomes[0].Error)
		}
		return outcomes[0], nil
	case "action":
		outcomes, err := scheduler.executeManualActionsWithExpected(ctx, []accountInspectionActionItem{entry.Item}, entry.Before)
		// A successful mutation remains successful if only snapshot persistence fails;
		// return the warning in its receipt so retry cannot repeat the side effect.
		if len(outcomes) == 1 && outcomes[0].Success {
			if err != nil {
				return gin.H{"outcome": outcomes[0], "warning": err.Error()}, nil
			}
			return outcomes[0], nil
		}
		if err != nil {
			return outcomes, err
		}
		if len(outcomes) != 1 {
			return outcomes, errors.New("action returned no receipt")
		}
		return outcomes[0], errors.New(outcomes[0].Error)
	case "recover":
		receipts := make([]json.RawMessage, 0, len(entry.recovery))
		var failure error
		for _, request := range entry.recovery {
			// An earlier directed probe may clear several restrictions. Recheck
			// the fixed target before invoking its versioned recovery endpoint:
			// absence means the target is satisfied, while a changed revision
			// must still fail the endpoint's optimistic concurrency check.
			current, ok := h.authManager.GetByID(auth.ID)
			if !ok || current == nil || current.RegistrationEpoch != entry.epoch || current.EnsureIndex() != request.AuthIndex || current.Disabled {
				return gin.H{"receipts": receipts}, errAccountInspectionResultStale
			}
			board := schedulingBoardAccount(current, time.Now())
			active := false
			for _, detail := range board.Details {
				if detail.Source == request.Source && detail.Model == request.Model {
					active = true
					break
				}
			}
			if !active {
				receipt, _ := json.Marshal(gin.H{
					"after":  board,
					"phases": []gin.H{{"source": request.Source, "model": request.Model, "status": "skipped", "reason": "restriction already cleared"}},
				})
				receipts = append(receipts, receipt)
				continue
			}
			body, _ := json.Marshal(request)
			writer := &inspectionBatchResponse{header: make(http.Header)}
			requestHTTP, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/routing-policy/check", bytes.NewReader(body))
			requestHTTP.Header.Set("Content-Type", "application/json")
			router := gin.New()
			router.POST("/routing-policy/check", h.CheckRoutingAccount)
			router.ServeHTTP(writer, requestHTTP)
			receipts = append(receipts, append(json.RawMessage(nil), writer.body.Bytes()...))
			var response struct {
				Error  string                 `json:"error"`
				Phases []routingRecoveryPhase `json:"phases"`
			}
			_ = json.Unmarshal(writer.body.Bytes(), &response)
			if writer.status >= 400 {
				failure = fmt.Errorf("recovery rejected: %s", response.Error)
			}
			for _, phase := range response.Phases {
				if phase.Status != "completed" {
					failure = fmt.Errorf("recovery %s: %s", phase.Status, phase.Error)
				}
			}
		}
		current, ok := h.authManager.GetByID(auth.ID)
		if !ok || current == nil || current.RegistrationEpoch != entry.epoch || current.EnsureIndex() != entry.Item.AuthIndex || current.Disabled {
			return receipts, errAccountInspectionResultStale
		}
		after := schedulingBoardAccount(current, time.Now())
		if after.AuthID != "" && failure == nil {
			failure = errors.New("check completed; account remains restricted")
		}
		return gin.H{"receipts": receipts, "after": after}, failure
	}
	return nil, errors.New("unsupported batch kind")
}

type inspectionBatchResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (writer *inspectionBatchResponse) Header() http.Header    { return writer.header }
func (writer *inspectionBatchResponse) WriteHeader(status int) { writer.status = status }
func (writer *inspectionBatchResponse) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = 200
	}
	return writer.body.Write(data)
}

func (store *inspectionBatchStore) load() error {
	raw, err := os.ReadFile(store.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved struct {
		Version    int                         `json:"version"`
		Operations []*inspectionBatchOperation `json:"operations"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	if saved.Version != 1 {
		return errors.New("unsupported batch receipt version")
	}
	now := time.Now().UnixMilli()
	for _, operation := range saved.Operations {
		if now-operation.CreatedAt > inspectionBatchRetention.Milliseconds() {
			continue
		}
		if operation.State == "running" || operation.State == "prepared" {
			operation.State = "interrupted"
			for i := range operation.Items {
				if operation.Items[i].Status == "ready" || operation.Items[i].Status == "running" {
					operation.Items[i].Status = "interrupted"
					operation.Items[i].Error = "server restarted; execution outcome is unknown"
				}
			}
		}
		store.operations[operation.OperationID] = operation
		if len(store.operations) >= inspectionBatchCapacity {
			break
		}
	}
	return store.save()
}
func scrubInspectionBatchEvidence(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			switch key {
			case "test", "body", "response", "errorDetail", "deepProbeError", "tokenRefreshError":
				delete(typed, key)
			case "error", "reason", "actionReason", "executeError":
				if message, ok := child.(string); ok && message != "" {
					typed[key] = "details omitted from retained audit evidence"
				}
			default:
				scrubInspectionBatchEvidence(child)
			}
		}
	case []any:
		for _, child := range typed {
			scrubInspectionBatchEvidence(child)
		}
	}
}
func (store *inspectionBatchStore) save() error {
	if store.loadErr != nil {
		return store.loadErr
	}
	if store.path == "" {
		return errors.New("batch receipt persistence unavailable")
	}
	records := make([]json.RawMessage, 0, len(store.operations))
	for _, operation := range store.operations {
		raw := operation.snapshot()
		var safe any
		if err := json.Unmarshal(raw, &safe); err != nil {
			return err
		}
		scrubInspectionBatchEvidence(safe)
		raw, err := json.Marshal(safe)
		if err != nil {
			return err
		}
		records = append(records, raw)
	}
	raw, err := json.Marshal(gin.H{"version": 1, "operations": records})
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(store.path), 0700); err != nil {
		return err
	}
	return proinspection.AtomicWriteFile(store.path, raw, 0600)
}
func (h *Handler) ListAccountInspectionBatches(c *gin.Context) {
	store := inspectionBatches(h)
	store.Lock()
	defer store.Unlock()
	if store.loadErr != nil {
		c.JSON(500, gin.H{"error": "operation receipts could not be loaded"})
		return
	}
	items := make([]*inspectionBatchOperation, 0)
	key := c.Query("key")
	for _, operation := range store.operations {
		if key != "" {
			found := false
			for _, item := range operation.Items {
				if item.Key == key {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		items = append(items, operation)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt > items[j].CreatedAt })
	start, end, page := inspectionEvidencePage(c, len(items))
	output := make([]json.RawMessage, 0, end-start)
	for _, operation := range items[start:end] {
		output = append(output, operation.snapshot())
	}
	c.JSON(200, gin.H{"items": output, "pageInfo": page, "retention": gin.H{"maxOperations": inspectionBatchCapacity, "maxAgeHours": 24}})
}

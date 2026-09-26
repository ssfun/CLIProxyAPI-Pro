package management

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
)

// Receipts are persisted for audit and never replayed on restart. A prepared operation
// owns exact account registrations and confirmed actions, not result references.
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
	OperationID        string                `json:"operationId"`
	ClientRequestID    string                `json:"clientRequestId,omitempty"`
	RequestFingerprint string                `json:"requestFingerprint,omitempty"`
	Kind               string                `json:"kind"`
	State              string                `json:"state"`
	CreatedAt          int64                 `json:"createdAt"`
	ExpiresAt          int64                 `json:"expiresAt"`
	Items              []inspectionBatchItem `json:"items"`
	Summary            map[string]int        `json:"summary"`
	RetryID            string                `json:"retryOperationId,omitempty"`
	ParentOperationID  string                `json:"parentOperationId,omitempty"`
	PersistenceError   string                `json:"persistenceError,omitempty"`
}
type inspectionBatchStore struct {
	sync.Mutex
	operations map[string]*inspectionBatchOperation
	once       sync.Once
	path       string
	loadErr    error
}

var inspectionBatchStores sync.Map

type inspectionBatchScope struct {
	Type        string                        `json:"type"`
	Items       []accountInspectionActionItem `json:"items"`
	Filter      string                        `json:"filter"`
	Provider    string                        `json:"provider"`
	Search      string                        `json:"search"`
	PendingOnly bool                          `json:"pendingOnly"`
	Action      accountInspectionAction       `json:"action"`
	Suggested   bool                          `json:"suggested"`
}

type inspectionBatchRequest struct {
	Kind            string                        `json:"kind"`
	Items           []accountInspectionActionItem `json:"items"`
	Scope           *inspectionBatchScope         `json:"scope"`
	ClientRequestID string                        `json:"clientRequestId"`
}

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
	group.GET("/account-inspection/batches", h.ListAccountInspectionBatches)
	group.POST("/account-inspection/batches", h.StartAccountInspectionBatch)
	group.POST("/account-inspection/batches/preflight", h.PreflightAccountInspectionBatch)
	group.GET("/account-inspection/batches/:operationId", h.GetAccountInspectionBatch)
	group.POST("/account-inspection/batches/:operationId/execute", h.ExecuteAccountInspectionBatch)
	group.POST("/account-inspection/batches/:operationId/retry", h.RetryAccountInspectionBatch)
	group.POST("/account-inspection/batches/:operationId/retry-execute", h.RetryExecuteAccountInspectionBatch)
}
func inspectionBatchPage(c *gin.Context, total int) (int, int, accountInspectionPageInfo) {
	page := parseAccountInspectionQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	size := parseAccountInspectionQueryInt(c, "page_size", 20)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	start := (page - 1) * size
	if start > total {
		start = total
	}
	end := start + size
	if end > total {
		end = total
	}
	return start, end, proinspection.ResultPageInfo(total, page, size)
}

var errInspectionBatchIdentityChanged = errors.New("batch account identity changed or unavailable")
var errInspectionBatchSuggestionChanged = errors.New("batch suggestion changed or already processed")
var errInspectionBatchStateChanged = errors.New("batch account state changed during execution")
var errInspectionBatchAccountAbsent = errors.New("batch account already absent")

// Batch selection identifies a registration, not an observation or token. Legacy
// callers can recover that identity from retained evidence, but never from a
// filename alone. Retry retains the same identity and confirmed action/effect.
func (s *accountInspectionScheduler) bindCurrentBatchItem(item accountInspectionActionItem) (accountInspectionActionItem, error) {
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil {
		return item, errInspectionBatchIdentityChanged
	}
	var selected, latest accountInspectionResult
	s.mu.Lock()
	for _, result := range s.status.Results {
		if result.Key == item.Key {
			latest = result
		}
		if item.ResultRef != "" && result.ResultRef == item.ResultRef && result.Key == item.Key {
			selected = result
		}
	}
	if selected.ResultRef == "" && item.ResultRef != "" {
		for _, result := range s.history {
			if result.ResultRef == item.ResultRef && result.Key == item.Key {
				selected = result
				break
			}
		}
	}
	s.mu.Unlock()
	if item.RegistrationEpoch == "" {
		item.RegistrationEpoch = selected.RegistrationEpoch
	}
	if item.Key == "" || item.AuthIndex == "" || item.RegistrationEpoch == "" {
		return item, errInspectionBatchIdentityChanged
	}
	if item.Suggested && item.ConfirmedEffect == "" && selected.ResultRef != "" {
		confirmed := proinspection.ActionItemFromResult(selected, item.Action)
		confirmed.Suggested = true
		item.ConfirmedEffect = inspectionBatchEffect("action", confirmed)
	}
	auth := s.h.authByIndex(item.AuthIndex)
	if auth == nil {
		// A replacement with a different index must not look like a missing file.
		for _, candidate := range s.inspectionAuthManager().List() {
			if candidate != nil && accountFromAuth(candidate).FileName == item.FileName {
				return item, errInspectionBatchIdentityChanged
			}
		}
		return item, errInspectionBatchAccountAbsent
	}
	identity := accountFromAuth(auth).baseResult()
	if identity.RegistrationEpoch != item.RegistrationEpoch || identity.Key != item.Key ||
		identity.FileName != item.FileName || !strings.EqualFold(identity.Provider, item.Provider) {
		return item, errInspectionBatchIdentityChanged
	}
	result := latest
	if result.Key == "" || result.RegistrationEpoch != identity.RegistrationEpoch {
		result = identity
	}
	// Refresh only identity/current-state fields; keep the current diagnosis for
	// suggested-action validation and audit continuity.
	result.AuthID, result.AuthIndex = identity.AuthID, identity.AuthIndex
	result.RegistrationEpoch, result.AccessTokenSHA256 = identity.RegistrationEpoch, identity.AccessTokenSHA256
	result.Disabled = auth.Disabled
	s.fillQuotaProtectionResult(auth, &result)
	bound := proinspection.ActionItemFromResult(result, item.Action)
	bound.BatchCurrent, bound.Suggested, bound.ConfirmedEffect = true, item.Suggested, item.ConfirmedEffect
	if item.Suggested && (latest.ResultRef == "" || latest.RegistrationEpoch != identity.RegistrationEpoch ||
		latest.Executed || latest.Action != item.Action || item.ConfirmedEffect == "" ||
		inspectionBatchEffect("action", bound) != item.ConfirmedEffect) {
		return item, errInspectionBatchSuggestionChanged
	}
	return bound, nil
}

func inspectionBatchConflict(err error) bool {
	return errors.Is(err, errAccountInspectionResultStale) || errors.Is(err, errInspectionBatchIdentityChanged) ||
		errors.Is(err, errInspectionBatchSuggestionChanged) || errors.Is(err, errInspectionBatchStateChanged)
}

func (h *Handler) preflightInspectionBatch(kind string, items []accountInspectionActionItem, _ bool) (*inspectionBatchOperation, error) {
	if kind != "inspect" && kind != "action" && kind != "recover" {
		return nil, errors.New("invalid batch kind")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s batch requires at least 1 item", kind)
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
		requested.BatchCurrent = true
		if kind != "action" {
			requested.Suggested = false
		}
		bound, bindErr := scheduler.bindCurrentBatchItem(requested)
		entry.Item = bound
		if errors.Is(bindErr, errInspectionBatchAccountAbsent) && kind == "action" && !requested.Suggested && requested.Action == accountInspectionActionDelete {
			entry.Effect = "delete"
			operation.Items = append(operation.Items, entry)
			continue
		}
		if bindErr != nil {
			entry.Status, entry.Error = "stale", bindErr.Error()
			operation.Items = append(operation.Items, entry)
			continue
		}
		entry.Key = bound.Key
		before := inspectionEvidenceResult(bound.ToResult())
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
			quotaSuggestion := kind == "action" && bound.Suggested && bound.Action == accountInspectionActionEnable && bound.QuotaCooling
			if quotaSuggestion {
				hold, exists := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
				if !exists || hold.Revision != bound.QuotaRevision {
					entry.Status, entry.Error = "stale", errAccountInspectionResultStale.Error()
				} else {
					detail := inspectionBoardDetail(hold)
					entry.recovery = append(entry.recovery, routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: detail.Source, Model: detail.Model, Revision: detail.Revision})
				}
			} else if kind == "recover" {
				board := schedulingBoardAccount(auth, now)
				for _, detail := range board.Details {
					if detail.Source != "inspection" && detail.Source != "upstream" {
						continue
					}
					entry.recovery = append(entry.recovery, routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: detail.Source, Model: detail.Model, Revision: detail.Revision})
				}
			}
			if entry.Status == "ready" && (quotaSuggestion || kind == "recover") && auth.Disabled {
				entry.Status, entry.Error = "unsupported", "account is disabled; scheduling recovery unavailable"
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

func (store *inspectionBatchStore) operationForClientRequestID(clientRequestID string) *inspectionBatchOperation {
	if clientRequestID == "" {
		return nil
	}
	for _, operation := range store.operations {
		if operation.ClientRequestID == clientRequestID {
			return operation
		}
	}
	return nil
}

func inspectionBatchReadyCount(operation *inspectionBatchOperation) int {
	count := 0
	for _, item := range operation.Items {
		if item.Status == "ready" {
			count++
		}
	}
	return count
}

func inspectionBatchRequestFingerprint(request inspectionBatchRequest) (string, error) {
	payload, err := json.Marshal(struct {
		Kind  string                        `json:"kind"`
		Items []accountInspectionActionItem `json:"items,omitempty"`
		Scope *inspectionBatchScope         `json:"scope,omitempty"`
	}{Kind: request.Kind, Items: request.Items, Scope: request.Scope})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func inspectionBatchIdempotencyConflict(operation *inspectionBatchOperation, requestFingerprint string) bool {
	return operation.RequestFingerprint != "" && operation.RequestFingerprint != requestFingerprint
}

func (h *Handler) resolveInspectionBatchItems(request inspectionBatchRequest) ([]accountInspectionActionItem, int, error) {
	if request.Scope == nil {
		return request.Items, 0, nil
	}
	scope := request.Scope
	switch scope.Type {
	case "selected":
		return scope.Items, 0, nil
	case "filtered":
		scheduler := schedulerForHandler(h)
		if scheduler == nil {
			return nil, http.StatusServiceUnavailable, errors.New("scheduler unavailable")
		}
		scheduler.mu.Lock()
		pageSize := len(scheduler.status.Results)
		if pageSize < 1 {
			pageSize = 1
		}
		results, _ := proinspection.PaginateResults(scheduler.status.Results, 1, pageSize, pageSize, scope.Filter, scope.PendingOnly || request.Kind == "action" && scope.Suggested, scope.Provider, scope.Search)
		items := make([]accountInspectionActionItem, 0, len(results))
		for _, result := range results {
			action := scope.Action
			if scope.Suggested {
				action = result.Action
			}
			item := proinspection.ActionItemFromResult(result, action)
			item.Suggested = scope.Suggested
			items = append(items, item)
		}
		scheduler.mu.Unlock()
		return items, 0, nil
	default:
		return nil, http.StatusBadRequest, errors.New("invalid scope type")
	}
}

func (h *Handler) StartAccountInspectionBatch(c *gin.Context) {
	var request inspectionBatchRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	request.ClientRequestID = strings.TrimSpace(request.ClientRequestID)
	if request.ClientRequestID == "" || len(request.ClientRequestID) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "clientRequestId is required and must not exceed 200 characters"})
		return
	}
	requestFingerprint, err := inspectionBatchRequestFingerprint(request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid batch request"})
		return
	}
	store := inspectionBatches(h)
	store.Lock()
	if store.loadErr != nil {
		store.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation receipts could not be loaded"})
		return
	}
	if existing := store.operationForClientRequestID(request.ClientRequestID); existing != nil {
		if inspectionBatchIdempotencyConflict(existing, requestFingerprint) {
			store.Unlock()
			c.JSON(http.StatusConflict, gin.H{"error": "clientRequestId was already used for a different batch request"})
			return
		}
		response := existing.snapshot()
		store.Unlock()
		c.Data(http.StatusAccepted, "application/json", response)
		return
	}
	store.Unlock()

	items, status, err := h.resolveInspectionBatchItems(request)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	operation, err := h.preflightInspectionBatch(request.Kind, items, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if inspectionBatchReadyCount(operation) == 0 {
		operation.snapshot()
		c.JSON(http.StatusConflict, gin.H{"error": "batch has no executable targets", "items": operation.Items, "summary": operation.Summary})
		return
	}
	operation.ClientRequestID = request.ClientRequestID
	operation.RequestFingerprint = requestFingerprint
	operation.State = "running"

	store.Lock()
	if existing := store.operationForClientRequestID(request.ClientRequestID); existing != nil {
		if inspectionBatchIdempotencyConflict(existing, requestFingerprint) {
			store.Unlock()
			c.JSON(http.StatusConflict, gin.H{"error": "clientRequestId was already used for a different batch request"})
			return
		}
		response := existing.snapshot()
		store.Unlock()
		c.Data(http.StatusAccepted, "application/json", response)
		return
	}
	if !store.insert(operation) {
		store.Unlock()
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "batch receipt capacity reached; retry after retention expires"})
		return
	}
	if err := store.save(); err != nil {
		delete(store.operations, operation.OperationID)
		store.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist execution intent"})
		return
	}
	response := operation.snapshot()
	store.Unlock()
	go h.runInspectionBatch(store, operation)
	c.Data(http.StatusAccepted, "application/json", response)
}

func (h *Handler) PreflightAccountInspectionBatch(c *gin.Context) {
	var request inspectionBatchRequest
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	items, status, err := h.resolveInspectionBatchItems(request)
	if err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	operation, err := h.preflightInspectionBatch(request.Kind, items, false)
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
	h.retryAccountInspectionBatch(c, false)
}

func (h *Handler) RetryExecuteAccountInspectionBatch(c *gin.Context) {
	h.retryAccountInspectionBatch(c, true)
}

func (h *Handler) retryAccountInspectionBatch(c *gin.Context, execute bool) {
	store := inspectionBatches(h)
	store.Lock()
	parent := store.operations[c.Param("operationId")]
	if parent == nil {
		store.Unlock()
		c.JSON(404, gin.H{"error": "operation not found"})
		return
	}
	if parent.State != "completed" && parent.State != "interrupted" {
		store.Unlock()
		c.JSON(409, gin.H{"error": "operation must complete before retry"})
		return
	}
	if previous := store.operations[parent.RetryID]; previous != nil {
		if !execute {
			response := previous.snapshot()
			store.Unlock()
			c.Data(http.StatusOK, "application/json", response)
			return
		}
		if previous.State == "interrupted" {
			store.Unlock()
			c.JSON(http.StatusConflict, gin.H{"error": "interrupted operation cannot be replayed; inspect current state and retry explicitly"})
			return
		}
		if previous.State == "prepared" {
			if inspectionBatchReadyCount(previous) == 0 {
				previous.snapshot()
				store.Unlock()
				c.JSON(http.StatusConflict, gin.H{"error": "batch has no executable targets", "items": previous.Items, "summary": previous.Summary})
				return
			}
			if time.Now().UnixMilli() > previous.ExpiresAt {
				store.Unlock()
				c.JSON(http.StatusConflict, gin.H{"error": "retry preflight expired; prepare a new retry"})
				return
			}
			previous.State = "running"
			if err := store.save(); err != nil {
				previous.State = "prepared"
				store.Unlock()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist execution intent"})
				return
			}
			response := previous.snapshot()
			store.Unlock()
			go h.runInspectionBatch(store, previous)
			c.Data(http.StatusAccepted, "application/json", response)
			return
		}
		response := previous.snapshot()
		store.Unlock()
		c.Data(http.StatusAccepted, "application/json", response)
		return
	}
	items := make([]accountInspectionActionItem, 0)
	for _, item := range parent.Items {
		if item.Status == "failed" || item.Status == "stale" {
			items = append(items, item.Item)
		}
	}
	if len(items) == 0 {
		store.Unlock()
		c.JSON(409, gin.H{"error": "operation has no failed items"})
		return
	}
	parentID, parentKind := parent.OperationID, parent.Kind
	store.Unlock()

	operation, err := h.preflightInspectionBatch(parentKind, items, true)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if execute && inspectionBatchReadyCount(operation) == 0 {
		operation.snapshot()
		c.JSON(http.StatusConflict, gin.H{"error": "batch has no executable targets", "items": operation.Items, "summary": operation.Summary})
		return
	}

	store.Lock()
	parent = store.operations[parentID]
	if parent == nil {
		store.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "operation not found"})
		return
	}
	if previous := store.operations[parent.RetryID]; previous != nil {
		if execute {
			if previous.State == "interrupted" {
				store.Unlock()
				c.JSON(http.StatusConflict, gin.H{"error": "interrupted operation cannot be replayed; inspect current state and retry explicitly"})
				return
			}
			if previous.State == "prepared" {
				if inspectionBatchReadyCount(previous) == 0 {
					previous.snapshot()
					store.Unlock()
					c.JSON(http.StatusConflict, gin.H{"error": "batch has no executable targets", "items": previous.Items, "summary": previous.Summary})
					return
				}
				if time.Now().UnixMilli() > previous.ExpiresAt {
					store.Unlock()
					c.JSON(http.StatusConflict, gin.H{"error": "retry preflight expired; prepare a new retry"})
					return
				}
				previous.State = "running"
				if err := store.save(); err != nil {
					previous.State = "prepared"
					store.Unlock()
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist execution intent"})
					return
				}
				response := previous.snapshot()
				store.Unlock()
				go h.runInspectionBatch(store, previous)
				c.Data(http.StatusAccepted, "application/json", response)
				return
			}
		}
		response := previous.snapshot()
		store.Unlock()
		status := http.StatusOK
		if execute {
			status = http.StatusAccepted
		}
		c.Data(status, "application/json", response)
		return
	}
	if !store.insert(operation) {
		store.Unlock()
		c.JSON(429, gin.H{"error": "batch receipt capacity reached"})
		return
	}
	parent.RetryID = operation.OperationID
	operation.ParentOperationID = parent.OperationID
	if execute {
		operation.State = "running"
	}
	if err := store.save(); err != nil {
		delete(store.operations, operation.OperationID)
		parent.RetryID = ""
		store.Unlock()
		c.JSON(500, gin.H{"error": "failed to persist operation"})
		return
	}
	response := operation.snapshot()
	store.Unlock()
	status := http.StatusOK
	if execute {
		status = http.StatusAccepted
		go h.runInspectionBatch(store, operation)
	}
	c.Data(status, "application/json", response)
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
			if inspectionBatchConflict(err) {
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
				if outcome.Error == errAccountInspectionResultStale.Error() || outcome.Error == errInspectionBatchIdentityChanged.Error() || outcome.Error == errInspectionBatchSuggestionChanged.Error() {
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
	bound, err := scheduler.bindCurrentBatchItem(entry.Item)
	if errors.Is(err, errInspectionBatchAccountAbsent) && kind == "action" && !entry.Item.Suggested && entry.Item.Action == accountInspectionActionDelete {
		return gin.H{"success": true, "noop": true, "accountState": "absent"}, nil
	}
	if err != nil {
		return nil, err
	}
	auth, err := scheduler.actionAuthForResult(bound.ToResult())
	if err != nil || auth.RegistrationEpoch != entry.epoch {
		return nil, errInspectionBatchIdentityChanged
	}
	entry.Item = bound
	suggestedQuotaRecovery := kind == "action" && entry.Item.Suggested && entry.Item.Action == accountInspectionActionEnable && entry.Item.QuotaCooling
	if suggestedQuotaRecovery {
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
		if auth.Disabled {
			return nil, errors.New("account is disabled; scheduling recovery unavailable")
		}
		if len(entry.recovery) == 0 {
			after := schedulingBoardAccount(auth, time.Now())
			if after.AuthID != "" {
				return gin.H{"after": after}, errInspectionBatchStateChanged
			}
			return gin.H{"success": true, "noop": true, "accountState": "unrestricted", "after": after}, nil
		}
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
		if failure == nil {
			if suggestedQuotaRecovery {
				for _, request := range entry.recovery {
					for _, detail := range after.Details {
						if detail.Source == request.Source && detail.Model == request.Model {
							failure = errors.New("check completed; suggested restriction remains active")
							break
						}
					}
				}
			} else if after.AuthID != "" {
				failure = errors.New("check completed; account remains restricted")
			}
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
	start, end, page := inspectionBatchPage(c, len(items))
	output := make([]json.RawMessage, 0, end-start)
	for _, operation := range items[start:end] {
		output = append(output, operation.snapshot())
	}
	c.JSON(200, gin.H{"items": output, "pageInfo": page, "retention": gin.H{"maxOperations": inspectionBatchCapacity, "maxAgeHours": 24}})
}

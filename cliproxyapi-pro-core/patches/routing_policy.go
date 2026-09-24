package management

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

var routingPolicyControllers sync.Map

type routingPolicyController struct {
	h      *Handler
	mu     sync.Mutex
	active map[string]struct{}
}

func beginRoutingRecovery(h *Handler, authID string) (func(), bool) {
	if h == nil || authID == "" {
		return nil, false
	}
	value, _ := routingPolicyControllers.LoadOrStore(h, &routingPolicyController{h: h})
	controller := value.(*routingPolicyController)
	controller.mu.Lock()
	if _, busy := controller.active[authID]; busy {
		controller.mu.Unlock()
		return nil, false
	}
	if controller.active == nil {
		controller.active = make(map[string]struct{})
	}
	controller.active[authID] = struct{}{}
	controller.mu.Unlock()
	return func() {
		controller.mu.Lock()
		delete(controller.active, authID)
		controller.mu.Unlock()
	}, true
}

type routingBoardSummary struct {
	Blocked          int   `json:"blocked"`
	Quota            int   `json:"quota"`
	AuthTransient    int   `json:"authTransient"`
	Recheck          int   `json:"recheck"`
	Overlap          int   `json:"overlap"`
	Excluded         int   `json:"excluded"`
	NextRetryAt      int64 `json:"nextRetryAt,omitempty"`
	NextActionAt     int64 `json:"nextActionAt,omitempty"`
	NextTransitionAt int64 `json:"nextTransitionAt,omitempty"`
}

type routingBoardDetail struct {
	Source     string `json:"source"`
	Scope      string `json:"scope"`
	Model      string `json:"model,omitempty"`
	Kind       string `json:"kind"`
	Resume     string `json:"resume"`
	RetryAt    int64  `json:"retryAt,omitempty"`
	Reason     string `json:"reason"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Revision   string `json:"revision,omitempty"`
}

type routingBoardAccount struct {
	Provider          string               `json:"provider"`
	AuthID            string               `json:"authId"`
	AuthIndex         string               `json:"authIndex"`
	RegistrationEpoch string               `json:"registrationEpoch"`
	FileName          string               `json:"fileName"`
	Scope             string               `json:"scope"`
	Models            []string             `json:"models,omitempty"`
	Kind              string               `json:"kind"`
	Bucket            string               `json:"bucket"`
	Sources           []string             `json:"sources"`
	Resume            string               `json:"resume"`
	RetryAt           int64                `json:"retryAt,omitempty"`
	NextActionAt      int64                `json:"nextActionAt,omitempty"`
	NextTransitionAt  int64                `json:"nextTransitionAt,omitempty"`
	RemainingSeconds  int64                `json:"remainingSeconds,omitempty"`
	Reason            string               `json:"reason"`
	HTTPStatus        int                  `json:"httpStatus,omitempty"`
	Inspection        bool                 `json:"inspection"`
	Overlap           bool                 `json:"overlap"`
	Details           []routingBoardDetail `json:"details"`
}

type routingPolicyResponse struct {
	GeneratedAt int64                 `json:"generatedAt"`
	Summary     routingBoardSummary   `json:"summary"`
	Accounts    []routingBoardAccount `json:"accounts"`
}

func startRoutingPolicyController(h *Handler) {
	if h == nil {
		return
	}
	controller := &routingPolicyController{h: h}
	if _, loaded := routingPolicyControllers.LoadOrStore(h, controller); loaded {
		return
	}
	clearLegacyRoutingQuotaProtections(h)
}

func stopRoutingPolicyController(h *Handler) {
	if h == nil {
		return
	}
	routingPolicyControllers.Delete(h)
}

func clearLegacyRoutingQuotaProtections(h *Handler) {
	if h == nil || h.authManager == nil {
		return
	}
	if err := h.authManager.SweepLegacyRoutingQuotaProtections(context.Background()); err != nil {
		log.WithError(err).Warn("failed to clear legacy routing quota protection")
	}
}

func (h *Handler) RegisterRoutingPolicyRoutes(group *gin.RouterGroup) {
	group.GET("/routing-policy", h.GetRoutingPolicy)
	group.PUT("/routing-policy", h.PutRoutingPolicy)
	group.PATCH("/routing-policy", h.PutRoutingPolicy)
	group.PUT("/routing-policy/request-protection", h.PutRoutingRequestProtection)
	group.POST("/routing-policy/release", h.ReleaseRoutingProtectedAuth)
	group.POST("/routing-policy/check", h.CheckRoutingAccount)
	group.POST("/routing-policy/restrictions/release", h.ReleaseRoutingRestriction)
}

func (h *Handler) GetRoutingPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, h.routingPolicyResponse())
}

func (h *Handler) PutRoutingPolicy(c *gin.Context) {
	writeRetiredRoutingPolicy(c)
}

func (h *Handler) PutRoutingRequestProtection(c *gin.Context) {
	writeRetiredRoutingPolicy(c)
}

func (h *Handler) ReleaseRoutingProtectedAuth(c *gin.Context) {
	writeRetiredRoutingPolicy(c)
}

func writeRetiredRoutingPolicy(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{"error": "routing policy writes are retired; use the scheduling recovery endpoints for directed checks and releases"})
}

type routingRecoveryRequest struct {
	AuthID            string `json:"authId"`
	AuthIndex         string `json:"authIndex"`
	RegistrationEpoch string `json:"registrationEpoch"`
	Source            string `json:"source,omitempty"`
	Model             string `json:"model,omitempty"`
	Revision          string `json:"revision,omitempty"`
}

func (h *Handler) routingRecoveryAuth(request routingRecoveryRequest) (*coreauth.Auth, int) {
	if h == nil || h.authManager == nil {
		return nil, http.StatusServiceUnavailable
	}
	epoch, err := strconv.ParseUint(request.RegistrationEpoch, 10, 64)
	if request.AuthID == "" || request.AuthIndex == "" || err != nil || epoch == 0 {
		return nil, http.StatusBadRequest
	}
	auth, ok := h.authManager.GetByID(request.AuthID)
	if !ok || auth == nil || auth.EnsureIndex() != request.AuthIndex || auth.RegistrationEpoch != epoch {
		return nil, http.StatusConflict
	}
	if auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return nil, http.StatusConflict
	}
	return auth, http.StatusOK
}

func routingRecoveryModel(auth *coreauth.Auth, requested string, details []routingBoardDetail) (string, error) {
	if requested != "" {
		return resolveAuthFileConnectionTestModel(auth, requested)
	}
	hasModelBlock := false
	for _, detail := range details {
		if detail.Source == "upstream" && detail.Scope == "model" && detail.Model != "" {
			hasModelBlock = true
			if model, err := resolveAuthFileConnectionTestModel(auth, detail.Model); err == nil {
				return model, nil
			}
		}
	}
	if hasModelBlock {
		return "", errors.New("restricted model has no supported text diagnostic request")
	}
	return resolveAuthFileConnectionTestModel(auth, "")
}

const routingRecoveryTimeout = 55 * time.Second

type routingRecoveryPhase struct {
	Source string `json:"source"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func failedRoutingPhase(source, model string, err error) routingRecoveryPhase {
	status := "failed"
	if errors.Is(err, context.DeadlineExceeded) {
		status = "timeout"
	}
	return routingRecoveryPhase{Source: source, Model: model, Status: status, Error: err.Error()}
}

// CheckRoutingAccount operates on the live restriction. Inspection-owned holds
// use the existing directed quota recovery; upstream failures use a pinned real
// request whose result is recorded by the native manager.
func (h *Handler) CheckRoutingAccount(c *gin.Context) {
	var request routingRecoveryRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	request.AuthID = strings.TrimSpace(request.AuthID)
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	request.Source = strings.TrimSpace(request.Source)
	auth, status := h.routingRecoveryAuth(request)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": "account is unavailable or has changed"})
		return
	}
	finish, acquired := beginRoutingRecovery(h, request.AuthID)
	if !acquired {
		c.JSON(http.StatusConflict, gin.H{"error": "recovery is already running for this account"})
		return
	}
	defer finish()
	observedEpoch := auth.RegistrationEpoch
	before := schedulingBoardAccount(auth, time.Now())
	if before.AuthID == "" {
		c.JSON(http.StatusConflict, gin.H{"error": "account is no longer restricted"})
		return
	}
	if request.Source != "" && request.Source != "inspection" && request.Source != "upstream" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid restriction source"})
		return
	}

	if request.Source != "" && !containsString(before.Sources, request.Source) {
		c.JSON(http.StatusConflict, gin.H{"error": "restriction source is no longer active"})
		return
	}

	if request.Source != "" && (request.Model != "" || request.Revision != "") {
		found := false
		for _, detail := range before.Details {
			if detail.Source == request.Source && (request.Model == "" && request.Revision == "" || detail.Model == request.Model) && (request.Revision == "" || detail.Revision == request.Revision) {
				found = true
				break
			}
		}
		if !found {
			c.JSON(http.StatusConflict, gin.H{"error": "restriction changed; refresh and retry"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), routingRecoveryTimeout)
	defer cancel()
	steps := make([]string, 0, 2)
	phases := make([]routingRecoveryPhase, 0, 2)
	var test *authFileConnectionTestResponse
	if ctx.Value(inspectionBatchAuditContextKey{}) == nil {
		if scheduler := schedulerForHandler(h); scheduler != nil {
			evidence := scheduler.currentInspectionEvidence(accountFromAuth(auth).Key)
			auditID, auditErr := scheduler.beginInspectionOperation("recovery", "recovery_check", evidence)
			if auditErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist recovery intent"})
				return
			}
			defer func() {
				after := scheduler.currentInspectionEvidence(evidence.Key)
				var operationErr error
				current, ok := h.authManager.GetByID(request.AuthID)
				if !ok || current == nil || current.RegistrationEpoch != observedEpoch || routingRecoveryScopeActive(schedulingBoardAccount(current, time.Now()), request) || c.Writer.Status() >= 400 {
					operationErr = errors.New("recovery did not complete")
				}
				for _, phase := range phases {
					if phase.Status == "failed" || phase.Status == "timeout" {
						operationErr = errors.New("recovery probe failed")
					}
				}
				if err := scheduler.finishInspectionOperation(auditID, &after, operationErr); err != nil {
					scheduler.appendLog("error", "recovery audit persistence failed")
				}
			}()
		}
	}
	writeResult := func() {
		current, ok := h.authManager.GetByID(request.AuthID)
		if !ok || current == nil || current.EnsureIndex() != request.AuthIndex || current.RegistrationEpoch != observedEpoch {
			c.JSON(http.StatusConflict, gin.H{"error": "account changed during recovery", "phases": phases})
			return
		}
		c.JSON(http.StatusOK, gin.H{"before": before, "after": schedulingBoardAccount(current, time.Now()), "steps": steps, "phases": phases, "test": test})
	}
	if request.Source == "" || request.Source == "inspection" {
		if _, exists := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]; exists {
			scheduler := schedulerForHandler(h)
			if scheduler == nil {
				phases = append(phases, failedRoutingPhase("inspection", "", errors.New("quota recovery is unavailable")))
				writeResult()
				return
			}
			if err := scheduler.checkQuotaRecoveryNow(ctx, auth); err != nil {
				phases = append(phases, failedRoutingPhase("inspection", "", err))
				writeResult()
				return
			}
			steps = append(steps, "inspection")
			phases = append(phases, routingRecoveryPhase{Source: "inspection", Status: "completed"})
			auth, _ = h.authManager.GetByID(request.AuthID)
			if auth == nil || auth.EnsureIndex() != request.AuthIndex || auth.RegistrationEpoch != observedEpoch {
				writeResult()
				return
			}
		}
	}
	_, stillHeld := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	currentBoard := schedulingBoardAccount(auth, time.Now())
	if (request.Source == "" && !stillHeld || request.Source == "upstream") && containsString(currentBoard.Sources, "upstream") {
		model, err := routingRecoveryModel(auth, request.Model, currentBoard.Details)
		if err != nil {
			phases = append(phases, failedRoutingPhase("upstream", request.Model, err))
			writeResult()
			return
		}
		if err := ctx.Err(); err != nil {
			phases = append(phases, failedRoutingPhase("upstream", model, err))
			writeResult()
			return
		}
		result := h.testAuthConnection(ctx, auth, model)
		test = &result
		steps = append(steps, "upstream")
		phase := routingRecoveryPhase{Source: "upstream", Model: model, Status: "completed"}
		if !result.Success {
			phase.Status, phase.Error = "failed", result.Error
			if result.ErrorCode == "timeout" {
				phase.Status = "timeout"
			}
		}
		phases = append(phases, phase)
	} else if request.Source == "" && stillHeld && containsString(currentBoard.Sources, "upstream") {
		phases = append(phases, routingRecoveryPhase{Source: "upstream", Status: "skipped", Error: "quota protection remains active; select the upstream restriction to check it separately"})
	}
	writeResult()
}

// A directed recovery only owns the observed restriction; unrelated model or
// source restrictions must not change the result of that operation.
func routingRecoveryScopeActive(board routingBoardAccount, request routingRecoveryRequest) bool {
	if request.Source == "" {
		return board.AuthID != ""
	}
	for _, detail := range board.Details {
		if detail.Source == request.Source && (request.Model == "" && request.Revision == "" || detail.Model == request.Model) && (request.Revision == "" || detail.Revision == request.Revision) {
			return true
		}
	}
	return false
}

func (s *accountInspectionScheduler) setRoutingOperationScope(id string, request routingRecoveryRequest, before, after *routingBoardAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.operations {
		if s.operations[i].OperationID != id {
			continue
		}
		state := func(board *routingBoardAccount) *proinspection.OperationRestriction {
			state := &proinspection.OperationRestriction{Source: inspectionEvidenceText(request.Source, 32), Model: inspectionEvidenceText(request.Model, 256), Revision: inspectionEvidenceText(request.Revision, 32)}
			for _, detail := range board.Details {
				if detail.Source == request.Source && detail.Model == request.Model {
					state.Active = true
					if after != nil {
						state.Revision = detail.Revision
					}
					break
				}
			}
			return state
		}
		if before != nil {
			s.operations[i].RestrictionBefore = state(before)
		}
		if after != nil {
			s.operations[i].RestrictionAfter = state(after)
		}
		break
	}
	return s.saveEvidenceLocked()
}

func (h *Handler) ReleaseRoutingRestriction(c *gin.Context) {
	var request routingRecoveryRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	request.AuthID = strings.TrimSpace(request.AuthID)
	request.AuthIndex = strings.TrimSpace(request.AuthIndex)
	request.Source = strings.TrimSpace(request.Source)
	auth, status := h.routingRecoveryAuth(request)
	if status != http.StatusOK {
		c.JSON(status, gin.H{"error": "account is unavailable or has changed"})
		return
	}
	finish, acquired := beginRoutingRecovery(h, request.AuthID)
	if !acquired {
		c.JSON(http.StatusConflict, gin.H{"error": "recovery is already running for this account"})
		return
	}
	defer finish()
	revision, parseErr := strconv.ParseInt(request.Revision, 10, 64)
	if parseErr != nil || revision <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "restriction revision is required"})
		return
	}
	if request.Source != "inspection" && request.Source != "upstream" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid restriction source"})
		return
	}
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "operation audit is unavailable"})
		return
	}
	before := scheduler.currentInspectionEvidence(accountFromAuth(auth).Key)
	auditID, auditErr := scheduler.beginInspectionOperation("recovery", "manual_release_"+request.Source, before)
	if auditErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist release intent"})
		return
	}
	boardBefore := schedulingBoardAccount(auth, time.Now())
	if err := scheduler.setRoutingOperationScope(auditID, request, &boardBefore, nil); err != nil {
		_ = scheduler.finishInspectionOperation(auditID, &before, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist release scope"})
		return
	}
	defer func() {
		after := scheduler.currentInspectionEvidence(before.Key)
		var operationErr error
		boardAfter := routingBoardAccount{}
		current, ok := h.authManager.GetByID(request.AuthID)
		if ok && current != nil {
			boardAfter = schedulingBoardAccount(current, time.Now())
		}
		if !ok || current == nil || current.RegistrationEpoch != auth.RegistrationEpoch || c.Writer.Status() >= 400 || routingRecoveryScopeActive(boardAfter, request) {
			operationErr = errors.New("restriction release did not complete")
		}
		if err := scheduler.setRoutingOperationScope(auditID, request, nil, &boardAfter); err != nil {
			scheduler.appendLog("error", "release scope audit persistence failed")
		}
		if err := scheduler.finishInspectionOperation(auditID, &after, operationErr); err != nil {
			scheduler.appendLog("error", "release audit persistence failed")
		}
	}()
	var err error
	switch request.Source {
	case "inspection":
		hold, ok := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
		if !ok || hold.Revision != revision || request.Model != strings.TrimSpace(hold.Model) {
			c.JSON(http.StatusConflict, gin.H{"error": "restriction changed; refresh and retry"})
			return
		}
		scheduler := schedulerForHandler(h)
		if scheduler != nil {
			release, lifecycleErr := scheduler.beginLifecycle()
			if lifecycleErr != nil {
				c.JSON(accountInspectionHTTPStatus(lifecycleErr), gin.H{"error": lifecycleErr.Error()})
				return
			}
			defer release()
		}
		err = h.authManager.ChangeQuotaProtection(c.Request.Context(), auth, inspectionQuotaSource, revision, nil)
		if err == nil && scheduler != nil {
			scheduler.publishManualQuotaRelease(auth.ID, before.ResultRef)
		}
	case "upstream":
		err = h.authManager.ClearSchedulingBlock(c.Request.Context(), auth, request.Model, revision)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid restriction source"})
		return
	}
	if err != nil {
		if errors.Is(err, coreauth.ErrQuotaProtectionChanged) || errors.Is(err, coreauth.ErrSchedulingBlockChanged) {
			c.JSON(http.StatusConflict, gin.H{"error": "restriction changed; refresh and retry"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	current, ok := h.authManager.GetByID(request.AuthID)
	if !ok || current == nil || current.RegistrationEpoch != auth.RegistrationEpoch {
		c.JSON(http.StatusConflict, gin.H{"error": "account changed during release"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"after": schedulingBoardAccount(current, time.Now())})
}

func (h *Handler) routingPolicyResponse() routingPolicyResponse {
	now := time.Now()
	response := routingPolicyResponse{
		GeneratedAt: now.UnixMilli(),
		Accounts:    []routingBoardAccount{},
	}
	if h == nil || h.authManager == nil {
		return response
	}
	for _, auth := range h.authManager.List() {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		if auth.Disabled || auth.Status == coreauth.StatusDisabled {
			response.Summary.Excluded++
			continue
		}
		account := schedulingBoardAccount(auth, now)
		if account.AuthID == "" {
			continue
		}
		response.Accounts = append(response.Accounts, account)
		response.Summary.Blocked++
		switch account.Bucket {
		case "overlap":
			response.Summary.Overlap++
		case "recheck":
			response.Summary.Recheck++
		case "quota":
			response.Summary.Quota++
		default:
			response.Summary.AuthTransient++
		}
		response.Summary.NextActionAt = earlierTimestamp(response.Summary.NextActionAt, account.NextActionAt)
		response.Summary.NextTransitionAt = earlierTimestamp(response.Summary.NextTransitionAt, account.NextTransitionAt)
		response.Summary.NextRetryAt = earlierTimestamp(response.Summary.NextRetryAt, account.RetryAt)
	}
	sort.Slice(response.Accounts, func(i, j int) bool {
		left, right := response.Accounts[i], response.Accounts[j]
		if left.RetryAt == right.RetryAt {
			if left.AuthIndex == right.AuthIndex {
				return left.AuthID < right.AuthID
			}
			return left.AuthIndex < right.AuthIndex
		}
		if left.RetryAt == 0 {
			return false
		}
		if right.RetryAt == 0 {
			return true
		}
		return left.RetryAt < right.RetryAt
	})
	return response
}

func schedulingBoardAccount(auth *coreauth.Auth, now time.Time) routingBoardAccount {
	details := schedulingBoardDetails(auth, now)
	if len(details) == 0 {
		return routingBoardAccount{}
	}
	sources := uniqueSortedStrings(mapSlice(details, func(detail routingBoardDetail) string { return detail.Source }))
	models := uniqueSortedStrings(compactStrings(mapSlice(details, func(detail routingBoardDetail) string { return detail.Model })))
	resume := ""
	kind := "transient"
	scope := "model"
	nextActionAt := int64(0)
	nextTransitionAt := int64(0)
	resumeSet := make(map[string]struct{})
	httpStatus := 0
	reason := ""
	for _, detail := range details {
		if detail.Resume != "" {
			resumeSet[detail.Resume] = struct{}{}
			resume = stricterResume(resume, detail.Resume)
		}
		kind = stricterKind(kind, detail.Kind)
		if detail.Scope == "credential" {
			scope = "credential"
		}
		if detail.RetryAt > 0 {
			if detail.Resume == "auto-expire" {
				if detail.RetryAt > now.UnixMilli() {
					nextTransitionAt = earlierTimestamp(nextTransitionAt, detail.RetryAt)
				}
			} else if detail.Resume == "recheck-quota" || detail.Resume == "probe-request" {
				nextActionAt = earlierTimestamp(nextActionAt, detail.RetryAt)
			}
		}
		if reason == "" || detail.Source == "inspection" && kind == "quota" {
			reason = detail.Reason
			httpStatus = detail.HTTPStatus
		}
	}
	if len(resumeSet) > 1 {
		resume = "multiple"
	}
	retryAt := earlierTimestamp(nextActionAt, nextTransitionAt)
	inspection := containsString(sources, "inspection")
	upstream := containsString(sources, "upstream")
	overlap := inspection && upstream
	account := routingBoardAccount{
		Provider:          strings.ToLower(strings.TrimSpace(auth.Provider)),
		AuthID:            auth.ID,
		AuthIndex:         auth.Index,
		RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10),
		FileName:          routingProtectionAuthFileName(auth),
		Scope:             scope,
		Models:            models,
		Kind:              kind,
		Sources:           sources,
		Resume:            resume,
		RetryAt:           retryAt,
		NextActionAt:      nextActionAt,
		NextTransitionAt:  nextTransitionAt,
		Reason:            reason,
		HTTPStatus:        httpStatus,
		Inspection:        inspection,
		Overlap:           overlap,
		Details:           details,
	}
	if retryAt > now.UnixMilli() {
		remaining := time.UnixMilli(retryAt).Sub(now)
		account.RemainingSeconds = int64((remaining + time.Second - 1) / time.Second)
	}
	switch {
	case overlap:
		account.Bucket = "overlap"
	case detailsContainResume(details, "recheck-quota"):
		account.Bucket = "recheck"
	case kind == "quota":
		account.Bucket = "quota"
	default:
		account.Bucket = "authTransient"
	}
	return account
}

func schedulingBoardDetails(auth *coreauth.Auth, now time.Time) []routingBoardDetail {
	details := make([]routingBoardDetail, 0)
	if hold, ok := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]; ok {
		details = append(details, inspectionBoardDetail(hold))
	}
	for _, view := range coreauth.SchedulingBlockSnapshotForAuth(auth, now) {
		detail := upstreamBoardDetail(view)
		if detail.Scope == "credential" {
			detail.Revision = strconv.FormatInt(auth.UpdatedAt.UnixNano(), 10)
		} else if state := auth.ModelStates[view.ModelKey]; state != nil {
			detail.Revision = strconv.FormatInt(state.UpdatedAt.UnixNano(), 10)
		}
		details = append(details, detail)
	}
	return details
}

func inspectionBoardDetail(hold prorouting.QuotaProtection) routingBoardDetail {
	detail := routingBoardDetail{
		Source:   "inspection",
		Scope:    "credential",
		Kind:     "quota",
		Resume:   "recheck-quota",
		Reason:   strings.TrimSpace(hold.Reason),
		Revision: strconv.FormatInt(hold.Revision, 10),
	}
	if hold.Model != "" {
		detail.Scope = "model"
		detail.Model = hold.Model
	}
	if !hold.Recheck {
		if hold.RetryAt == 0 {
			detail.Resume = "manual"
		} else {
			detail.Resume = "probe-request"
		}
	}
	if hold.RetryAt > 0 {
		detail.RetryAt = hold.RetryAt
	}
	if detail.Reason == "" {
		detail.Reason = "inspection quota protection"
	}
	return detail
}

func upstreamBoardDetail(view coreauth.SchedulingBlockView) routingBoardDetail {
	detail := routingBoardDetail{
		Source:     "upstream",
		Scope:      "credential",
		Kind:       upstreamBoardKind(view.Reason),
		Resume:     upstreamBoardResume(view),
		Reason:     view.Reason,
		HTTPStatus: view.HTTPStatus,
	}
	if view.Scope == "model" || strings.TrimSpace(view.ModelKey) != "" {
		detail.Scope = "model"
		detail.Model = view.ModelKey
	}
	if view.RetryAt.IsZero() {
		detail.RetryAt = 0
	} else {
		detail.RetryAt = view.RetryAt.UnixMilli()
	}
	return detail
}

func upstreamBoardKind(reason string) string {
	switch strings.TrimSpace(reason) {
	case "quota", "credential_quota":
		return "quota"
	case "unauthorized", "payment_required", "invalid_grant", "token_expired":
		return "auth"
	case "model_not_supported", "not_found", "model_disabled":
		return "model"
	default:
		return "transient"
	}
}

func upstreamBoardResume(view coreauth.SchedulingBlockView) string {
	if !view.RetryAt.IsZero() {
		return "auto-expire"
	}
	switch strings.TrimSpace(view.Reason) {
	case "credential_disabled", "model_disabled", "model_not_supported", "not_found", "payment_required":
		return "manual"
	case "unauthorized", "invalid_grant":
		return "reauthenticate"
	case "token_expired":
		return "refresh-token"
	default:
		return "await-state-change"
	}
}

func stricterResume(current, next string) string {
	return pickByRank(current, next, map[string]int{
		"auto-expire":        1,
		"await-state-change": 2,
		"refresh-token":      3,
		"probe-request":      4,
		"recheck-quota":      5,
		"reauthenticate":     6,
		"manual":             7,
	})
}

func stricterKind(current, next string) string {
	return pickByRank(current, next, map[string]int{
		"transient": 1,
		"model":     2,
		"auth":      3,
		"quota":     4,
	})
}

func pickByRank(current, next string, rank map[string]int) string {
	if rank[next] > rank[current] {
		return next
	}
	if current == "" {
		return next
	}
	return current
}

func routingProtectionAuthFileName(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, candidate := range []string{
		auth.FileName,
		authAttribute(auth, coreauth.AttributeVirtualSource),
		authAttribute(auth, "path"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		name := filepath.Base(filepath.Clean(candidate))
		if name != "" && name != "." && name != string(filepath.Separator) {
			return name
		}
	}
	return ""
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func mapSlice[T any, R any](values []T, project func(T) R) []R {
	out := make([]R, 0, len(values))
	for _, value := range values {
		out = append(out, project(value))
	}
	return out
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func earlierTimestamp(current, candidate int64) int64 {
	if candidate <= 0 {
		return current
	}
	if current <= 0 || candidate < current {
		return candidate
	}
	return current
}

func detailsContainResume(details []routingBoardDetail, wanted string) bool {
	for _, detail := range details {
		if detail.Resume == wanted {
			return true
		}
	}
	return false
}

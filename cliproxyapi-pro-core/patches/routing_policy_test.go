package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRoutingRecoveryChecksPinnedModelAndClearsNativeCooldown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	executor := &authFileConnectionExecutor{response: []byte(`{"choices":[{"message":{"content":"OK"}}]}`)}
	manager.RegisterExecutor(executor)
	model := authFileConnectionTestModels(&coreauth.Auth{Provider: "codex"})[0].ID
	now := time.Now()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "recover-model", Provider: "codex", FileName: "recover.json",
		ModelStates: map[string]*coreauth.ModelState{
			model: {Unavailable: true, Status: coreauth.StatusError, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10)})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if executor.lastAuthID != auth.ID || executor.lastModel != model {
		t.Fatalf("pinned request = %s/%s", executor.lastAuthID, executor.lastModel)
	}
	var response struct {
		After routingBoardAccount            `json:"after"`
		Test  authFileConnectionTestResponse `json:"test"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Test.Success || response.After.AuthID != "" {
		t.Fatalf("recovery = %+v body = %s", response, recorder.Body.String())
	}
}

func TestRoutingRecoveryReleaseOnlySelectedSourceAndRejectsStaleRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	now := time.Now()
	auth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "overlap-release", Provider: "codex", FileName: "overlap.json",
		Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0,
		&prorouting.QuotaProtection{RetryAt: now.Add(2 * time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(auth.ID)
	before := schedulingBoardAccount(current, time.Now())
	var inspectionRevision, upstreamRevision string
	for _, detail := range before.Details {
		if detail.Source == "inspection" {
			inspectionRevision = detail.Revision
		}
		if detail.Source == "upstream" {
			upstreamRevision = detail.Revision
		}
	}
	if inspectionRevision == "" || upstreamRevision == "" {
		t.Fatalf("missing revisions: %+v", before.Details)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	send := func(request routingRecoveryRequest) *httptest.ResponseRecorder {
		body, _ := json.Marshal(request)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/restrictions/release", strings.NewReader(string(body))))
		return recorder
	}
	base := routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection", Revision: inspectionRevision}
	if recorder := send(base); recorder.Code != http.StatusOK {
		t.Fatalf("inspection release status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder := send(base); recorder.Code != http.StatusConflict {
		t.Fatalf("stale release status = %d", recorder.Code)
	}
	remaining, _ := manager.GetByID(auth.ID)
	if board := schedulingBoardAccount(remaining, time.Now()); board.AuthID == "" || board.Inspection || !containsString(board.Sources, "upstream") {
		t.Fatalf("release changed native cooldown: %+v", board)
	}
	if recorder := send(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Revision: upstreamRevision}); recorder.Code != http.StatusConflict {
		t.Fatalf("stale native release status = %d", recorder.Code)
	}
	for _, detail := range schedulingBoardAccount(remaining, time.Now()).Details {
		if detail.Source == "upstream" {
			upstreamRevision = detail.Revision
		}
	}
	if recorder := send(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "upstream", Revision: upstreamRevision}); recorder.Code != http.StatusOK {
		t.Fatalf("native release status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	remaining, _ = manager.GetByID(auth.ID)
	if board := schedulingBoardAccount(remaining, time.Now()); board.AuthID != "" {
		t.Fatalf("cooldown not cleared: %+v", board)
	}
}

func TestRoutingRecoveryRejectsReplacedAccountWithSameIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	old, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "replaced-recovery", Provider: "codex", FileName: "same.json",
		Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch := strconv.FormatUint(old.RegistrationEpoch, 10)
	newAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: old.ID, Provider: old.Provider, FileName: old.FileName,
		Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.EnsureIndex() != newAuth.EnsureIndex() {
		t.Fatal("test account index changed")
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: old.ID, AuthIndex: old.EnsureIndex(), RegistrationEpoch: oldEpoch})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("old account check status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestGetRoutingPolicyReturnsReadOnlyBoard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	quotaAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:             "quota-auth",
		Provider:       "codex",
		FileName:       "quota.json",
		Unavailable:    true,
		NextRetryAfter: now.Add(2 * time.Minute),
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: now.Add(2 * time.Minute),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectionAuth, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "inspection-auth",
		Provider: "claude",
		FileName: "inspection.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	excluded, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "disabled-auth",
		Provider: "xai",
		FileName: "disabled.json",
		Disabled: true,
		Status:   coreauth.StatusDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = quotaAuth
	_ = excluded
	hold := prorouting.QuotaProtection{Recheck: true, RetryAt: now.Add(5 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"}
	if err = manager.ChangeQuotaProtection(ctx, inspectionAuth, inspectionQuotaSource, 0, &hold); err != nil {
		t.Fatal(err)
	}

	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/routing-policy", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response routingPolicyResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Summary.Blocked != 2 || response.Summary.Quota != 1 || response.Summary.Recheck != 1 || response.Summary.Excluded != 1 {
		t.Fatalf("summary = %+v", response.Summary)
	}
	if len(response.Accounts) != 2 {
		t.Fatalf("accounts = %#v", response.Accounts)
	}
}

func TestRetiredRoutingPolicyWritesAreGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/routing-policy"},
		{http.MethodPatch, "/routing-policy"},
		{http.MethodPut, "/routing-policy/request-protection"},
		{http.MethodPost, "/routing-policy/release"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(target.method, target.path, nil))
		if recorder.Code != http.StatusGone {
			t.Fatalf("%s %s status = %d", target.method, target.path, recorder.Code)
		}
	}
}

func TestSchedulingBoardSeparatesInspectionActionFromUpstreamTransition(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{
		ID:             "overlap-auth",
		Provider:       "claude",
		FileName:       "overlap.json",
		Unavailable:    true,
		NextRetryAfter: now.Add(time.Minute),
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: now.Add(time.Minute),
		},
		Metadata: map[string]any{},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: now.Add(10 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "multiple" || account.Bucket != "overlap" || !account.Overlap {
		t.Fatalf("account = %+v", account)
	}
	if account.NextActionAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("next action at = %d", account.NextActionAt)
	}
	if account.NextTransitionAt != now.Add(time.Minute).UnixMilli() || account.RetryAt != account.NextTransitionAt {
		t.Fatalf("transition/retry = %d/%d", account.NextTransitionAt, account.RetryAt)
	}
}

func TestSchedulingBoardKeepsDueInspectionActionSeparateFromLaterCooldown(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{
		ID: "due-overlap", Provider: "claude", FileName: "due-overlap.json",
		Unavailable: true, NextRetryAfter: now.Add(10 * time.Minute),
		Metadata: map[string]any{},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.NextActionAt != due || account.NextTransitionAt != now.Add(10*time.Minute).UnixMilli() {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardUsesEarliestKnownModelTransition(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "multi-model", Provider: "codex", ModelStates: map[string]*coreauth.ModelState{
		"model-a": {Unavailable: true, NextRetryAfter: now.Add(time.Minute)},
		"model-b": {Unavailable: true, NextRetryAfter: now.Add(5 * time.Minute)},
	}}
	account := schedulingBoardAccount(auth, now)
	if account.NextTransitionAt != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("account = %+v", account)
	}
	if len(account.Details) != 2 || account.Details[0].RetryAt == account.Details[1].RetryAt {
		t.Fatalf("details = %+v", account.Details)
	}
}

func TestSchedulingBoardReportsUntimedUnavailableAccount(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "untimed", Provider: "codex", Unavailable: true}
	account := schedulingBoardAccount(auth, now)
	if account.AuthID != auth.ID || account.Resume != "await-state-change" || account.RetryAt != 0 || account.Bucket != "authTransient" {
		t.Fatalf("account = %+v", account)
	}
	if len(account.Details) != 1 || account.Details[0].Reason != "unavailable" {
		t.Fatalf("details = %+v", account.Details)
	}
}

func TestSchedulingBoardCredentialScopeOverridesModelList(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{
		ID: "scope-overlap", Provider: "claude", Metadata: map[string]any{},
		ModelStates: map[string]*coreauth.ModelState{
			"model-a": {Unavailable: true, NextRetryAfter: now.Add(time.Minute)},
		},
	}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: now.Add(2 * time.Minute).UnixMilli(), Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Scope != "credential" || !reflect.DeepEqual(account.Models, []string{"model-a"}) {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardKeepsManualInspectionHold(t *testing.T) {
	now := time.Now()
	auth := &coreauth.Auth{ID: "manual-auth", Provider: "codex", FileName: "manual.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "manual" || account.RetryAt != 0 || account.Bucket != "quota" {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardKeepsDueInspectionRecheckTime(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{ID: "due-auth", Provider: "claude", FileName: "due.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {Recheck: true, RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "recheck-quota" || account.RetryAt != due || account.NextActionAt != due || account.NextTransitionAt != 0 || account.Bucket != "recheck" {
		t.Fatalf("account = %+v", account)
	}
}

func TestSchedulingBoardKeepsDueProbeBlockedUntilRecoveryRuns(t *testing.T) {
	now := time.Now()
	due := now.Add(-time.Minute).UnixMilli()
	auth := &coreauth.Auth{ID: "probe-auth", Provider: "xai", FileName: "probe.json", Metadata: map[string]any{}}
	setQuotaProtectionsForTest(auth, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: {RetryAt: due, Reason: "inspection quota threshold"},
	})
	account := schedulingBoardAccount(auth, now)
	if account.Resume != "probe-request" || account.RetryAt != due || account.NextActionAt != due || account.NextTransitionAt != 0 || account.Bucket != "quota" {
		t.Fatalf("account = %+v", account)
	}
}

func TestClearLegacyRoutingQuotaProtectionsRemovesRetiredHolds(t *testing.T) {
	ctx := startProQuotaTestService(t)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "legacy-routing", Provider: "codex", FileName: "legacy.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{Recheck: true, RetryAt: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(auth.ID)
	setQuotaProtectionsForTest(current, map[string]prorouting.QuotaProtection{
		inspectionQuotaSource: prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource],
		"routing:gpt-test":    {Source: "routing:gpt-test", Model: "gpt-test", RetryAt: now.Add(time.Hour).UnixMilli(), Reason: "request protection"},
	})
	h := &Handler{authManager: manager}
	clearLegacyRoutingQuotaProtections(h)
	current, _ = manager.GetByID(auth.ID)
	got := prorouting.QuotaProtections(current.Metadata)
	if _, ok := got["routing:gpt-test"]; ok {
		t.Fatalf("legacy routing hold remained: %#v", got)
	}
	if _, ok := got[inspectionQuotaSource]; !ok {
		t.Fatal("inspection hold was cleared")
	}
}

func TestClearLegacyRoutingQuotaProtectionsLeavesUnprotectedAuthsUnchanged(t *testing.T) {
	_ = startProQuotaTestService(t)
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "clean-routing", Provider: "codex", FileName: "clean.json"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := manager.GetByID(auth.ID)
	clearLegacyRoutingQuotaProtections(&Handler{authManager: manager})
	after, _ := manager.GetByID(auth.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy routing sweep mutated an unprotected auth")
	}
}

func TestSchedulingBoardCountsExcludedAndAuthTransient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now()
	manager := coreauth.NewManager(nil, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "disabled-auth", Provider: "xai", FileName: "disabled.json", Disabled: true, Status: coreauth.StatusDisabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID: "transient-auth", Provider: "codex", FileName: "transient.json",
		Unavailable: true, NextRetryAfter: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	response := h.routingPolicyResponse()
	if response.Summary.Excluded != 1 || response.Summary.AuthTransient != 1 || response.Summary.Blocked != 1 {
		t.Fatalf("summary = %+v", response.Summary)
	}
	if len(response.Accounts) != 1 || response.Accounts[0].Bucket != "authTransient" {
		t.Fatalf("accounts = %#v", response.Accounts)
	}
}

func setQuotaProtectionsForTest(auth *coreauth.Auth, protections map[string]prorouting.QuotaProtection) {
	raw, _ := json.Marshal(protections)
	if auth.Metadata == nil {
		auth.Metadata = map[string]any{}
	}
	auth.Metadata[prorouting.QuotaProtectionMetadataKey] = string(raw)
}

func TestUpdateProAuthSerializesInspectionPriority(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	registered, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "serialized-auth",
		Provider: "xai",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			close(firstEntered)
			<-releaseFirst
			setProAuthDisabledState(auth, true)
			auth.Metadata[prorouting.ProtectionMetadataKey] = map[string]any{"owner": prorouting.ProtectionOwner}
		})
	}()
	<-firstEntered
	inspectionDone := make(chan error, 1)
	go func() {
		inspectionDone <- h.updateProAuth(context.Background(), registered.Index, func(auth *coreauth.Auth) {
			setProAuthDisabledState(auth, false)
		})
	}()
	select {
	case err = <-inspectionDone:
		t.Fatalf("inspection mutation bypassed serialization: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err = <-inspectionDone; err != nil {
		t.Fatal(err)
	}
	updated, ok := manager.GetByID(registered.ID)
	if !ok || updated == nil {
		t.Fatal("updated auth missing")
	}
	if updated.Disabled || prorouting.ProtectionOwned(updated.Metadata) {
		t.Fatalf("inspection must win: disabled=%v metadata=%#v", updated.Disabled, updated.Metadata)
	}
}

func TestRoutingRecoveryRejectsDisappearedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(context.Background(), &coreauth.Auth{ID: "source-disappeared", Provider: "codex", Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{authManager: manager}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/check", strings.NewReader(string(body))))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRoutingInspectionReleaseImmediatelyUpdatesStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := startProQuotaTestService(t)
	t.Setenv("ACCOUNT_INSPECTION_SCHEDULE_PATH", t.TempDir()+"/schedule.json")
	t.Setenv("ACCOUNT_INSPECTION_SNAPSHOT_PATH", t.TempDir()+"/snapshot.json")
	manager := coreauth.NewManager(nil, nil, nil)
	auth, err := manager.Register(ctx, &coreauth.Auth{ID: "release-inspection-status", Provider: "codex", FileName: "release.json"})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, 0, &prorouting.QuotaProtection{RetryAt: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	auth, _ = manager.GetByID(auth.ID)
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	h := &Handler{authManager: manager}
	scheduler := newAccountInspectionScheduler(h, nil)
	accountInspectionSchedulers.Store(h, scheduler)
	t.Cleanup(func() { accountInspectionSchedulers.Delete(h) })
	result := accountFromAuth(auth).baseResult()
	scheduler.fillQuotaProtectionResult(auth, &result)
	result.IsQuota = true
	scheduler.status.Results = []accountInspectionResult{result}
	updates := make(chan accountInspectionLogStreamMessage, 4)
	scheduler.subscribers[updates] = struct{}{}
	router := gin.New()
	h.RegisterRoutingPolicyRoutes(router.Group("/"))
	router.GET("/account-inspection/status", h.GetAccountInspectionStatus)
	statusResult := func() accountInspectionResult {
		t.Helper()
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/account-inspection/status", nil))
		var response struct {
			Status accountInspectionStatus `json:"status"`
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("status GET=%d", recorder.Code)
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Status.Results) != 1 {
			t.Fatalf("results=%+v", response.Status.Results)
		}
		return response.Status.Results[0]
	}
	if !statusResult().QuotaCooling {
		t.Fatal("fixture has no inspection hold")
	}
	body, _ := json.Marshal(routingRecoveryRequest{AuthID: auth.ID, AuthIndex: auth.EnsureIndex(), RegistrationEpoch: strconv.FormatUint(auth.RegistrationEpoch, 10), Source: "inspection", Revision: strconv.FormatInt(hold.Revision, 10)})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/routing-policy/restrictions/release", strings.NewReader(string(body))))
	if recorder.Code != http.StatusOK {
		t.Fatalf("release=%d %s", recorder.Code, recorder.Body.String())
	}
	updated := statusResult()
	if updated.QuotaCooling || updated.QuotaRetryAt != 0 || updated.QuotaRevision != 0 || updated.IsQuota {
		t.Fatalf("stale status after release: %+v", updated)
	}
	select {
	case <-updates:
	default:
		t.Fatal("inspection status update was not broadcast")
	}
}

package management

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

var routingPolicyControllers sync.Map

type routingPolicyController struct {
	h *Handler
}

type routingBoardSummary struct {
	Blocked       int   `json:"blocked"`
	Quota         int   `json:"quota"`
	AuthTransient int   `json:"authTransient"`
	Recheck       int   `json:"recheck"`
	Overlap       int   `json:"overlap"`
	Excluded      int   `json:"excluded"`
	NextRetryAt   int64 `json:"nextRetryAt,omitempty"`
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
}

type routingBoardAccount struct {
	Provider         string               `json:"provider"`
	AuthID           string               `json:"authId"`
	AuthIndex        string               `json:"authIndex"`
	FileName         string               `json:"fileName"`
	Scope            string               `json:"scope"`
	Models           []string             `json:"models,omitempty"`
	Kind             string               `json:"kind"`
	Bucket           string               `json:"bucket"`
	Sources          []string             `json:"sources"`
	Resume           string               `json:"resume"`
	RetryAt          int64                `json:"retryAt,omitempty"`
	RemainingSeconds int64                `json:"remainingSeconds,omitempty"`
	Reason           string               `json:"reason"`
	HTTPStatus       int                  `json:"httpStatus,omitempty"`
	Inspection       bool                 `json:"inspection"`
	Overlap          bool                 `json:"overlap"`
	Details          []routingBoardDetail `json:"details"`
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
	c.JSON(http.StatusGone, gin.H{"error": "scheduling board is read-only; inspect and release quota protection from account inspection"})
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
		if account.RetryAt > 0 && (response.Summary.NextRetryAt == 0 || account.RetryAt < response.Summary.NextRetryAt) {
			response.Summary.NextRetryAt = account.RetryAt
		}
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
	resume := "auto-expire"
	kind := "transient"
	scope := "model"
	retryAt := int64(0)
	permanent := false
	httpStatus := 0
	reason := ""
	for _, detail := range details {
		resume = stricterResume(resume, detail.Resume)
		kind = stricterKind(kind, detail.Kind)
		if detail.Scope == "credential" {
			scope = "credential"
		}
		if detail.RetryAt == 0 && (detail.Resume == "manual" || detail.Resume == "recheck-quota") {
			permanent = true
		} else if !permanent && detail.RetryAt > retryAt {
			retryAt = detail.RetryAt
		}
		if reason == "" || detail.Source == "inspection" && kind == "quota" {
			reason = detail.Reason
			httpStatus = detail.HTTPStatus
		}
	}
	if permanent {
		retryAt = 0
	}
	inspection := containsString(sources, "inspection")
	upstream := containsString(sources, "upstream")
	overlap := inspection && upstream
	account := routingBoardAccount{
		Provider:   strings.ToLower(strings.TrimSpace(auth.Provider)),
		AuthID:     auth.ID,
		AuthIndex:  auth.Index,
		FileName:   routingProtectionAuthFileName(auth),
		Scope:      scope,
		Models:     models,
		Kind:       kind,
		Sources:    sources,
		Resume:     resume,
		RetryAt:    retryAt,
		Reason:     reason,
		HTTPStatus: httpStatus,
		Inspection: inspection,
		Overlap:    overlap,
		Details:    details,
	}
	if retryAt > now.UnixMilli() {
		remaining := time.UnixMilli(retryAt).Sub(now)
		account.RemainingSeconds = int64((remaining + time.Second - 1) / time.Second)
	}
	switch {
	case overlap:
		account.Bucket = "overlap"
	case resume == "recheck-quota":
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
	for _, view := range coreauth.CooldownSnapshotForAuth(auth, now) {
		details = append(details, upstreamBoardDetail(view))
	}
	return details
}

func inspectionBoardDetail(hold prorouting.QuotaProtection) routingBoardDetail {
	detail := routingBoardDetail{
		Source: "inspection",
		Scope:  "credential",
		Kind:   "quota",
		Resume: "recheck-quota",
		Reason: strings.TrimSpace(hold.Reason),
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

func upstreamBoardDetail(view coreauth.CooldownView) routingBoardDetail {
	detail := routingBoardDetail{
		Source:     "upstream",
		Scope:      "credential",
		Kind:       upstreamBoardKind(view.Reason),
		Resume:     "auto-expire",
		RetryAt:    view.RetryAt.UnixMilli(),
		Reason:     view.Reason,
		HTTPStatus: view.HTTPStatus,
	}
	if view.Scope == "model" || strings.TrimSpace(view.ModelKey) != "" {
		detail.Scope = "model"
		detail.Model = view.ModelKey
	}
	if view.RetryAt.IsZero() {
		detail.RetryAt = 0
	}
	return detail
}

func upstreamBoardKind(reason string) string {
	switch strings.TrimSpace(reason) {
	case "quota", "credential_quota":
		return "quota"
	case "unauthorized", "payment_required", "invalid_grant":
		return "auth"
	case "model_not_supported", "not_found":
		return "model"
	default:
		return "transient"
	}
}

func stricterResume(current, next string) string {
	return pickByRank(current, next, map[string]int{
		"auto-expire":   1,
		"probe-request": 2,
		"recheck-quota": 3,
		"manual":        4,
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

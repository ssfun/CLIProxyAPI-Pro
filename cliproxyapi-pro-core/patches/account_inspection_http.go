package management

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

var accountInspectionWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type accountInspectionPageInfo = proinspection.PageInfo
type accountInspectionSnapshotOptions = proinspection.SnapshotOptions

func (s *accountInspectionScheduler) snapshotWithOptions(options accountInspectionSnapshotOptions) gin.H {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gin.H{
		"schedule": s.schedule,
		"status":   s.streamStatusLocked(options),
	}
}

func accountInspectionRequestSnapshotOptions(c *gin.Context) accountInspectionSnapshotOptions {
	value := strings.ToLower(strings.TrimSpace(c.Query("details")))
	resultPageSize := parseAccountInspectionQueryInt(c, "result_page_size", 100)
	if strings.TrimSpace(c.Query("result_page_size")) == "" {
		resultPageSize = parseAccountInspectionQueryInt(c, "result_limit", resultPageSize)
	}
	logPageSize := parseAccountInspectionQueryInt(c, "log_page_size", 100)
	if strings.TrimSpace(c.Query("log_page_size")) == "" {
		logPageSize = parseAccountInspectionQueryInt(c, "log_limit", logPageSize)
	}
	return accountInspectionSnapshotOptions{
		IncludeDetails:    value != "0" && value != "false" && value != "summary",
		ResultPage:        parseAccountInspectionQueryInt(c, "result_page", 1),
		ResultPageSize:    resultPageSize,
		ResultFilter:      strings.ToLower(strings.TrimSpace(c.Query("result_filter"))),
		ResultPendingOnly: parseAccountInspectionQueryBool(c, "result_pending_only"),
		ResultProvider:    strings.ToLower(strings.TrimSpace(c.Query("result_provider"))),
		ResultSearch:      strings.TrimSpace(c.Query("result_search")),
		LogPage:           parseAccountInspectionQueryInt(c, "log_page", 1),
		LogPageSize:       logPageSize,
		LogLevel:          strings.ToLower(strings.TrimSpace(c.Query("log_level"))),
	}
}

func parseAccountInspectionQueryBool(c *gin.Context, key string) bool {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func parseAccountInspectionQueryInt(c *gin.Context, key string, fallback int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func firstNonNilError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func accountInspectionHTTPStatus(err error) int {
	switch {
	case errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly),
		errors.Is(err, errAccountInspectionResultStale),
		errors.Is(err, errAccountInspectionAlreadyRunning):
		return http.StatusConflict
	case errors.Is(err, proinspection.ErrPaused):
		return http.StatusLocked
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout
	default:
		return http.StatusInternalServerError
	}
}

func (s *accountInspectionScheduler) snapshotForRequest(c *gin.Context) gin.H {
	return s.snapshotWithOptions(accountInspectionRequestSnapshotOptions(c))
}

func (s *accountInspectionScheduler) healthCountsLocked() accountInspectionHealthCounts {
	if s.healthCounts.Total == len(s.status.Results) {
		return s.healthCounts
	}
	s.healthCounts = proinspection.ResultHealthCounts(s.status.Results)
	return s.healthCounts
}

func paginateAccountInspectionLogs(logs []accountInspectionLogEntry, page int, pageSize int, level string) ([]accountInspectionLogEntry, accountInspectionPageInfo) {
	return proinspection.PaginateLogs(logs, page, pageSize, accountInspectionMaxLogPageSize, level)
}

func paginateAccountInspectionResults(results []accountInspectionResult, page int, pageSize int, filter string, pendingOnly bool, provider string, search string) ([]accountInspectionResult, accountInspectionPageInfo) {
	return proinspection.PaginateResults(results, page, pageSize, accountInspectionMaxResultPageSize, filter, pendingOnly, provider, search)
}

func (s *accountInspectionScheduler) streamStatusLocked(options accountInspectionSnapshotOptions) accountInspectionStatus {
	healthCounts := s.healthCounts
	if options.IncludeDetails {
		healthCounts = s.healthCountsLocked()
	}
	s.status.RunSettings = s.lastRunSettings
	return proinspection.ProjectStatus(s.status, healthCounts, options, accountInspectionMaxResultPageSize, accountInspectionMaxLogPageSize)
}

func (s *accountInspectionScheduler) streamMessageLocked(messageType accountInspectionStreamMessageType, options accountInspectionSnapshotOptions, logEntry *accountInspectionLogEntry) accountInspectionLogStreamMessage {
	return accountInspectionLogStreamMessage{Type: messageType, Schedule: s.schedule, Status: s.streamStatusLocked(options), Log: logEntry}
}

func (s *accountInspectionScheduler) snapshotStreamMessageLocked(options accountInspectionSnapshotOptions) accountInspectionLogStreamMessage {
	return s.streamMessageLocked(accountInspectionStreamSnapshot, options, nil)
}

func (s *accountInspectionScheduler) statusStreamMessageLocked(includeDetails bool) accountInspectionLogStreamMessage {
	return s.streamMessageLocked(accountInspectionStreamStatus, accountInspectionSnapshotOptions{IncludeDetails: includeDetails}, nil)
}

func (s *accountInspectionScheduler) logStreamMessageLocked(entry accountInspectionLogEntry) accountInspectionLogStreamMessage {
	return s.streamMessageLocked(accountInspectionStreamLog, accountInspectionSnapshotOptions{}, &entry)
}

type accountInspectionBroadcast struct {
	subscribers []chan accountInspectionLogStreamMessage
	message     accountInspectionLogStreamMessage
}

func (broadcast accountInspectionBroadcast) send() {
	for _, subscriber := range broadcast.subscribers {
		select {
		case subscriber <- broadcast.message:
		default:
		}
	}
}

func (s *accountInspectionScheduler) subscribersLocked() []chan accountInspectionLogStreamMessage {
	subscribers := make([]chan accountInspectionLogStreamMessage, 0, len(s.subscribers))
	for subscriber := range s.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	return subscribers
}

func (s *accountInspectionScheduler) statusBroadcastLocked() accountInspectionBroadcast {
	return accountInspectionBroadcast{
		subscribers: s.subscribersLocked(),
		message:     s.statusStreamMessageLocked(false),
	}
}

func (s *accountInspectionScheduler) logBroadcastLocked(entry accountInspectionLogEntry) accountInspectionBroadcast {
	return accountInspectionBroadcast{
		subscribers: s.subscribersLocked(),
		message:     s.logStreamMessageLocked(entry),
	}
}

func (s *accountInspectionScheduler) subscribeLogs(options accountInspectionSnapshotOptions) (chan accountInspectionLogStreamMessage, accountInspectionLogStreamMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subscriber := make(chan accountInspectionLogStreamMessage, 16)
	s.subscribers[subscriber] = struct{}{}
	return subscriber, s.snapshotStreamMessageLocked(options)
}

func (s *accountInspectionScheduler) unsubscribeLogs(subscriber chan accountInspectionLogStreamMessage) {
	s.mu.Lock()
	delete(s.subscribers, subscriber)
	s.mu.Unlock()
}

func (s *accountInspectionScheduler) streamLogs(c *gin.Context) {
	responseHeader := http.Header{}
	for _, protocol := range strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",") {
		protocol = strings.TrimSpace(protocol)
		if strings.HasPrefix(protocol, "cpa-management.") {
			responseHeader.Set("Sec-WebSocket-Protocol", protocol)
			break
		}
	}
	conn, err := accountInspectionWebSocketUpgrader.Upgrade(c.Writer, c.Request, responseHeader)
	if err != nil {
		return
	}
	defer conn.Close()

	done := make(chan struct{})
	go readAccountInspectionWebSocket(conn, done)

	subscriber, snapshot := s.subscribeLogs(accountInspectionRequestSnapshotOptions(c))
	defer s.unsubscribeLogs(subscriber)

	pingTicker := time.NewTicker(accountInspectionWebSocketPingPeriod)
	defer pingTicker.Stop()

	if err := writeAccountInspectionWebSocketMessage(conn, snapshot); err != nil {
		return
	}
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-done:
			return
		case <-pingTicker.C:
			if err := writeAccountInspectionWebSocketPing(conn); err != nil {
				return
			}
		case message, ok := <-subscriber:
			if !ok {
				return
			}
			if err := writeAccountInspectionWebSocketMessage(conn, message); err != nil {
				return
			}
		}
	}
}

func readAccountInspectionWebSocket(conn *websocket.Conn, done chan<- struct{}) {
	defer close(done)
	_ = conn.SetReadDeadline(time.Now().Add(accountInspectionWebSocketPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(accountInspectionWebSocketPongWait))
	})
	for {
		if _, _, err := conn.NextReader(); err != nil {
			return
		}
	}
}

func writeAccountInspectionWebSocketPing(conn *websocket.Conn) error {
	_ = conn.SetWriteDeadline(time.Now().Add(accountInspectionWebSocketWriteTimeout))
	return conn.WriteMessage(websocket.PingMessage, nil)
}

func writeAccountInspectionWebSocketMessage(conn *websocket.Conn, message accountInspectionLogStreamMessage) error {
	_ = conn.SetWriteDeadline(time.Now().Add(accountInspectionWebSocketWriteTimeout))
	return conn.WriteJSON(message)
}

func (h *Handler) RegisterAccountInspectionRoutes(group *gin.RouterGroup) {
	h.RegisterAccountInspectionBatchRoutes(group)
	group.GET("/account-inspection/logs", h.StreamAccountInspectionLogs)
	group.GET("/account-inspection/schedule", h.GetAccountInspectionSchedule)
	group.PUT("/account-inspection/schedule", h.PutAccountInspectionSchedule)
	group.GET("/account-inspection/status", h.GetAccountInspectionStatus)
	group.POST("/account-inspection/run", h.RunAccountInspection)
	group.POST("/account-inspection/inspect-one", h.InspectOneAccount)
	group.POST("/account-inspection/inspect-many", h.InspectManyAccounts)
	group.POST("/account-inspection/refresh-token", h.RefreshAccountInspectionToken)
	group.POST("/account-inspection/pause", h.PauseAccountInspection)
	group.POST("/account-inspection/resume", h.ResumeAccountInspection)
	group.POST("/account-inspection/stop", h.StopAccountInspection)
	group.POST("/account-inspection/actions", h.ExecuteAccountInspectionActions)
}

func (h *Handler) InspectManyAccounts(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	var request accountInspectionManyRequest
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Items) == 0 || len(request.Items) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	outcomes, err := scheduler.inspectMany(c.Request.Context(), request.Items)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		c.JSON(accountInspectionHTTPStatus(err), gin.H{"error": err.Error(), "outcomes": outcomes, "schedule": snapshot["schedule"], "status": snapshot["status"]})
		return
	}
	c.JSON(http.StatusOK, gin.H{"outcomes": outcomes, "schedule": snapshot["schedule"], "status": snapshot["status"]})
}

func (h *Handler) GetAccountInspectionSchedule(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

func (h *Handler) PutAccountInspectionSchedule(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	var schedule accountInspectionSchedule
	if err := c.ShouldBindJSON(&schedule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := probackup.Default.ExecuteWrite(c.Request.Context(), func(context.Context) error {
		return scheduler.update(schedule)
	}); err != nil {
		c.JSON(accountInspectionHTTPStatus(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

func (h *Handler) RunAccountInspection(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	if err := scheduler.startRun(true); err != nil {
		c.JSON(http.StatusConflict, scheduler.snapshotForRequest(c))
		return
	}
	c.JSON(http.StatusAccepted, scheduler.snapshotForRequest(c))
}

func (h *Handler) InspectOneAccount(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	var request accountInspectionOneRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	result, err := scheduler.inspectOne(c.Request.Context(), request.Item)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		c.JSON(accountInspectionHTTPStatus(err), gin.H{"error": err.Error(), "result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
}

func (h *Handler) RefreshAccountInspectionToken(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	var request accountInspectionRefreshTokenRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	result, err := scheduler.refreshTokenNow(c.Request.Context(), request.Item)
	scheduler.mu.Lock()
	var saveErr error
	if result.Key != "" {
		scheduler.mergeTokenRefreshResultLocked(result)
		scheduler.status.Results = proinspection.SortResults(scheduler.status.Results)
		saveErr = scheduler.saveResultSnapshotLocked()
	}
	broadcast := scheduler.statusBroadcastLocked()
	scheduler.mu.Unlock()
	broadcast.send()
	err = firstNonNilError(err, saveErr)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		c.JSON(accountInspectionHTTPStatus(err), gin.H{"error": err.Error(), "result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
}

func (h *Handler) PauseAccountInspection(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	scheduler.pauseRun()
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

func (h *Handler) ResumeAccountInspection(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	scheduler.resumeRun()
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

func (h *Handler) StopAccountInspection(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	scheduler.stopRun()
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

func (h *Handler) ExecuteAccountInspectionActions(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	var request accountInspectionActionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	for _, item := range request.Items {
		if strings.TrimSpace(item.ResultRef) == "" {
			c.JSON(http.StatusConflict, gin.H{"error": errAccountInspectionResultStale.Error()})
			return
		}
	}
	outcomes, err := scheduler.executeManualActions(c.Request.Context(), request.Items)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		c.JSON(accountInspectionHTTPStatus(err), gin.H{
			"error":    err.Error(),
			"outcomes": outcomes,
			"summary":  proinspection.SummarizeActionOutcomes(outcomes),
			"schedule": snapshot["schedule"],
			"status":   snapshot["status"],
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"outcomes": outcomes,
		"summary":  proinspection.SummarizeActionOutcomes(outcomes),
		"schedule": snapshot["schedule"],
		"status":   snapshot["status"],
	})
}

func (h *Handler) StreamAccountInspectionLogs(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	scheduler.streamLogs(c)
}

func (h *Handler) GetAccountInspectionStatus(c *gin.Context) {
	scheduler := schedulerForHandler(h)
	if scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "account inspection scheduler unavailable"})
		return
	}
	c.JSON(http.StatusOK, scheduler.snapshotForRequest(c))
}

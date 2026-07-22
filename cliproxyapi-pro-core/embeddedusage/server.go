package embeddedusage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
)

const accountInspectionScheduleExportRecordType = "account_inspection_schedule"
const accountInspectionSnapshotExportRecordType = "account_inspection_snapshot"
const backupManifestRecordType = "backup_manifest"
const usageHistoryStartCursorValue = int64(1<<63 - 1)

type usageStreamEvent = internalusage.Payload

type usageHistoryCursor struct {
	SnapshotMaxID     int64  `json:"snapshot_max_id"`
	MatchedTotal      int64  `json:"matched_total"`
	BeforeTimestamp   int64  `json:"before_timestamp_ms"`
	BeforeID          int64  `json:"before_id"`
	FromMS            int64  `json:"from_ms,omitempty"`
	ToMS              int64  `json:"to_ms,omitempty"`
	Provider          string `json:"provider,omitempty"`
	Model             string `json:"model,omitempty"`
	AuthIndex         string `json:"auth_index,omitempty"`
	SearchAuthIndexes string `json:"search_auth_indexes,omitempty"`
	APIKeyHash        string `json:"api_key_hash,omitempty"`
	Status            string `json:"status,omitempty"`
	Search            string `json:"search,omitempty"`
}

type accountInspectionScheduleExportRecord struct {
	RecordType string          `json:"record_type"`
	Version    int             `json:"version"`
	Schedule   json.RawMessage `json:"schedule"`
	ExportedAt int64           `json:"exported_at_ms"`
}

type accountInspectionSnapshotExportRecord struct {
	RecordType string          `json:"record_type"`
	Version    int             `json:"version"`
	Snapshot   json.RawMessage `json:"snapshot"`
	ExportedAt int64           `json:"exported_at_ms"`
}

type backupManifestRecord struct {
	RecordType string `json:"record_type"`
	Version    int    `json:"version"`
	Records    int    `json:"records"`
	SHA256     string `json:"sha256"`
	ExportedAt int64  `json:"exported_at_ms"`
}

type Server struct {
	cfg   Config
	store *Store
}

func NewServer(cfg Config, store *Store) *Server {
	return &Server{cfg: cfg, store: store}
}

func RegisterGinRoutes(group *gin.RouterGroup) {
	server := defaultServer()
	if server == nil {
		group.GET("", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/export", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.POST("/import", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.POST("/reset", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/status", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/events", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/aggregates", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/stream", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/quota-cache", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.PUT("/quota-cache", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.DELETE("/quota-cache", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/model-prices", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.PUT("/model-prices", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/model-price-rules", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.PUT("/model-price-rules", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.DELETE("/model-price-rules", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.POST("/model-prices/sync", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/model-prices/sync-status", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.POST("/model-prices/recalculate", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.GET("/settings", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		group.PUT("/settings", func(c *gin.Context) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "usage service is not available"})
		})
		return
	}
	server.RegisterGinRoutes(group)
}

func (s *Server) RegisterGinRoutes(group *gin.RouterGroup) {
	group.GET("", s.handleUsage)
	group.GET("/export", s.handleUsageExport)
	group.POST("/import", s.handleUsageImport)
	group.POST("/reset", s.handleUsageReset)
	group.GET("/status", s.handleStatus)
	group.GET("/events", s.handleUsageEvents)
	group.GET("/aggregates", s.handleUsageAggregates)
	group.GET("/stream", s.handleUsageStream)
	group.GET("/quota-cache", s.handleQuotaCacheGet)
	group.PUT("/quota-cache", s.handleQuotaCachePut)
	group.DELETE("/quota-cache", s.handleQuotaCacheDelete)
	group.GET("/model-prices", s.handleModelPricesGet)
	group.PUT("/model-prices", s.handleModelPricesPut)
	group.GET("/model-price-rules", s.handleModelPriceRulesGet)
	group.PUT("/model-price-rules", s.handleModelPriceRulesPut)
	group.DELETE("/model-price-rules", s.handleModelPriceRulesDelete)
	group.POST("/model-prices/sync", s.handleModelPricesSync)
	group.GET("/model-prices/sync-status", s.handleModelPricesSyncStatus)
	group.POST("/model-prices/recalculate", s.handleModelPricesRecalculate)
	group.GET("/settings", s.handleMonitoringSettingsGet)
	group.PUT("/settings", s.handleMonitoringSettingsPut)
}

func parseQueryInt64(c *gin.Context, key string, fallback int64) int64 {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func parseQueryInt(c *gin.Context, key string, fallback int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseQueryIntSigned(c *gin.Context, key string, fallback int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func allowLegacyUsageImport(c *gin.Context) bool {
	value := strings.ToLower(strings.TrimSpace(c.Query("allow_legacy")))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(c.GetHeader("X-CLIProxy-Allow-Legacy-Backup")))
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func usageEventPageLimit(requestedLimit int) int {
	if requestedLimit <= 0 || requestedLimit > usageEventsPageLimit {
		return usageEventsPageLimit
	}
	return requestedLimit
}

func (s *Server) loadUsageEventPage(ctx context.Context, afterID int64, requestedLimit int) ([]internalusage.Event, int, bool, error) {
	limit := usageEventPageLimit(requestedLimit)
	events, err := s.store.EventsAfter(ctx, afterID, limit+1)
	if err != nil {
		return nil, limit, false, err
	}
	detailsLimited := len(events) > limit
	if detailsLimited {
		events = events[:limit]
	}
	return events, limit, detailsLimited, nil
}

func usagePayloadWithDetailLimit(events []internalusage.Event, limit int, detailsLimited bool) internalusage.Payload {
	payload := internalusage.BuildPayload(events)
	payload.DetailsLimit = int64(limit)
	payload.DetailsLimited = detailsLimited
	return payload
}

func applyUsageDatasetState(payload *internalusage.Payload, state UsageDatasetState) {
	payload.Generation = state.Generation
	payload.ResetAtMS = state.ResetAtMS
}

func (s *Server) usageDatasetState(ctx context.Context) (UsageDatasetState, error) {
	return s.store.UsageDatasetState(ctx)
}

func encodeUsageHistoryCursor(cursor usageHistoryCursor) string {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeUsageHistoryCursor(value string) (usageHistoryCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return usageHistoryCursor{}, fmt.Errorf("invalid usage cursor")
	}
	var cursor usageHistoryCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return usageHistoryCursor{}, fmt.Errorf("invalid usage cursor")
	}
	if cursor.SnapshotMaxID <= 0 || cursor.BeforeTimestamp <= 0 || cursor.BeforeID <= 0 {
		return usageHistoryCursor{}, fmt.Errorf("invalid usage cursor")
	}
	return cursor, nil
}

func usageStatusFilter(value string) (*bool, string) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "failed", "failure":
		failed := true
		return &failed, "failed"
	case "success", "succeeded":
		failed := false
		return &failed, "success"
	default:
		return nil, ""
	}
}

func usageEventQueryOptionsFromCursor(cursor usageHistoryCursor, limit int) UsageEventQueryOptions {
	failed, _ := usageStatusFilter(cursor.Status)
	return UsageEventQueryOptions{
		SnapshotMaxID:     cursor.SnapshotMaxID,
		BeforeTimestamp:   cursor.BeforeTimestamp,
		BeforeID:          cursor.BeforeID,
		FromMS:            cursor.FromMS,
		ToMS:              cursor.ToMS,
		Provider:          cursor.Provider,
		Model:             cursor.Model,
		AuthIndex:         cursor.AuthIndex,
		SearchAuthIndexes: cursor.SearchAuthIndexes,
		APIKeyHash:        cursor.APIKeyHash,
		Failed:            failed,
		Search:            cursor.Search,
		Limit:             limit,
		MatchedTotal:      cursor.MatchedTotal,
		SkipCount:         true,
	}
}

func usageHistoryCursorFromOptions(options UsageEventQueryOptions, status string, matchedTotal int64, event internalusage.Event) usageHistoryCursor {
	return usageHistoryCursor{
		SnapshotMaxID:     options.SnapshotMaxID,
		MatchedTotal:      matchedTotal,
		BeforeTimestamp:   event.TimestampMS,
		BeforeID:          event.ID,
		FromMS:            options.FromMS,
		ToMS:              options.ToMS,
		Provider:          options.Provider,
		Model:             options.Model,
		AuthIndex:         options.AuthIndex,
		SearchAuthIndexes: options.SearchAuthIndexes,
		APIKeyHash:        options.APIKeyHash,
		Status:            status,
		Search:            options.Search,
	}
}

func (s *Server) handleUsageHistoryEvents(c *gin.Context) {
	limit := usageEventPageLimit(parseQueryInt(c, "limit", 100))
	cursorValue := strings.TrimSpace(c.Query("cursor"))
	status := ""
	var options UsageEventQueryOptions
	if cursorValue != "" {
		cursor, err := decodeUsageHistoryCursor(cursorValue)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		options = usageEventQueryOptionsFromCursor(cursor, limit)
		status = cursor.Status
		page, err := s.store.QueryEvents(c.Request.Context(), options)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		payload := internalusage.BuildPayload(page.Events)
		state, err := s.usageDatasetState(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		applyUsageDatasetState(&payload, state)
		payload.DetailsLimit = int64(limit)
		payload.DetailsLimited = page.HasMore
		payload.MatchedTotal = page.MatchedTotal
		payload.SnapshotMaxID = options.SnapshotMaxID
		payload.PageCursor = cursorValue
		payload.HasMore = page.HasMore
		if page.HasMore && len(page.Events) > 0 {
			payload.NextCursor = encodeUsageHistoryCursor(usageHistoryCursorFromOptions(options, status, page.MatchedTotal, page.Events[len(page.Events)-1]))
		}
		c.JSON(http.StatusOK, payload)
		return
	} else {
		latestID, _, err := s.store.LatestCursor(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if latestID <= 0 {
			payload := internalusage.BuildPayload(nil)
			state, stateErr := s.usageDatasetState(c.Request.Context())
			if stateErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": stateErr.Error()})
				return
			}
			applyUsageDatasetState(&payload, state)
			payload.DetailsLimit = int64(limit)
			c.JSON(http.StatusOK, payload)
			return
		}
		failed, normalizedStatus := usageStatusFilter(c.Query("status"))
		status = normalizedStatus
		options = UsageEventQueryOptions{
			SnapshotMaxID:     latestID,
			FromMS:            parseQueryInt64(c, "from_ms", 0),
			ToMS:              parseQueryInt64(c, "to_ms", 0),
			Provider:          strings.TrimSpace(c.Query("provider")),
			Model:             strings.TrimSpace(c.Query("model")),
			AuthIndex:         strings.TrimSpace(c.Query("auth_index")),
			SearchAuthIndexes: strings.TrimSpace(c.Query("search_auth_indexes")),
			APIKeyHash:        strings.TrimSpace(c.Query("api_key_hash")),
			Failed:            failed,
			Search:            strings.TrimSpace(c.Query("search")),
			Limit:             limit,
		}
	}

	page, err := s.store.QueryEvents(c.Request.Context(), options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	payload := internalusage.BuildPayload(page.Events)
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	applyUsageDatasetState(&payload, state)
	payload.DetailsLimit = int64(limit)
	payload.DetailsLimited = page.HasMore
	payload.MatchedTotal = page.MatchedTotal
	payload.SnapshotMaxID = options.SnapshotMaxID
	payload.PageCursor = encodeUsageHistoryCursor(usageHistoryCursorFromOptions(
		options,
		status,
		page.MatchedTotal,
		internalusage.Event{TimestampMS: usageHistoryStartCursorValue, ID: usageHistoryStartCursorValue},
	))
	payload.HasMore = page.HasMore
	if page.HasMore && len(page.Events) > 0 {
		payload.NextCursor = encodeUsageHistoryCursor(usageHistoryCursorFromOptions(options, status, page.MatchedTotal, page.Events[len(page.Events)-1]))
	}
	c.JSON(http.StatusOK, payload)
}

func (s *Server) handleUsage(c *gin.Context) {
	limit := parseQueryInt(c, "limit", s.cfg.QueryLimit)
	if limit <= 0 {
		limit = s.cfg.QueryLimit
	}
	if limit <= 0 {
		limit = 50000
	}
	latestID, _, err := s.store.LatestCursor(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	events, err := s.store.RecentEvents(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	payload := internalusage.BuildPayload(events)
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	applyUsageDatasetState(&payload, state)
	if latestID > payload.LatestID {
		payload.LatestID = latestID
	}
	summary, err := s.store.UsageSummary(c.Request.Context(), payload.LatestID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	payload.TotalRequests = summary.TotalRequests
	payload.SuccessCount = summary.SuccessCount
	payload.FailureCount = summary.FailureCount
	payload.TotalTokens = summary.TotalTokens
	if limit > 0 {
		payload.DetailsLimit = int64(limit)
		payload.DetailsLimited = summary.TotalRequests > payload.DetailsCount
	}
	c.JSON(http.StatusOK, payload)
}

func (s *Server) handleUsageEvents(c *gin.Context) {
	if strings.TrimSpace(c.Query("cursor")) != "" || strings.EqualFold(strings.TrimSpace(c.Query("direction")), "before") {
		s.handleUsageHistoryEvents(c)
		return
	}
	afterID := parseQueryInt64(c, "after_id", 0)
	events, limit, detailsLimited, err := s.loadUsageEventPage(c.Request.Context(), afterID, parseQueryInt(c, "limit", s.cfg.BatchSize))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	payload := usagePayloadWithDetailLimit(events, limit, detailsLimited)
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	applyUsageDatasetState(&payload, state)
	c.JSON(http.StatusOK, payload)
}

func (s *Server) handleUsageAggregates(c *gin.Context) {
	latestID, _, err := s.store.LatestCursor(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	options := UsageAggregateOptions{
		FromMS:                parseQueryInt64(c, "from_ms", 0),
		ToMS:                  parseQueryInt64(c, "to_ms", 0),
		Interval:              strings.TrimSpace(c.Query("interval")),
		GroupBy:               parseCSVQuery(c.Query("group_by")),
		Limit:                 parseQueryInt(c, "limit", 1000),
		APIKeyHash:            strings.TrimSpace(c.Query("api_key_hash")),
		TimezoneOffsetMinutes: parseQueryIntSigned(c, "timezone_offset_minutes", 0),
	}
	buckets, err := s.store.UsageAggregates(c.Request.Context(), options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":          buckets,
		"latest_id":      latestID,
		"generation":     state.Generation,
		"reset_at_ms":    state.ResetAtMS,
		"snapshot_at_ms": time.Now().UnixMilli(),
	})
}

func (s *Server) handleUsageStream(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	lastID := parseQueryInt64(c, "after_id", 0)
	clientGeneration := parseQueryInt64(c, "generation", 0)
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	resetRequired := clientGeneration > 0 && clientGeneration != state.Generation
	if resetRequired {
		lastID = 0
	}
	initialEvents, initialLimit, initialDetailsLimited, err := s.loadUsageEventPage(c.Request.Context(), lastID, s.cfg.BatchSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	writeEvent := func(name string, payload usageStreamEvent) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if resetRequired {
		payload := internalusage.BuildPayload(nil)
		applyUsageDatasetState(&payload, state)
		if !writeEvent("reset", payload) {
			return
		}
		return
	}
	if len(initialEvents) == 0 {
		payload := internalusage.BuildPayload(nil)
		applyUsageDatasetState(&payload, state)
		if !writeEvent("ready", payload) {
			return
		}
	} else {
		payload := usagePayloadWithDetailLimit(initialEvents, initialLimit, initialDetailsLimited)
		applyUsageDatasetState(&payload, state)
		lastID = payload.LatestID
		if !writeEvent("usage", payload) {
			return
		}
	}

	for {
		eventSignal := s.store.EventSignal()
		currentState, err := s.usageDatasetState(c.Request.Context())
		if err != nil {
			return
		}
		if currentState.Generation != state.Generation {
			state = currentState
			lastID = 0
			payload := internalusage.BuildPayload(nil)
			applyUsageDatasetState(&payload, state)
			if !writeEvent("reset", payload) {
				return
			}
			return
		}
		events, limit, detailsLimited, err := s.loadUsageEventPage(c.Request.Context(), lastID, s.cfg.BatchSize)
		if err != nil {
			return
		}
		if len(events) > 0 {
			payload := usagePayloadWithDetailLimit(events, limit, detailsLimited)
			applyUsageDatasetState(&payload, state)
			lastID = payload.LatestID
			if !writeEvent("usage", payload) {
				return
			}
			continue
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-keepaliveTicker.C:
			if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			continue
		case <-eventSignal:
			continue
		}
	}
}

func (s *Server) exportJSONL(ctx context.Context) ([]byte, error) {
	if err := flushRuntimeStateWrites(ctx, s.store); err != nil {
		return nil, err
	}
	data, err := s.store.ExportJSONL(ctx)
	if err != nil {
		return nil, err
	}
	if accountInspectionScheduleExporter != nil {
		schedule, ok, err := accountInspectionScheduleExporter()
		if err != nil {
			return nil, err
		}
		if ok {
			line, err := json.Marshal(accountInspectionScheduleExportRecord{
				RecordType: accountInspectionScheduleExportRecordType,
				Version:    1,
				Schedule:   schedule,
				ExportedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				return nil, err
			}
			data = append(data, line...)
			data = append(data, '\n')
		}
	}
	if accountInspectionSnapshotExporter != nil {
		snapshot, ok, err := accountInspectionSnapshotExporter()
		if err != nil {
			return nil, err
		}
		if ok {
			line, err := json.Marshal(accountInspectionSnapshotExportRecord{
				RecordType: accountInspectionSnapshotExportRecordType,
				Version:    1,
				Snapshot:   snapshot,
				ExportedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				return nil, err
			}
			data = append(data, line...)
			data = append(data, '\n')
		}
	}
	digest := sha256.Sum256(data)
	manifest, err := json.Marshal(backupManifestRecord{
		RecordType: backupManifestRecordType,
		Version:    1,
		Records:    bytes.Count(data, []byte{'\n'}),
		SHA256:     fmt.Sprintf("%x", digest),
		ExportedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	output := make([]byte, 0, len(manifest)+1+len(data))
	output = append(output, manifest...)
	output = append(output, '\n')
	output = append(output, data...)
	return output, nil
}

func (s *Server) handleUsageExport(c *gin.Context) {
	data, err := s.exportJSONL(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Content-Disposition", `attachment; filename="usage-events.jsonl"`)
	_, _ = c.Writer.Write(data)
}

func (s *Server) handleUsageImport(c *gin.Context) {
	eventStage, err := os.CreateTemp("", "cliproxy-usage-import-*.jsonl")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	eventStagePath := eventStage.Name()
	defer func() {
		_ = eventStage.Close()
		_ = os.Remove(eventStagePath)
	}()
	reader := bufio.NewScanner(c.Request.Body)
	reader.Buffer(make([]byte, 64*1024), 64*1024*1024)
	totalEvents := 0
	var modelPrices map[string]ModelPrice
	var modelPriceRules []ModelPriceRule
	modelPriceRecords := 0
	var quotaEntries []QuotaCacheEntry
	quotaCacheRecords := 0
	var routingCursors []RoutingCursorState
	routingCursorRecords := 0
	var authRuntimeStats []AuthRuntimeStats
	authRuntimeStatsRecords := 0
	var accountInspectionSchedule json.RawMessage
	accountInspectionScheduleRecords := 0
	var accountInspectionSnapshot json.RawMessage
	accountInspectionSnapshotRecords := 0
	var monitoringSettings *MonitoringSettings
	monitoringSettingsRecords := 0
	failed := 0
	var manifest *backupManifestRecord
	hashedRecords := 0
	hasher := sha256.New()
	nonEmptyRecords := 0
	for reader.Scan() {
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}
		raw := []byte(line)
		recordType, err := readImportRecordType(raw)
		if err != nil {
			nonEmptyRecords++
			if manifest != nil {
				_, _ = hasher.Write(raw)
				_, _ = hasher.Write([]byte{'\n'})
				hashedRecords++
			}
			failed++
			continue
		}
		if recordType == backupManifestRecordType {
			if nonEmptyRecords != 0 || manifest != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "backup manifest must be the first and only manifest record"})
				return
			}
			parsed, err := parseBackupManifest(raw)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			manifest = &parsed
			nonEmptyRecords++
			continue
		}
		nonEmptyRecords++
		if manifest != nil {
			_, _ = hasher.Write(raw)
			_, _ = hasher.Write([]byte{'\n'})
			hashedRecords++
		}
		switch recordType {
		case accountInspectionScheduleExportRecordType:
			schedule, err := parseAccountInspectionScheduleImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			accountInspectionSchedule = schedule
			accountInspectionScheduleRecords++
			continue
		case accountInspectionSnapshotExportRecordType:
			snapshot, err := parseAccountInspectionSnapshotImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			accountInspectionSnapshot = snapshot
			accountInspectionSnapshotRecords++
			continue
		case modelPricesExportRecordType:
			prices, rules, err := parseModelPricesImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			modelPrices = prices
			modelPriceRules = rules
			modelPriceRecords++
			continue
		case monitoringSettingsExportRecordType:
			settings, err := parseMonitoringSettingsImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			monitoringSettings = &settings
			monitoringSettingsRecords++
			continue
		case quotaCacheExportRecordType:
			entries, err := parseQuotaCacheImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			quotaEntries = append(quotaEntries, entries...)
			quotaCacheRecords++
			continue
		case routingCursorExportRecordType:
			items, err := parseRoutingCursorImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			routingCursors = append(routingCursors, items...)
			routingCursorRecords++
			continue
		case authRuntimeStatsExportRecordType:
			items, err := parseAuthRuntimeStatsImportRecord(raw)
			if err != nil {
				failed++
				continue
			}
			authRuntimeStats = append(authRuntimeStats, items...)
			authRuntimeStatsRecords++
			continue
		}
		if recordType != "" {
			failed++
			continue
		}
		if _, err := internalusage.NormalizeRaw(raw); err != nil {
			failed++
			continue
		}
		if _, err := eventStage.Write(raw); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if _, err := eventStage.Write([]byte{'\n'}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		totalEvents++
	}
	if err := reader.Err(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if manifest == nil && !allowLegacyUsageImport(c) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "backup manifest is required; retry with allow_legacy=1 only for a trusted legacy backup",
		})
		return
	}
	if manifest != nil {
		actualHash := fmt.Sprintf("%x", hasher.Sum(nil))
		if failed > 0 || hashedRecords != manifest.Records || actualHash != manifest.SHA256 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "backup manifest verification failed"})
			return
		}
	}
	result := InsertResult{}
	batchSize := s.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	if _, err := eventStage.Seek(0, 0); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	events := make([]internalusage.Event, 0, batchSize)
	flushEvents := func() error {
		if len(events) == 0 {
			return nil
		}
		batchResult, err := s.store.InsertEvents(c.Request.Context(), events)
		if err != nil {
			return err
		}
		result.Inserted += batchResult.Inserted
		result.Skipped += batchResult.Skipped
		events = events[:0]
		return nil
	}
	stagedReader := bufio.NewScanner(eventStage)
	stagedReader.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for stagedReader.Scan() {
		event, err := internalusage.NormalizeRaw(stagedReader.Bytes())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		events = append(events, event)
		if len(events) >= batchSize {
			if err := flushEvents(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}
	if err := stagedReader.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := flushEvents(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if modelPrices != nil {
		if err := s.store.SetModelPrices(c.Request.Context(), modelPrices); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	for _, rule := range modelPriceRules {
		if _, _, err := s.store.UpsertModelPriceRule(c.Request.Context(), rule, true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if modelPrices != nil || len(modelPriceRules) > 0 {
		if _, err := s.store.RecalculateEventCosts(c.Request.Context(), true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	importedQuotaEntries, err := s.store.ImportQuotaCache(c.Request.Context(), quotaEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(routingCursors) > 0 || len(authRuntimeStats) > 0 {
		if err := flushRuntimeStateWrites(c.Request.Context(), s.store); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	importedRoutingCursors, importedAuthRuntimeStats, err := s.store.ImportRuntimeState(c.Request.Context(), routingCursors, authRuntimeStats)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if authRuntimeStateImporter != nil && (importedRoutingCursors > 0 || importedAuthRuntimeStats > 0) {
		currentRoutingCursors, errLoad := s.store.ListRoutingCursorStates(c.Request.Context())
		if errLoad != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errLoad.Error()})
			return
		}
		currentAuthRuntimeStats, errLoad := s.store.ListAuthRuntimeStats(c.Request.Context())
		if errLoad != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errLoad.Error()})
			return
		}
		if errApply := authRuntimeStateImporter(currentRoutingCursors, currentAuthRuntimeStats); errApply != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": errApply.Error()})
			return
		}
	}
	if monitoringSettings != nil {
		if err := s.store.SetMonitoringSettings(c.Request.Context(), *monitoringSettings); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if accountInspectionSchedule != nil && accountInspectionScheduleImporter != nil {
		if err := accountInspectionScheduleImporter(accountInspectionSchedule); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if accountInspectionSnapshot != nil && accountInspectionSnapshotImporter != nil {
		if err := accountInspectionSnapshotImporter(accountInspectionSnapshot); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"added":                            result.Inserted,
		"skipped":                          result.Skipped,
		"total":                            totalEvents,
		"failed":                           failed,
		"modelPrices":                      len(modelPrices),
		"modelPriceRecords":                modelPriceRecords,
		"modelPriceRules":                  len(modelPriceRules),
		"quotaCache":                       importedQuotaEntries,
		"quotaCacheRecords":                quotaCacheRecords,
		"routingCursors":                   importedRoutingCursors,
		"routingCursorRecords":             routingCursorRecords,
		"authRuntimeStats":                 importedAuthRuntimeStats,
		"authRuntimeStatsRecords":          authRuntimeStatsRecords,
		"accountInspectionSchedule":        accountInspectionSchedule != nil,
		"accountInspectionScheduleRecords": accountInspectionScheduleRecords,
		"accountInspectionSnapshot":        accountInspectionSnapshot != nil,
		"accountInspectionSnapshotRecords": accountInspectionSnapshotRecords,
		"monitoringSettings":               monitoringSettings != nil,
		"monitoringSettingsRecords":        monitoringSettingsRecords,
		"legacyBackup":                     manifest == nil,
	})
}

func (s *Server) handleUsageReset(c *gin.Context) {
	var payload struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !payload.Confirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "reset confirmation is required"})
		return
	}
	result, err := s.store.ResetUsageStatistics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func readImportRecordType(raw []byte) (string, error) {
	var header struct {
		RecordType string `json:"record_type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", err
	}
	return header.RecordType, nil
}

func parseBackupManifest(raw []byte) (backupManifestRecord, error) {
	var manifest backupManifestRecord
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return backupManifestRecord{}, err
	}
	if manifest.Version != 1 {
		return backupManifestRecord{}, fmt.Errorf("unsupported backup manifest version %d", manifest.Version)
	}
	if manifest.Records < 0 || len(manifest.SHA256) != sha256.Size*2 {
		return backupManifestRecord{}, fmt.Errorf("invalid backup manifest")
	}
	for _, character := range manifest.SHA256 {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return backupManifestRecord{}, fmt.Errorf("invalid backup manifest hash")
		}
	}
	return manifest, nil
}

func parseAccountInspectionScheduleImportRecord(raw []byte) (json.RawMessage, error) {
	var record accountInspectionScheduleExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if len(record.Schedule) == 0 {
		return nil, nil
	}
	return record.Schedule, nil
}

func parseAccountInspectionSnapshotImportRecord(raw []byte) (json.RawMessage, error) {
	var record accountInspectionSnapshotExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if len(record.Snapshot) == 0 {
		return nil, nil
	}
	return record.Snapshot, nil
}

func parseQuotaCacheImportRecord(raw []byte) ([]QuotaCacheEntry, error) {
	var record quotaCacheExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if record.Version < 1 || record.Version > 2 {
		return nil, fmt.Errorf("unsupported quota cache export version %d", record.Version)
	}
	for index := range record.Entries {
		entry := &record.Entries[index]
		if entry.Provider == "" || entry.FileName == "" || len(entry.Data) == 0 || !json.Valid(entry.Data) {
			return nil, fmt.Errorf("invalid quota cache entry at index %d", index)
		}
		if record.Version == 1 && entry.ObservedAt <= 0 {
			entry.ObservedAt = entry.CachedAt
		}
	}
	return record.Entries, nil
}

func parseRoutingCursorImportRecord(raw []byte) ([]RoutingCursorState, error) {
	var record routingCursorExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 {
		return nil, fmt.Errorf("unsupported routing cursor export version %d", record.Version)
	}
	for index, item := range record.Items {
		if strings.TrimSpace(item.CursorKey) == "" || strings.TrimSpace(item.LastAuthID) == "" {
			return nil, fmt.Errorf("invalid routing cursor at index %d", index)
		}
	}
	return record.Items, nil
}

func parseAuthRuntimeStatsImportRecord(raw []byte) ([]AuthRuntimeStats, error) {
	var record authRuntimeStatsExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 {
		return nil, fmt.Errorf("unsupported auth runtime stats export version %d", record.Version)
	}
	for index, item := range record.Items {
		if strings.TrimSpace(item.AuthIndex) == "" || strings.TrimSpace(item.AuthID) == "" ||
			item.SelectedCount < 0 || item.SuccessCount < 0 || item.FailureCount < 0 || len(item.RecentBuckets) > 20 {
			return nil, fmt.Errorf("invalid auth runtime stats at index %d", index)
		}
	}
	return record.Items, nil
}

func parseMonitoringSettingsImportRecord(raw []byte) (MonitoringSettings, error) {
	var record monitoringSettingsExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return MonitoringSettings{}, err
	}
	return normalizeMonitoringSettings(record.Settings), nil
}

func parseModelPricesImportRecord(raw []byte) (map[string]ModelPrice, []ModelPriceRule, error) {
	var record modelPricesExportRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, nil, err
	}
	if record.Prices == nil {
		record.Prices = map[string]ModelPrice{}
	}
	return record.Prices, record.Rules, nil
}

func (s *Server) handleStatus(c *gin.Context) {
	events, deadLetters, err := s.store.Counts(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	latestID, latestTimestamp, err := s.store.LatestCursor(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	deadLetterSamples, err := s.store.RecentDeadLetters(c.Request.Context(), parseQueryInt(c, "dead_letter_limit", 5))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	state, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service":           "embedded-usage-service",
		"dbPath":            s.cfg.DBPath,
		"events":            events,
		"deadLetters":       deadLetters,
		"deadLetterSamples": deadLetterSamples,
		"latestId":          latestID,
		"latestTimestampMs": latestTimestamp,
		"generation":        state.Generation,
		"resetAtMs":         state.ResetAtMS,
	})
}

func parseCSVQuery(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *Server) handleQuotaCacheGet(c *gin.Context) {
	if c.Query("stats") == "1" || c.Query("stats") == "true" {
		stats, err := s.store.QuotaCacheStats(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, stats)
		return
	}

	provider := strings.TrimSpace(c.Query("provider"))
	fileName := strings.TrimSpace(c.Query("fileName"))
	entries, err := s.store.GetQuotaCache(c.Request.Context(), provider, fileName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": entries})
}

func (s *Server) handleQuotaCachePut(c *gin.Context) {
	var entry QuotaCacheEntry
	if err := json.NewDecoder(c.Request.Body).Decode(&entry); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	entry.Provider = strings.TrimSpace(entry.Provider)
	entry.FileName = strings.TrimSpace(entry.FileName)
	if entry.Provider == "" || entry.FileName == "" || len(entry.Data) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider, fileName and data are required"})
		return
	}
	if err := s.store.SetQuotaCache(c.Request.Context(), entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleQuotaCacheDelete(c *gin.Context) {
	provider := strings.TrimSpace(c.Query("provider"))
	fileName := strings.TrimSpace(c.Query("fileName"))
	if err := s.store.DeleteQuotaCache(c.Request.Context(), provider, fileName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleModelPricesGet(c *gin.Context) {
	prices, err := s.store.GetModelPrices(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"prices": prices})
}

func (s *Server) handleModelPricesPut(c *gin.Context) {
	var payload struct {
		Prices map[string]ModelPrice `json:"prices"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if payload.Prices == nil {
		payload.Prices = map[string]ModelPrice{}
	}
	if err := s.store.SetModelPrices(c.Request.Context(), payload.Prices); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleModelPriceRulesGet(c *gin.Context) {
	rules, err := s.store.ActiveModelPriceRules(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	observed, err := s.store.ObservedModels(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules, "observedModels": observed})
}

func (s *Server) handleModelPriceRulesPut(c *gin.Context) {
	var payload struct {
		Rule ModelPriceRule `json:"rule"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	payload.Rule.Source = modelPriceSourceManual
	payload.Rule.Locked = true
	rule, changed, err := s.store.UpsertModelPriceRule(c.Request.Context(), payload.Rule, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rule": rule, "changed": changed})
}

func (s *Server) handleModelPriceRulesDelete(c *gin.Context) {
	provider := strings.TrimSpace(c.Query("provider"))
	model := strings.TrimSpace(c.Query("model"))
	if model == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is required"})
		return
	}
	if err := s.store.DeleteModelPriceRule(c.Request.Context(), provider, model); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleModelPricesSync(c *gin.Context) {
	var payload struct {
		DryRun               bool     `json:"dryRun"`
		RecalculateUnpriced  bool     `json:"recalculateUnpriced"`
		OverrideLockedModels []string `json:"overrideLockedModels"`
	}
	if c.Request.ContentLength != 0 {
		if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	result, err := s.store.SyncModelsDevPrices(c.Request.Context(), payload.DryRun, payload.RecalculateUnpriced, payload.OverrideLockedModels...)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (s *Server) handleModelPricesSyncStatus(c *gin.Context) {
	state, err := s.store.GetModelPriceSyncState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"state": state})
}

func (s *Server) handleModelPricesRecalculate(c *gin.Context) {
	var payload struct {
		All bool `json:"all"`
	}
	if c.Request.ContentLength != 0 {
		if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	updated, err := s.store.RecalculateEventCosts(c.Request.Context(), !payload.All)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

func (s *Server) handleMonitoringSettingsGet(c *gin.Context) {
	settings, err := s.store.GetMonitoringSettings(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

func (s *Server) handleMonitoringSettingsPut(c *gin.Context) {
	var payload struct {
		Settings MonitoringSettings `json:"settings"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	settings := normalizeMonitoringSettings(payload.Settings)
	if settings.WebDAV.Enabled && strings.TrimSpace(settings.WebDAV.URL) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "webdav url is required"})
		return
	}
	if err := s.store.SetMonitoringSettings(c.Request.Context(), settings); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if _, err := s.store.ApplyRetention(c.Request.Context(), time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

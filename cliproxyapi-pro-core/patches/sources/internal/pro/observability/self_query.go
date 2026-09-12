package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability/internalusage"
)

// Public response fields are allowlisted independently of Management payloads.
type selfEvent struct {
	ReasoningEffort  string   `json:"reasoningEffort,omitempty"`
	ReasoningTokens  int64    `json:"reasoningTokens"`
	CachedTokens     int64    `json:"cachedTokens"`
	CacheReadTokens  int64    `json:"cacheReadTokens"`
	CacheInputTokens int64    `json:"cacheInputTokens"`
	ID               int64    `json:"id"`
	TimestampMS      int64    `json:"timestampMs"`
	Model            string   `json:"model"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	TotalTokens      int64    `json:"totalTokens"`
	LatencyMS        *int64   `json:"latencyMs,omitempty"`
	TTFTMS           *int64   `json:"ttftMs,omitempty"`
	StatusCode       *int     `json:"statusCode,omitempty"`
	EstimatedCost    *float64 `json:"estimatedCost,omitempty"`
	Failed           bool     `json:"failed"`
}

func projectSelfEvent(e internalusage.Event) selfEvent {
	model := e.RequestedModel
	if model == "" {
		model = e.Model
	}
	tokens := internalusage.EventTokens(e)
	return selfEvent{ReasoningEffort: e.ReasoningEffort, ReasoningTokens: tokens.ReasoningTokens,
		CachedTokens: tokens.CachedTokens, CacheReadTokens: tokens.CacheReadTokens, CacheInputTokens: tokens.CacheInputTokens, ID: e.ID, TimestampMS: e.TimestampMS, Model: model,
		InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens, TotalTokens: tokens.TotalTokens,
		LatencyMS: e.LatencyMS, TTFTMS: e.TTFTMS, StatusCode: e.StatusCode,
		EstimatedCost: e.EstimatedCost, Failed: e.Failed}
}

// HandleSelfQuery only accepts identity set by the server's API-key authenticator.
func HandleSelfQuery(c *gin.Context) {
	s := defaultServer()
	if s == nil || !s.cfg.Enabled || s.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Usage service unavailable"})
		return
	}
	s.handleSelfQuery(c)
}

func (s *Server) handleSelfQuery(c *gin.Context) {
	identity, ok := apikeypolicy.IdentityFromContext(c.Request.Context())
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	ctx := c.Request.Context()
	if c.Param("section") == "stream" {
		s.handleSelfStream(c, identity.Hash())
		return
	}
	days, err := strconv.Atoi(c.DefaultQuery("days", "7"))
	if err != nil || (days != 1 && days != 7 && days != 30) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be 1, 7 or 30"})
		return
	}
	to := time.Now().UnixMilli()
	from := to - int64(time.Duration(days)*24*time.Hour/time.Millisecond)
	fail := func() { c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Usage temporarily unavailable"}) }
	state, err := s.usageDatasetState(ctx)
	if err != nil {
		fail()
		return
	}
	switch c.Param("section") {
	case "stats":
		items, err := s.store.UsageAggregates(ctx, UsageAggregateOptions{APIKeyHash: identity.Hash(), FromMS: from, ToMS: to, Interval: "all", Limit: 1})
		if err != nil {
			fail()
			return
		}
		total := UsageAggregateBucket{}
		if len(items) > 0 {
			total = items[0]
		}
		trend, err := s.store.UsageAggregates(ctx, UsageAggregateOptions{APIKeyHash: identity.Hash(), FromMS: from, ToMS: to, Interval: "day", Limit: 32})
		if err != nil {
			fail()
			return
		}
		s.writeSelfQuerySnapshot(c, state, gin.H{"total": total, "trend": trend, "snapshotAtMs": to, "fromMs": from})
	case "events":
		beforeMS, errMS := strconv.ParseInt(c.DefaultQuery("before_ms", "0"), 10, 64)
		beforeID, errID := strconv.ParseInt(c.DefaultQuery("before_id", "0"), 10, 64)
		if errMS != nil || errID != nil || beforeMS < 0 || beforeID < 0 || (beforeMS == 0) != (beforeID == 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cursor"})
			return
		}
		page, err := s.store.QueryEvents(ctx, UsageEventQueryOptions{APIKeyHash: identity.Hash(), FromMS: from, ToMS: to, BeforeTimestamp: beforeMS, BeforeID: beforeID, Limit: 50, SkipCount: true})
		if err != nil {
			fail()
			return
		}
		items := make([]selfEvent, 0, len(page.Events))
		for _, e := range page.Events {
			items = append(items, projectSelfEvent(e))
		}
		s.writeSelfQuerySnapshot(c, state, gin.H{"items": items, "hasMore": page.HasMore, "snapshotAtMs": to})
	default:
		c.AbortWithStatus(http.StatusNotFound)
	}
}

// Never return records or aggregates spanning a reset/restore boundary.
func (s *Server) writeSelfQuerySnapshot(c *gin.Context, state UsageDatasetState, payload gin.H) {
	current, err := s.usageDatasetState(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Usage temporarily unavailable"})
		return
	}
	if current.Generation != state.Generation {
		c.JSON(http.StatusConflict, gin.H{"error": "Usage data changed", "generation": current.Generation})
		return
	}
	payload["generation"] = state.Generation
	c.JSON(http.StatusOK, payload)
}

// The stream invalidates only this key's view. It never publishes global event
// IDs or payloads. Reconnecting clients fetch a fresh, scoped history snapshot.
func (s *Server) handleSelfStream(c *gin.Context, hash string) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	s.streamSelfUsage(c, hash, ticker.C)
}

func (s *Server) streamSelfUsage(c *gin.Context, hash string, ticks <-chan time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Cache-Control", "no-store")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	var previousID, previousGeneration int64 = -1, -1
	for {
		if value, exists := c.Get("selfQueryValidate"); exists {
			if validate, ok := value.(func() bool); !ok || !validate() {
				return
			}
		}
		var latestID int64
		err := s.store.executor(c.Request.Context()).QueryRowContext(c.Request.Context(), `select coalesce(max(id), 0) from usage_events where api_key_hash = ?`, hash).Scan(&latestID)
		state, stateErr := s.usageDatasetState(c.Request.Context())
		if err != nil || stateErr != nil {
			return
		}
		message := ": keepalive\n\n"
		if latestID != previousID || state.Generation != previousGeneration {
			event := "change"
			if previousGeneration >= 0 && state.Generation != previousGeneration {
				event = "reset"
			}
			message = fmt.Sprintf("event: %s\ndata: {\"generation\":%d}\n\n", event, state.Generation)
			previousID, previousGeneration = latestID, state.Generation
		}
		if _, err := fmt.Fprint(c.Writer, message); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticks:
		}
	}
}

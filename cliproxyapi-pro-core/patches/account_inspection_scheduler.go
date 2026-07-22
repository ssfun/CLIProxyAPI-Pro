package management

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	accountInspectionProviderAll            = "all"
	accountInspectionDefaultIntervalMin     = 360
	accountInspectionDefaultTimeoutMS       = 15000
	accountInspectionMinTimeoutMS           = 3000
	accountInspectionMaxTimeoutMS           = 30000
	accountInspectionMaxWorkers             = 8
	accountInspectionMaxDeleteWorkers       = 4
	accountInspectionMaxRetries             = 1
	accountInspectionMaxRunDuration         = 30 * time.Minute
	accountInspectionMaxProviderConcurrency = 2
	accountInspectionMaxRefreshConcurrency  = 2
	accountInspectionXAIMinAttempts         = 2
	accountInspectionXAIRetryDelay          = 300 * time.Millisecond
	accountInspectionWebSocketWriteTimeout  = 5 * time.Second
	accountInspectionWebSocketPongWait      = 60 * time.Second
	accountInspectionWebSocketPingPeriod    = 54 * time.Second
	accountInspectionProgressBroadcastGap   = 500 * time.Millisecond
	accountInspectionMaxResultPageSize      = 500
	accountInspectionMaxLogPageSize         = 500
	accountInspectionQuotaParserVersion     = 4
)

var accountInspectionWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var accountInspectionSupportedProviders = map[string]struct{}{
	"antigravity": {},
	"claude":      {},
	"codex":       {},
	"gemini-cli":  {},
	"kimi":        {},
	"xai":         {},
}

var accountInspectionSchedulers sync.Map

type accountInspectionSettings struct {
	TargetType                      string                                `json:"targetType"`
	Workers                         int                                   `json:"workers"`
	DeleteWorkers                   int                                   `json:"deleteWorkers"`
	Timeout                         int                                   `json:"timeout"`
	Retries                         int                                   `json:"retries"`
	UsedPercentThreshold            int                                   `json:"usedPercentThreshold"`
	SampleSize                      int                                   `json:"sampleSize"`
	AntigravityDeepProbeEnabled     bool                                  `json:"antigravityDeepProbeEnabled"`
	AntigravityDeepProbeModel       string                                `json:"antigravityDeepProbeModel"`
	AntigravityQuotaMode            accountInspectionAntigravityQuotaMode `json:"antigravityQuotaMode"`
	XAIDeepProbeEnabled             bool                                  `json:"xaiDeepProbeEnabled"`
	XAIDeepProbeModel               string                                `json:"xaiDeepProbeModel"`
	AutoExecuteQuotaLimitDisable    bool                                  `json:"autoExecuteQuotaLimitDisable"`
	AutoExecuteQuotaRecoveryEnable  bool                                  `json:"autoExecuteQuotaRecoveryEnable"`
	AutoExecuteAccountInvalidAction accountInspectionAction               `json:"autoExecuteAccountInvalidAction"`
	AutoExecuteRequestErrorAction   accountInspectionAction               `json:"autoExecuteRequestErrorAction"`
	AutoExecuteConfirmations        int                                   `json:"autoExecuteConfirmations,omitempty"`
}

type accountInspectionSchedule struct {
	Enabled         bool                      `json:"enabled"`
	IntervalMinutes int                       `json:"intervalMinutes"`
	NextRunAt       int64                     `json:"nextRunAt"`
	Settings        accountInspectionSettings `json:"settings"`
}

type accountInspectionLogEntry struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type accountInspectionResult struct {
	Key                   string                  `json:"key"`
	Provider              string                  `json:"provider"`
	FileName              string                  `json:"fileName"`
	DisplayName           string                  `json:"displayName"`
	Email                 string                  `json:"email"`
	Name                  string                  `json:"name"`
	AuthIndex             string                  `json:"authIndex"`
	Disabled              bool                    `json:"disabled"`
	Action                accountInspectionAction `json:"action"`
	ActionReason          string                  `json:"actionReason"`
	StatusCode            *int                    `json:"statusCode"`
	UsedPercent           *float64                `json:"usedPercent"`
	IsQuota               bool                    `json:"isQuota"`
	Error                 string                  `json:"error"`
	ErrorDetail           string                  `json:"errorDetail,omitempty"`
	ErrorCode             string                  `json:"errorCode"`
	DeepProbeTriggered    bool                    `json:"deepProbeTriggered"`
	DeepProbeStatus       string                  `json:"deepProbeStatus"`
	DeepProbeError        string                  `json:"deepProbeError"`
	TokenRefreshTriggered bool                    `json:"tokenRefreshTriggered"`
	TokenRefreshStatus    string                  `json:"tokenRefreshStatus"`
	TokenRefreshError     string                  `json:"tokenRefreshError"`
	NextRefreshAt         int64                   `json:"nextRefreshAt"`
	Executed              bool                    `json:"executed"`
	ExecuteError          string                  `json:"executeError"`
}

type accountInspectionSummary struct {
	TotalFiles           int `json:"totalFiles"`
	ProbeSetCount        int `json:"probeSetCount"`
	SampledCount         int `json:"sampledCount"`
	DisabledCount        int `json:"disabledCount"`
	EnabledCount         int `json:"enabledCount"`
	DeleteCount          int `json:"deleteCount"`
	DisableCount         int `json:"disableCount"`
	EnableCount          int `json:"enableCount"`
	KeepCount            int `json:"keepCount"`
	ErrorCount           int `json:"errorCount"`
	ExecutedDeleteCount  int `json:"executedDeleteCount"`
	ExecutedDisableCount int `json:"executedDisableCount"`
	ExecutedEnableCount  int `json:"executedEnableCount"`
}

type accountInspectionHealthCounts struct {
	Total           int `json:"total"`
	Healthy         int `json:"healthy"`
	Disabled        int `json:"disabled"`
	AuthInvalid     int `json:"authInvalid"`
	QuotaExhausted  int `json:"quotaExhausted"`
	InspectionError int `json:"inspectionError"`
	Recoverable     int `json:"recoverable"`
}

type accountInspectionRunState string

type accountInspectionStreamMessageType string

type accountInspectionDeepProbeStatus string

type accountInspectionAntigravityQuotaMode string

type accountInspectionAction string

const (
	accountInspectionStreamSnapshot accountInspectionStreamMessageType = "snapshot"
	accountInspectionStreamLog      accountInspectionStreamMessageType = "log"
	accountInspectionStreamStatus   accountInspectionStreamMessageType = "status"
)

const (
	accountInspectionActionNone    accountInspectionAction = "none"
	accountInspectionActionKeep    accountInspectionAction = "keep"
	accountInspectionActionDelete  accountInspectionAction = "delete"
	accountInspectionActionDisable accountInspectionAction = "disable"
	accountInspectionActionEnable  accountInspectionAction = "enable"
)

const (
	accountInspectionDeepProbeSuccess        accountInspectionDeepProbeStatus = "success"
	accountInspectionDeepProbeQuota          accountInspectionDeepProbeStatus = "quota"
	accountInspectionDeepProbeAuthError      accountInspectionDeepProbeStatus = "auth_error"
	accountInspectionDeepProbeTransientError accountInspectionDeepProbeStatus = "transient_error"
	accountInspectionDeepProbeSkipped        accountInspectionDeepProbeStatus = "skipped"
)

const (
	accountInspectionAntigravityQuotaModeMaxUsed   accountInspectionAntigravityQuotaMode = "max-used"
	accountInspectionAntigravityQuotaModeClaudeGpt accountInspectionAntigravityQuotaMode = "claude-gpt"
)

const antigravityCodeAssistURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"

const (
	geminiCLIQuotaURL      = "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota"
	geminiCLICodeAssistURL = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
)

const (
	accountInspectionStateIdle      accountInspectionRunState = "idle"
	accountInspectionStateRunning   accountInspectionRunState = "running"
	accountInspectionStatePaused    accountInspectionRunState = "paused"
	accountInspectionStateStopping  accountInspectionRunState = "stopping"
	accountInspectionStateStopped   accountInspectionRunState = "stopped"
	accountInspectionStateCompleted accountInspectionRunState = "completed"
	accountInspectionStatePartial   accountInspectionRunState = "partial"
	accountInspectionStateFailed    accountInspectionRunState = "failed"
)

const accountInspectionResultSnapshotVersion = 1

var errAccountInspectionRestoredSnapshotReadOnly = errors.New("restored account inspection snapshot is read-only; run a new inspection first")
var errAccountInspectionResultStale = errors.New("account inspection result is stale or no longer available")
var errAccountInspectionSharedSourceDelete = errors.New("cannot delete one plugin virtual auth from a shared source file")

type accountInspectionProgress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	InFlight  int `json:"inFlight"`
	Pending   int `json:"pending"`
}

type accountInspectionStatus struct {
	State            accountInspectionRunState      `json:"state"`
	LastStartedAt    int64                          `json:"lastStartedAt"`
	LastFinishedAt   int64                          `json:"lastFinishedAt"`
	LastError        string                         `json:"lastError"`
	Progress         accountInspectionProgress      `json:"progress"`
	Summary          accountInspectionSummary       `json:"summary"`
	HealthCounts     *accountInspectionHealthCounts `json:"healthCounts,omitempty"`
	LogsPage         *accountInspectionPageInfo     `json:"logsPage,omitempty"`
	ResultsPage      *accountInspectionPageInfo     `json:"resultsPage,omitempty"`
	LogsLimited      bool                           `json:"logsLimited,omitempty"`
	ResultsLimited   bool                           `json:"resultsLimited,omitempty"`
	RestoredSnapshot bool                           `json:"restoredSnapshot,omitempty"`
	Logs             []accountInspectionLogEntry    `json:"logs"`
	Results          []accountInspectionResult      `json:"results"`
}

type accountInspectionResultSnapshot struct {
	Version        int                           `json:"version"`
	State          accountInspectionRunState     `json:"state"`
	LastStartedAt  int64                         `json:"lastStartedAt"`
	LastFinishedAt int64                         `json:"lastFinishedAt"`
	LastError      string                        `json:"lastError,omitempty"`
	Settings       accountInspectionSettings     `json:"settings"`
	Summary        accountInspectionSummary      `json:"summary"`
	HealthCounts   accountInspectionHealthCounts `json:"healthCounts"`
	Results        []accountInspectionResult     `json:"results"`
}

type accountInspectionPageInfo struct {
	Page       int  `json:"page"`
	PageSize   int  `json:"pageSize"`
	Total      int  `json:"total"`
	TotalPages int  `json:"totalPages"`
	HasMore    bool `json:"hasMore"`
}

type accountInspectionSnapshotOptions struct {
	IncludeDetails bool
	ResultPage     int
	ResultPageSize int
	ResultFilter   string
	ResultProvider string
	LogPage        int
	LogPageSize    int
	LogLevel       string
}

type accountInspectionHealthBucket string

const (
	accountInspectionHealthHealthy         accountInspectionHealthBucket = "healthy"
	accountInspectionHealthDisabled        accountInspectionHealthBucket = "disabled"
	accountInspectionHealthAuthInvalid     accountInspectionHealthBucket = "authInvalid"
	accountInspectionHealthQuotaExhausted  accountInspectionHealthBucket = "quotaExhausted"
	accountInspectionHealthInspectionError accountInspectionHealthBucket = "inspectionError"
	accountInspectionHealthRecoverable     accountInspectionHealthBucket = "recoverable"
)

type accountInspectionLogStreamMessage struct {
	Type     accountInspectionStreamMessageType `json:"type"`
	Schedule accountInspectionSchedule          `json:"schedule"`
	Status   accountInspectionStatus            `json:"status"`
	Log      *accountInspectionLogEntry         `json:"log,omitempty"`
}

type accountInspectionScheduler struct {
	h                       *Handler
	path                    string
	snapshotPath            string
	trigger                 chan struct{}
	mu                      sync.Mutex
	actionMu                sync.Mutex
	pause                   *sync.Cond
	cancel                  context.CancelFunc
	schedule                accountInspectionSchedule
	lastRunSettings         accountInspectionSettings
	status                  accountInspectionStatus
	healthCounts            accountInspectionHealthCounts
	autoActionConfirmations map[string]int
	subscribers             map[chan accountInspectionLogStreamMessage]struct{}
	lastProgressBroadcastAt int64
	xaiDeepProbeOnce        sync.Once
	xaiDeepProbeGate        chan struct{}
	runWG                   sync.WaitGroup
	stopped                 bool
}

type accountInspectionAccount struct {
	Auth        *coreauth.Auth
	Key         string
	Provider    string
	FileName    string
	DisplayName string
	Email       string
	Name        string
	AuthIndex   string
	Disabled    bool
}

type accountInspectionHTTPResult struct {
	StatusCode int
	Body       string
}

type accountInspectionDecision struct {
	Action          accountInspectionAction
	ActionReason    string
	UsedPercent     *float64
	IsQuota         bool
	Error           string
	ErrorDetail     string
	DeepProbeStatus accountInspectionDeepProbeStatus
	DeepProbeError  string
}

type accountInspectionActionItem struct {
	Key         string                  `json:"key"`
	Provider    string                  `json:"provider"`
	FileName    string                  `json:"fileName"`
	DisplayName string                  `json:"displayName"`
	Email       string                  `json:"email"`
	Name        string                  `json:"name"`
	AuthIndex   string                  `json:"authIndex"`
	Disabled    bool                    `json:"disabled"`
	Action      accountInspectionAction `json:"action"`
}

type accountInspectionActionRequest struct {
	Items []accountInspectionActionItem `json:"items"`
}

type accountInspectionOneRequest struct {
	Item accountInspectionActionItem `json:"item"`
}

type accountInspectionRefreshTokenRequest struct {
	Item accountInspectionActionItem `json:"item"`
}

type accountInspectionActionOutcome struct {
	Action      accountInspectionAction `json:"action"`
	FileName    string                  `json:"fileName"`
	DisplayName string                  `json:"displayName"`
	Email       string                  `json:"email"`
	Name        string                  `json:"name"`
	Provider    string                  `json:"provider"`
	AuthIndex   string                  `json:"authIndex"`
	Success     bool                    `json:"success"`
	Error       string                  `json:"error"`
}

func (h *Handler) startAccountInspectionScheduler() {
	if h == nil {
		return
	}
	if _, loaded := accountInspectionSchedulers.LoadOrStore(h, newAccountInspectionScheduler(h)); loaded {
		return
	}
	scheduler := schedulerForHandler(h)
	if scheduler != nil {
		embeddedusage.SetAccountInspectionScheduleHandlers(scheduler.exportSchedule, scheduler.importSchedule)
		embeddedusage.SetAccountInspectionSnapshotHandlers(scheduler.exportResultSnapshot, scheduler.importResultSnapshot)
		if h.authManager != nil {
			embeddedusage.SetAuthRuntimeStateImportHandler(h.authManager.ApplyImportedRuntimeState)
		}
		scheduler.cleanupLegacyQuotaCaches(context.Background())
		if h.lifecycleContext != nil {
			h.lifecycleWG.Add(1)
			go func() {
				defer h.lifecycleWG.Done()
				scheduler.loop(h.lifecycleContext)
			}()
		}
	}
	startRoutingPolicyController(h)
}

// Shutdown stops every background task owned by this management handler.
// It is safe to call more than once.
func (h *Handler) Shutdown() {
	if h == nil {
		return
	}
	h.shutdownOnce.Do(func() {
		if h.lifecycleCancel != nil {
			h.lifecycleCancel()
		}
		if scheduler := schedulerForHandler(h); scheduler != nil {
			scheduler.shutdown()
		}
		h.lifecycleWG.Wait()
		accountInspectionSchedulers.Delete(h)
		stopRoutingPolicyController(h)
		embeddedusage.SetAccountInspectionScheduleHandlers(nil, nil)
		embeddedusage.SetAccountInspectionSnapshotHandlers(nil, nil)
		embeddedusage.SetAuthRuntimeStateImportHandler(nil)
	})
}

func schedulerForHandler(h *Handler) *accountInspectionScheduler {
	if h == nil {
		return nil
	}
	value, ok := accountInspectionSchedulers.Load(h)
	if !ok {
		return nil
	}
	scheduler, _ := value.(*accountInspectionScheduler)
	return scheduler
}

func newAccountInspectionScheduler(h *Handler) *accountInspectionScheduler {
	schedulePath := accountInspectionSchedulePath()
	scheduler := &accountInspectionScheduler{
		h:                       h,
		path:                    schedulePath,
		snapshotPath:            accountInspectionResultSnapshotPath(schedulePath),
		trigger:                 make(chan struct{}, 1),
		subscribers:             make(map[chan accountInspectionLogStreamMessage]struct{}),
		autoActionConfirmations: make(map[string]int),
		schedule: accountInspectionSchedule{
			Enabled:         false,
			IntervalMinutes: accountInspectionDefaultIntervalMin,
			Settings:        defaultAccountInspectionSettings(),
		},
		status: accountInspectionStatus{State: accountInspectionStateIdle},
	}
	scheduler.lastRunSettings = scheduler.schedule.Settings
	scheduler.pause = sync.NewCond(&scheduler.mu)
	scheduler.load()
	return scheduler
}

func accountInspectionSchedulePath() string {
	if value := strings.TrimSpace(os.Getenv("ACCOUNT_INSPECTION_SCHEDULE_PATH")); value != "" {
		return value
	}
	dataDir := strings.TrimSpace(os.Getenv("USAGE_DATA_DIR"))
	if dataDir == "" {
		dataDir = "/CLIProxyAPI/usage"
	}
	return filepath.Join(dataDir, "account-inspection-schedule.json")
}

func accountInspectionResultSnapshotPath(schedulePath string) string {
	if value := strings.TrimSpace(os.Getenv("ACCOUNT_INSPECTION_SNAPSHOT_PATH")); value != "" {
		return value
	}
	return filepath.Join(filepath.Dir(schedulePath), "account-inspection-snapshot.json")
}

func defaultAccountInspectionSettings() accountInspectionSettings {
	return accountInspectionSettings{
		TargetType:                      accountInspectionProviderAll,
		Workers:                         4,
		DeleteWorkers:                   4,
		Timeout:                         accountInspectionDefaultTimeoutMS,
		Retries:                         0,
		UsedPercentThreshold:            100,
		SampleSize:                      0,
		AntigravityDeepProbeEnabled:     false,
		AntigravityDeepProbeModel:       "claude-sonnet-4-6",
		AntigravityQuotaMode:            accountInspectionAntigravityQuotaModeClaudeGpt,
		XAIDeepProbeEnabled:             false,
		XAIDeepProbeModel:               "grok-4.5",
		AutoExecuteQuotaLimitDisable:    false,
		AutoExecuteQuotaRecoveryEnable:  false,
		AutoExecuteAccountInvalidAction: accountInspectionActionNone,
		AutoExecuteRequestErrorAction:   accountInspectionActionNone,
		AutoExecuteConfirmations:        1,
	}
}

func normalizeAccountInspectionSchedule(input accountInspectionSchedule) accountInspectionSchedule {
	defaults := defaultAccountInspectionSettings()
	settings := input.Settings
	settings.TargetType = strings.ToLower(strings.TrimSpace(settings.TargetType))
	if settings.TargetType == "" {
		settings.TargetType = defaults.TargetType
	}
	if _, ok := accountInspectionSupportedProviders[settings.TargetType]; !ok && settings.TargetType != accountInspectionProviderAll {
		settings.TargetType = defaults.TargetType
	}
	if settings.Workers <= 0 {
		settings.Workers = defaults.Workers
	}
	if settings.Workers > accountInspectionMaxWorkers {
		settings.Workers = accountInspectionMaxWorkers
	}
	if settings.DeleteWorkers <= 0 {
		settings.DeleteWorkers = settings.Workers
	}
	if settings.DeleteWorkers > accountInspectionMaxDeleteWorkers {
		settings.DeleteWorkers = accountInspectionMaxDeleteWorkers
	}
	if settings.Timeout <= 0 {
		settings.Timeout = defaults.Timeout
	}
	if settings.Timeout < accountInspectionMinTimeoutMS {
		settings.Timeout = accountInspectionMinTimeoutMS
	}
	if settings.Timeout > accountInspectionMaxTimeoutMS {
		settings.Timeout = accountInspectionMaxTimeoutMS
	}
	if settings.Retries < 0 {
		settings.Retries = 0
	}
	if settings.Retries > accountInspectionMaxRetries {
		settings.Retries = accountInspectionMaxRetries
	}
	if settings.UsedPercentThreshold < 0 {
		settings.UsedPercentThreshold = 0
	}
	if settings.UsedPercentThreshold > 100 {
		settings.UsedPercentThreshold = 100
	}
	if settings.SampleSize < 0 {
		settings.SampleSize = 0
	}
	if settings.AutoExecuteConfirmations <= 0 {
		settings.AutoExecuteConfirmations = defaults.AutoExecuteConfirmations
	}
	if settings.AutoExecuteConfirmations > 5 {
		settings.AutoExecuteConfirmations = 5
	}
	settings.AntigravityDeepProbeModel = strings.TrimSpace(settings.AntigravityDeepProbeModel)
	if settings.AntigravityDeepProbeModel == "" {
		settings.AntigravityDeepProbeModel = defaults.AntigravityDeepProbeModel
	}
	settings.AntigravityQuotaMode = accountInspectionAntigravityQuotaMode(strings.ToLower(strings.TrimSpace(string(settings.AntigravityQuotaMode))))
	if settings.AntigravityQuotaMode != accountInspectionAntigravityQuotaModeMaxUsed && settings.AntigravityQuotaMode != accountInspectionAntigravityQuotaModeClaudeGpt {
		settings.AntigravityQuotaMode = defaults.AntigravityQuotaMode
	}
	settings.XAIDeepProbeModel = strings.TrimSpace(settings.XAIDeepProbeModel)
	if settings.XAIDeepProbeModel == "" {
		settings.XAIDeepProbeModel = defaults.XAIDeepProbeModel
	}
	settings.AutoExecuteAccountInvalidAction = normalizeAccountInspectionAutoAction(settings.AutoExecuteAccountInvalidAction)
	settings.AutoExecuteRequestErrorAction = normalizeAccountInspectionAutoAction(settings.AutoExecuteRequestErrorAction)
	input.Settings = settings
	if input.IntervalMinutes <= 0 {
		input.IntervalMinutes = accountInspectionDefaultIntervalMin
	}
	if input.Enabled && input.NextRunAt <= 0 {
		input.NextRunAt = time.Now().Add(time.Duration(input.IntervalMinutes) * time.Minute).UnixMilli()
	}
	if !input.Enabled {
		input.NextRunAt = 0
	}
	return input
}

func normalizeAccountInspectionAutoAction(action accountInspectionAction) accountInspectionAction {
	action = accountInspectionAction(strings.ToLower(strings.TrimSpace(string(action))))
	if action == accountInspectionActionDisable || action == accountInspectionActionDelete {
		return action
	}
	return accountInspectionActionNone
}

func (s *accountInspectionScheduler) load() {
	raw, err := os.ReadFile(s.path)
	if err == nil {
		var schedule accountInspectionSchedule
		if err := json.Unmarshal(raw, &schedule); err != nil {
			log.WithError(err).Warn("failed to load account inspection schedule")
		} else {
			s.schedule = normalizeAccountInspectionSchedule(schedule)
		}
	}
	if err := s.loadResultSnapshot(); err != nil {
		log.WithError(err).Warn("failed to load account inspection snapshot")
	}
}

func (s *accountInspectionScheduler) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.schedule, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(raw, '\n'), 0o600)
}

func normalizeAccountInspectionSnapshotState(state accountInspectionRunState) accountInspectionRunState {
	switch state {
	case accountInspectionStateStopped, accountInspectionStateCompleted, accountInspectionStatePartial, accountInspectionStateFailed:
		return state
	default:
		return accountInspectionStateCompleted
	}
}

func decodeAccountInspectionResultSnapshot(raw []byte) (accountInspectionResultSnapshot, error) {
	var snapshot accountInspectionResultSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return accountInspectionResultSnapshot{}, err
	}
	if snapshot.Version != accountInspectionResultSnapshotVersion {
		return accountInspectionResultSnapshot{}, fmt.Errorf("unsupported account inspection snapshot version %d", snapshot.Version)
	}
	if snapshot.LastFinishedAt <= 0 {
		return accountInspectionResultSnapshot{}, fmt.Errorf("account inspection snapshot is missing completion time")
	}
	if snapshot.LastStartedAt <= 0 || snapshot.LastStartedAt > snapshot.LastFinishedAt {
		snapshot.LastStartedAt = snapshot.LastFinishedAt
	}
	snapshot.State = normalizeAccountInspectionSnapshotState(snapshot.State)
	snapshot.Settings = normalizeAccountInspectionSchedule(accountInspectionSchedule{Settings: snapshot.Settings}).Settings
	snapshot.Results = sortAccountInspectionResults(snapshot.Results)
	snapshot.HealthCounts = accountInspectionResultHealthCounts(snapshot.Results)
	return snapshot, nil
}

func (s *accountInspectionScheduler) resultSnapshotLocked() (accountInspectionResultSnapshot, bool) {
	if s == nil || s.status.LastFinishedAt <= 0 {
		return accountInspectionResultSnapshot{}, false
	}
	settings := s.lastRunSettings
	if strings.TrimSpace(settings.TargetType) == "" {
		settings = s.schedule.Settings
	}
	return accountInspectionResultSnapshot{
		Version:        accountInspectionResultSnapshotVersion,
		State:          normalizeAccountInspectionSnapshotState(s.status.State),
		LastStartedAt:  s.status.LastStartedAt,
		LastFinishedAt: s.status.LastFinishedAt,
		LastError:      s.status.LastError,
		Settings:       settings,
		Summary:        s.status.Summary,
		HealthCounts:   s.healthCountsLocked(),
		Results:        append([]accountInspectionResult(nil), s.status.Results...),
	}, true
}

func (s *accountInspectionScheduler) applyResultSnapshotLocked(snapshot accountInspectionResultSnapshot, restored bool) {
	s.lastRunSettings = snapshot.Settings
	s.status.State = snapshot.State
	s.status.LastStartedAt = snapshot.LastStartedAt
	s.status.LastFinishedAt = snapshot.LastFinishedAt
	s.status.LastError = snapshot.LastError
	s.status.Progress = accountInspectionProgress{
		Total:     len(snapshot.Results),
		Completed: len(snapshot.Results),
	}
	s.status.Summary = snapshot.Summary
	s.status.Logs = nil
	s.status.Results = append([]accountInspectionResult(nil), snapshot.Results...)
	s.status.RestoredSnapshot = restored
	s.healthCounts = snapshot.HealthCounts
}

func (s *accountInspectionScheduler) saveResultSnapshotLocked() error {
	snapshot, ok := s.resultSnapshotLocked()
	if !ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.snapshotPath), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.snapshotPath, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(s.snapshotPath, 0o600)
}

func (s *accountInspectionScheduler) loadResultSnapshot() error {
	if s == nil {
		return nil
	}
	raw, err := os.ReadFile(s.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	snapshot, err := decodeAccountInspectionResultSnapshot(raw)
	if err != nil {
		return err
	}
	s.applyResultSnapshotLocked(snapshot, true)
	return nil
}

func (s *accountInspectionScheduler) exportResultSnapshot() ([]byte, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.Lock()
	snapshot, ok := s.resultSnapshotLocked()
	s.mu.Unlock()
	if !ok {
		raw, err := os.ReadFile(s.snapshotPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, false, nil
			}
			return nil, false, err
		}
		snapshot, err = decodeAccountInspectionResultSnapshot(raw)
		if err != nil {
			return nil, false, err
		}
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (s *accountInspectionScheduler) importResultSnapshot(raw []byte) error {
	if s == nil {
		return nil
	}
	snapshot, err := decodeAccountInspectionResultSnapshot(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.isRunningLocked() {
		s.mu.Unlock()
		return fmt.Errorf("account inspection is running")
	}
	s.applyResultSnapshotLocked(snapshot, true)
	err = s.saveResultSnapshotLocked()
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	return err
}

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
		IncludeDetails: value != "0" && value != "false" && value != "summary",
		ResultPage:     parseAccountInspectionQueryInt(c, "result_page", 1),
		ResultPageSize: resultPageSize,
		ResultFilter:   strings.ToLower(strings.TrimSpace(c.Query("result_filter"))),
		ResultProvider: strings.ToLower(strings.TrimSpace(c.Query("result_provider"))),
		LogPage:        parseAccountInspectionQueryInt(c, "log_page", 1),
		LogPageSize:    logPageSize,
		LogLevel:       strings.ToLower(strings.TrimSpace(c.Query("log_level"))),
	}
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

func (s *accountInspectionScheduler) snapshotForRequest(c *gin.Context) gin.H {
	return s.snapshotWithOptions(accountInspectionRequestSnapshotOptions(c))
}

func accountInspectionResultHealthCounts(results []accountInspectionResult) accountInspectionHealthCounts {
	counts := accountInspectionHealthCounts{Total: len(results)}
	for _, result := range results {
		switch accountInspectionResultHealthBucketOf(result) {
		case accountInspectionHealthAuthInvalid:
			counts.AuthInvalid++
		case accountInspectionHealthInspectionError:
			counts.InspectionError++
		case accountInspectionHealthQuotaExhausted:
			counts.QuotaExhausted++
		case accountInspectionHealthRecoverable:
			counts.Recoverable++
		case accountInspectionHealthDisabled:
			counts.Disabled++
		default:
			counts.Healthy++
		}
	}
	return counts
}

func adjustAccountInspectionHealthCountsForResult(counts accountInspectionHealthCounts, result accountInspectionResult, delta int) accountInspectionHealthCounts {
	counts.Total += delta
	switch accountInspectionResultHealthBucketOf(result) {
	case accountInspectionHealthAuthInvalid:
		counts.AuthInvalid += delta
	case accountInspectionHealthInspectionError:
		counts.InspectionError += delta
	case accountInspectionHealthQuotaExhausted:
		counts.QuotaExhausted += delta
	case accountInspectionHealthRecoverable:
		counts.Recoverable += delta
	case accountInspectionHealthDisabled:
		counts.Disabled += delta
	default:
		counts.Healthy += delta
	}
	return counts
}

func (s *accountInspectionScheduler) healthCountsLocked() accountInspectionHealthCounts {
	if s.healthCounts.Total == len(s.status.Results) {
		return s.healthCounts
	}
	s.healthCounts = accountInspectionResultHealthCounts(s.status.Results)
	return s.healthCounts
}

func accountInspectionResultHealthBucketOf(result accountInspectionResult) accountInspectionHealthBucket {
	switch {
	case result.Action == accountInspectionActionDelete || isAccountErrorStatusPtr(result.StatusCode):
		return accountInspectionHealthAuthInvalid
	case result.Error != "":
		return accountInspectionHealthInspectionError
	case result.IsQuota || result.Action == accountInspectionActionDisable:
		return accountInspectionHealthQuotaExhausted
	case result.Action == accountInspectionActionEnable:
		return accountInspectionHealthRecoverable
	case result.Disabled:
		return accountInspectionHealthDisabled
	default:
		return accountInspectionHealthHealthy
	}
}

func isAccountErrorStatusPtr(status *int) bool {
	return status != nil && isAccountErrorStatus(*status)
}

func accountInspectionResultMatchesFilter(result accountInspectionResult, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	switch filter {
	case "", "all":
		return true
	case "pending":
		return result.Action != accountInspectionActionKeep && !result.Executed
	case "accountinvalid", "account-invalid", "account_invalid", "authinvalid", "auth-invalid", "auth_invalid":
		return accountInspectionResultHealthBucketOf(result) == accountInspectionHealthAuthInvalid
	case "requesterror", "request-error", "request_error", "inspectionerror", "inspection-error", "inspection_error":
		return accountInspectionResultHealthBucketOf(result) == accountInspectionHealthInspectionError
	case "quotaexhausted", "quota-exhausted", "quota_exhausted":
		return accountInspectionResultHealthBucketOf(result) == accountInspectionHealthQuotaExhausted
	case "recoverable":
		return accountInspectionResultHealthBucketOf(result) == accountInspectionHealthRecoverable
	case "highavailable", "high-available", "high_available", "healthy":
		return accountInspectionResultHealthBucketOf(result) == accountInspectionHealthHealthy
	default:
		return true
	}
}

func accountInspectionResultMatchesProvider(result accountInspectionResult, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return provider == "" || provider == accountInspectionProviderAll || strings.EqualFold(result.Provider, provider)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func normalizeAccountInspectionPage(page int) int {
	if page <= 0 {
		return 1
	}
	return page
}

func normalizeAccountInspectionPageSize(size int, fallback int, maxSize int) int {
	if size <= 0 {
		size = fallback
	}
	if size > maxSize {
		return maxSize
	}
	return size
}

func accountInspectionPageInfoFor(total int, page int, pageSize int) accountInspectionPageInfo {
	page = normalizeAccountInspectionPage(page)
	if pageSize <= 0 {
		pageSize = 1
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	start := (page - 1) * pageSize
	return accountInspectionPageInfo{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		HasMore:    start+pageSize < total,
	}
}

func paginateAccountInspectionLogs(logs []accountInspectionLogEntry, page int, pageSize int, level string) ([]accountInspectionLogEntry, accountInspectionPageInfo) {
	page = normalizeAccountInspectionPage(page)
	pageSize = normalizeAccountInspectionPageSize(pageSize, 100, accountInspectionMaxLogPageSize)
	filtered := make([]accountInspectionLogEntry, 0, len(logs))
	for _, entry := range logs {
		if level == "" || level == "all" || strings.EqualFold(entry.Level, level) {
			filtered = append(filtered, entry)
		}
	}
	total := len(filtered)
	info := accountInspectionPageInfoFor(total, page, pageSize)
	if total == 0 {
		return []accountInspectionLogEntry{}, info
	}
	end := total - (page-1)*pageSize
	if end <= 0 {
		return []accountInspectionLogEntry{}, info
	}
	start := maxInt(0, end-pageSize)
	return append([]accountInspectionLogEntry(nil), filtered[start:end]...), info
}

func paginateAccountInspectionResults(results []accountInspectionResult, page int, pageSize int, filter string, provider string) ([]accountInspectionResult, accountInspectionPageInfo) {
	page = normalizeAccountInspectionPage(page)
	pageSize = normalizeAccountInspectionPageSize(pageSize, 100, accountInspectionMaxResultPageSize)
	filtered := make([]accountInspectionResult, 0, len(results))
	for _, result := range results {
		if accountInspectionResultMatchesFilter(result, filter) && accountInspectionResultMatchesProvider(result, provider) {
			filtered = append(filtered, result)
		}
	}
	total := len(filtered)
	info := accountInspectionPageInfoFor(total, page, pageSize)
	start := (page - 1) * pageSize
	if start >= total {
		return []accountInspectionResult{}, info
	}
	end := minInt(total, start+pageSize)
	return append([]accountInspectionResult(nil), filtered[start:end]...), info
}

func (s *accountInspectionScheduler) streamStatusLocked(options accountInspectionSnapshotOptions) accountInspectionStatus {
	status := s.status
	if options.IncludeDetails {
		healthCounts := s.healthCountsLocked()
		logs, logsPage := paginateAccountInspectionLogs(s.status.Logs, options.LogPage, options.LogPageSize, options.LogLevel)
		results, resultsPage := paginateAccountInspectionResults(s.status.Results, options.ResultPage, options.ResultPageSize, options.ResultFilter, options.ResultProvider)
		status.HealthCounts = &healthCounts
		status.Logs = logs
		status.Results = results
		status.LogsPage = &logsPage
		status.ResultsPage = &resultsPage
		status.LogsLimited = logsPage.Total > len(logs)
		status.ResultsLimited = resultsPage.Total > len(results)
	} else {
		status.HealthCounts = nil
		status.LogsPage = nil
		status.ResultsPage = nil
		status.Logs = nil
		status.Results = nil
		status.LogsLimited = false
		status.ResultsLimited = false
	}
	return status
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

func (s *accountInspectionScheduler) isRunningLocked() bool {
	return s.status.State == accountInspectionStateRunning || s.status.State == accountInspectionStatePaused || s.status.State == accountInspectionStateStopping
}

func (s *accountInspectionScheduler) isPausedLocked() bool {
	return s.status.State == accountInspectionStatePaused
}

func (s *accountInspectionScheduler) isStoppingLocked() bool {
	return s.status.State == accountInspectionStateStopping
}

func (s *accountInspectionScheduler) setRunStateLocked(state accountInspectionRunState) {
	s.status.State = state
}

func (s *accountInspectionScheduler) exportSchedule() ([]byte, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	s.mu.Lock()
	schedule := s.schedule
	s.mu.Unlock()
	raw, err := json.Marshal(schedule)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func (s *accountInspectionScheduler) importSchedule(raw []byte) error {
	if s == nil {
		return nil
	}
	var schedule accountInspectionSchedule
	if err := json.Unmarshal(raw, &schedule); err != nil {
		return err
	}
	schedule.NextRunAt = 0
	return s.update(schedule)
}

func (s *accountInspectionScheduler) update(schedule accountInspectionSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return fmt.Errorf("account inspection scheduler is shut down")
	}
	previousNextRunAt := s.schedule.NextRunAt
	s.schedule = normalizeAccountInspectionSchedule(schedule)
	if s.schedule.Enabled && previousNextRunAt > 0 && schedule.NextRunAt == 0 {
		s.schedule.NextRunAt = previousNextRunAt
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	select {
	case s.trigger <- struct{}{}:
	default:
	}
	return nil
}

func (s *accountInspectionScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		s.maybeRunDue()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.trigger:
		}
	}
}

func (s *accountInspectionScheduler) maybeRunDue() {
	s.mu.Lock()
	schedule := s.schedule
	running := s.isRunningLocked()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped || !schedule.Enabled || running || schedule.NextRunAt <= 0 || time.Now().UnixMilli() < schedule.NextRunAt {
		return
	}
	go func() { _ = s.startRun(false) }()
}

func (s *accountInspectionScheduler) startRun(manual bool) error {
	baseContext := context.Background()
	if s != nil && s.h != nil && s.h.lifecycleContext != nil {
		baseContext = s.h.lifecycleContext
	}
	ctx, cancel := context.WithTimeout(baseContext, accountInspectionMaxRunDuration)
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("account inspection scheduler is shut down")
	}
	if s.isRunningLocked() {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("account inspection already running")
	}
	s.cancel = cancel
	s.lastRunSettings = s.schedule.Settings
	s.setRunStateLocked(accountInspectionStateRunning)
	s.status.RestoredSnapshot = false
	s.status.LastStartedAt = time.Now().UnixMilli()
	s.status.LastFinishedAt = 0
	s.status.LastError = ""
	s.status.Progress = accountInspectionProgress{}
	s.status.Summary = accountInspectionSummary{}
	s.status.Logs = nil
	s.status.Results = nil
	s.healthCounts = accountInspectionHealthCounts{}
	schedule := s.schedule
	s.runWG.Add(2)
	s.mu.Unlock()

	go func() {
		defer s.runWG.Done()
		<-ctx.Done()
		s.mu.Lock()
		s.pause.Broadcast()
		s.mu.Unlock()
	}()
	go func() {
		defer s.runWG.Done()
		s.run(ctx, cancel, schedule, manual)
	}()
	return nil
}

func (s *accountInspectionScheduler) shutdown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	cancel := s.cancel
	s.pause.Broadcast()
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.runWG.Wait()
}

func (s *accountInspectionScheduler) appendLog(level string, message string) {
	entry := accountInspectionLogEntry{Time: time.Now().UnixMilli(), Level: level, Message: message}
	s.mu.Lock()
	s.status.Logs = append(s.status.Logs, entry)
	if len(s.status.Logs) > 200 {
		s.status.Logs = s.status.Logs[len(s.status.Logs)-200:]
	}
	broadcast := s.logBroadcastLocked(entry)
	s.mu.Unlock()
	broadcast.send()
}

func (s *accountInspectionScheduler) updateProgress(total int, completed int, inFlight int, force bool) {
	pending := total - completed - inFlight
	if pending < 0 {
		pending = 0
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	previous := s.status.Progress
	next := accountInspectionProgress{Total: total, Completed: completed, InFlight: inFlight, Pending: pending}
	if previous == next {
		s.mu.Unlock()
		return
	}
	s.status.Progress = next
	shouldBroadcast := force || completed == total || now-s.lastProgressBroadcastAt >= accountInspectionProgressBroadcastGap.Milliseconds()
	var broadcast accountInspectionBroadcast
	if shouldBroadcast {
		s.lastProgressBroadcastAt = now
		broadcast = s.statusBroadcastLocked()
	}
	s.mu.Unlock()
	broadcast.send()
}

func (s *accountInspectionScheduler) waitIfPaused(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.isPausedLocked() && !s.isStoppingLocked() {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.pause.Wait()
	}
	return ctx.Err()
}

func (s *accountInspectionScheduler) pauseRun() {
	var broadcast accountInspectionBroadcast
	s.mu.Lock()
	if s.isRunningLocked() && !s.isStoppingLocked() {
		s.setRunStateLocked(accountInspectionStatePaused)
		broadcast = s.statusBroadcastLocked()
	}
	s.mu.Unlock()
	broadcast.send()
}

func (s *accountInspectionScheduler) resumeRun() {
	var broadcast accountInspectionBroadcast
	s.mu.Lock()
	if s.isRunningLocked() && s.isPausedLocked() {
		s.setRunStateLocked(accountInspectionStateRunning)
		broadcast = s.statusBroadcastLocked()
		s.pause.Broadcast()
	}
	s.mu.Unlock()
	broadcast.send()
}

func (s *accountInspectionScheduler) stopRun() {
	var broadcast accountInspectionBroadcast
	s.mu.Lock()
	cancel := s.cancel
	if s.isRunningLocked() {
		s.setRunStateLocked(accountInspectionStateStopping)
		broadcast = s.statusBroadcastLocked()
		s.pause.Broadcast()
	}
	s.mu.Unlock()
	broadcast.send()
	if cancel != nil {
		cancel()
	}
}

func (s *accountInspectionScheduler) inspectOne(ctx context.Context, item accountInspectionActionItem) (accountInspectionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, accountInspectionMaxRunDuration)
	defer cancel()
	s.mu.Lock()
	if s.status.RestoredSnapshot {
		s.mu.Unlock()
		return accountInspectionResult{}, errAccountInspectionRestoredSnapshotReadOnly
	}
	if s.isRunningLocked() {
		s.mu.Unlock()
		return accountInspectionResult{}, fmt.Errorf("account inspection already running")
	}
	schedule := s.schedule
	s.mu.Unlock()
	boundItem, err := s.bindActionItemToSnapshot(item)
	if err != nil {
		return accountInspectionResult{}, err
	}

	result, _, runErr := s.executeSingleInspection(ctx, schedule.Settings, boundItem)
	if runErr != nil {
		s.appendLog("error", fmt.Sprintf("重新检查失败：%s", runErr.Error()))
		return result, runErr
	}

	s.mu.Lock()
	var saveErr error
	if !s.isRunningLocked() {
		s.mergeSingleInspectionResultLocked(result)
		s.status.Results = sortAccountInspectionResults(s.status.Results)
		saveErr = s.saveResultSnapshotLocked()
	}
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if saveErr != nil {
		return result, fmt.Errorf("failed to save account inspection snapshot: %w", saveErr)
	}
	return result, nil
}

func (s *accountInspectionScheduler) refreshTokenNow(ctx context.Context, item accountInspectionActionItem) (accountInspectionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return accountInspectionResult{}, fmt.Errorf("account inspection scheduler unavailable")
	}
	s.mu.Lock()
	restoredSnapshot := s.status.RestoredSnapshot
	s.mu.Unlock()
	if restoredSnapshot {
		return accountInspectionResult{}, errAccountInspectionRestoredSnapshotReadOnly
	}
	if s.h == nil || s.h.authManager == nil {
		return accountInspectionResult{}, fmt.Errorf("core auth manager unavailable")
	}
	boundItem, err := s.bindActionItemToSnapshot(item)
	if err != nil {
		return accountInspectionResult{}, err
	}
	auths, err := s.auths()
	if err != nil {
		return accountInspectionResult{}, err
	}
	for _, auth := range auths {
		account := accountFromAuth(auth)
		if boundItem.Key != "" && account.Key != boundItem.Key {
			continue
		}
		if boundItem.Key == "" && (account.FileName != boundItem.FileName || account.AuthIndex != boundItem.AuthIndex) {
			continue
		}
		result := account.baseResult()
		result.TokenRefreshTriggered = true
		if account.Auth == nil || account.Auth.ID == "" {
			result.TokenRefreshStatus = "failed"
			result.TokenRefreshError = "missing auth id"
			result.Error = result.TokenRefreshError
			result.ErrorCode = "missing_auth_id"
			result.ActionReason = "刷新令牌失败，保留账号"
			s.syncInspectionAuthError(ctx, account, "token_refresh_error", result.TokenRefreshError, 0)
			return result, errors.New(result.TokenRefreshError)
		}
		s.appendLog("info", fmt.Sprintf("主动刷新令牌 %s", account.identity()))
		updated, refreshed, refreshErr := s.h.authManager.ForceRefreshForInspection(ctx, account.Auth.ID)
		if updated != nil {
			account = accountFromAuth(updated)
			result = account.baseResult()
		}
		result.TokenRefreshTriggered = true
		result.NextRefreshAt = account.nextRefreshAtMillis()
		if refreshErr != nil {
			result.TokenRefreshStatus = "failed"
			result.TokenRefreshError = refreshErr.Error()
			result.Error = refreshErr.Error()
			result.ErrorCode = "token_refresh_error"
			result.ActionReason = "刷新令牌失败，保留账号"
			s.syncInspectionAuthError(ctx, account, "token_refresh_error", refreshErr.Error(), 0)
			s.appendLog("warning", fmt.Sprintf("%s 主动刷新令牌失败：%s", account.identity(), refreshErr.Error()))
			return result, refreshErr
		}
		if refreshed {
			result.TokenRefreshStatus = "success"
			s.appendLog("success", fmt.Sprintf("%s 主动刷新令牌成功", account.identity()))
		} else {
			result.TokenRefreshStatus = ""
			s.appendLog("warning", fmt.Sprintf("%s 主动刷新令牌未执行", account.identity()))
		}
		return result, nil
	}
	return accountInspectionResult{}, fmt.Errorf("account not found")
}

func sameAccountInspectionResult(a accountInspectionResult, b accountInspectionResult) bool {
	return a.Key == b.Key || (a.FileName == b.FileName && a.AuthIndex == b.AuthIndex)
}

func (s *accountInspectionScheduler) updateInspectionResultLocked(result accountInspectionResult, appendMissing bool, update func(accountInspectionResult) (accountInspectionResult, bool)) bool {
	if result.Key == "" {
		return false
	}

	for index, current := range s.status.Results {
		if sameAccountInspectionResult(current, result) {
			merged, updateSummary := update(current)
			if updateSummary {
				s.status.Summary = adjustAccountInspectionSummaryForResult(s.status.Summary, current, -1)
				s.status.Summary = adjustAccountInspectionSummaryForResult(s.status.Summary, merged, 1)
			}
			s.healthCounts = adjustAccountInspectionHealthCountsForResult(s.healthCounts, current, -1)
			s.healthCounts = adjustAccountInspectionHealthCountsForResult(s.healthCounts, merged, 1)
			s.status.Results[index] = merged
			return true
		}
	}

	if !appendMissing {
		return false
	}
	s.status.Summary = adjustAccountInspectionSummaryForResult(s.status.Summary, result, 1)
	s.healthCounts = adjustAccountInspectionHealthCountsForResult(s.healthCounts, result, 1)
	s.status.Results = append(s.status.Results, result)
	return true
}

func (s *accountInspectionScheduler) mergeTokenRefreshResultLocked(result accountInspectionResult) {
	s.updateInspectionResultLocked(result, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		current.Provider = result.Provider
		current.FileName = result.FileName
		current.DisplayName = result.DisplayName
		current.Email = result.Email
		current.Name = result.Name
		current.AuthIndex = result.AuthIndex
		current.Disabled = result.Disabled
		current.TokenRefreshTriggered = result.TokenRefreshTriggered
		current.TokenRefreshStatus = result.TokenRefreshStatus
		current.TokenRefreshError = result.TokenRefreshError
		current.NextRefreshAt = result.NextRefreshAt
		if result.TokenRefreshStatus == "failed" {
			current.Error = result.Error
			current.ErrorDetail = result.ErrorDetail
			current.ErrorCode = result.ErrorCode
			current.ActionReason = result.ActionReason
			return current, true
		}
		if result.TokenRefreshStatus == "success" && current.ErrorCode == "token_refresh_error" {
			current.Error = ""
			current.ErrorDetail = ""
			current.ErrorCode = ""
			current.ActionReason = result.ActionReason
			return current, true
		}
		return current, false
	})
}

func (s *accountInspectionScheduler) mergeSingleInspectionResultLocked(result accountInspectionResult) {
	s.updateInspectionResultLocked(result, false, func(current accountInspectionResult) (accountInspectionResult, bool) {
		result.Executed = current.Executed
		result.ExecuteError = current.ExecuteError
		return result, true
	})
}

func (s *accountInspectionScheduler) executeSingleInspection(ctx context.Context, settings accountInspectionSettings, item accountInspectionActionItem) (accountInspectionResult, accountInspectionSummary, error) {
	auths, err := s.auths()
	if err != nil {
		return accountInspectionResult{}, accountInspectionSummary{}, err
	}
	for _, auth := range auths {
		account := accountFromAuth(auth)
		if item.Key != "" && account.Key != item.Key {
			continue
		}
		if item.Key == "" && (account.FileName != item.FileName || account.AuthIndex != item.AuthIndex) {
			continue
		}
		if !shouldInspectAccount(account, accountInspectionProviderAll) {
			return accountInspectionResult{}, accountInspectionSummary{}, fmt.Errorf("unsupported provider")
		}
		s.appendLog("info", fmt.Sprintf("重新检查 %s", account.identity()))
		result := s.inspectAccount(ctx, account, settings, make(chan struct{}, accountInspectionMaxRefreshConcurrency))
		return result, summarizeAccountInspection(len(auths), 1, []accountInspectionAccount{account}, []accountInspectionResult{result}), nil
	}
	return accountInspectionResult{}, accountInspectionSummary{}, fmt.Errorf("account not found")
}

func (s *accountInspectionScheduler) run(ctx context.Context, cancel context.CancelFunc, schedule accountInspectionSchedule, manual bool) {
	defer cancel()
	s.appendLog("info", "后端账号巡检开始")
	results, summary, runErr := s.executeInspection(ctx, schedule.Settings)
	finishedAt := time.Now().UnixMilli()
	state := accountInspectionStateCompleted
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			state = accountInspectionStateStopped
		} else if errors.Is(runErr, context.DeadlineExceeded) {
			state = accountInspectionStatePartial
		} else {
			state = accountInspectionStateFailed
		}
	}

	s.mu.Lock()
	s.setRunStateLocked(state)
	s.status.LastFinishedAt = finishedAt
	s.status.Summary = summary
	s.status.Results = results
	s.status.RestoredSnapshot = false
	s.healthCounts = accountInspectionResultHealthCounts(results)
	completed := s.status.Progress.Completed
	if state == accountInspectionStateCompleted {
		completed = len(results)
	} else if completed > len(results) {
		completed = len(results)
	}
	s.status.Progress.Completed = completed
	s.status.Progress.InFlight = 0
	s.status.Progress.Pending = 0
	if runErr != nil {
		s.status.LastError = runErr.Error()
	} else {
		s.status.LastError = ""
	}
	s.cancel = nil
	broadcast := s.statusBroadcastLocked()
	if s.schedule.Enabled && !manual {
		s.schedule.NextRunAt = time.Now().Add(time.Duration(s.schedule.IntervalMinutes) * time.Minute).UnixMilli()
		if err := s.saveLocked(); err != nil {
			log.WithError(err).Warn("failed to save next account inspection run time")
		}
	}
	if err := s.saveResultSnapshotLocked(); err != nil {
		log.WithError(err).Warn("failed to save account inspection snapshot")
	}
	s.mu.Unlock()
	broadcast.send()
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

func runAccountInspectionWorkers(total int, workers int, beforeNext func() bool, run func(index int) bool) {
	if workers <= 0 {
		workers = 1
	}
	cursor := 0
	var cursorMu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers && i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if beforeNext != nil && !beforeNext() {
					return
				}
				cursorMu.Lock()
				index := cursor
				cursor++
				cursorMu.Unlock()
				if index >= total {
					return
				}
				if !run(index) {
					return
				}
			}
		}()
	}
	wg.Wait()
}

func (s *accountInspectionScheduler) executeInspection(ctx context.Context, settings accountInspectionSettings) ([]accountInspectionResult, accountInspectionSummary, error) {
	auths, err := s.auths()
	if err != nil {
		return nil, accountInspectionSummary{}, err
	}
	liveAuths := make([]*coreauth.Auth, 0, len(auths))
	accounts := make([]accountInspectionAccount, 0, len(auths))
	existingPaths := make(map[string]bool)
	for _, auth := range auths {
		liveAuths = append(liveAuths, auth)
		account := accountFromAuth(auth)
		if shouldInspectAccount(account, settings.TargetType) {
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].FileName == accounts[j].FileName {
			return accounts[i].AuthIndex < accounts[j].AuthIndex
		}
		return accounts[i].FileName < accounts[j].FileName
	})
	probeSetCount := len(accounts)
	accounts = sampleAccounts(accounts, settings.SampleSize)
	accounts = s.filterExistingAccounts(accounts, existingPaths)
	s.appendLog("info", fmt.Sprintf("巡检集合 %d 个账号，本次探测 %d 个账号", probeSetCount, len(accounts)))

	results := make([]accountInspectionResult, len(accounts))
	providerLimiters := accountInspectionProviderLimiters()
	refreshLimiter := make(chan struct{}, accountInspectionMaxRefreshConcurrency)
	completed := 0
	inFlight := 0
	var progressMu sync.Mutex
	var runErr error
	var runErrOnce sync.Once
	setRunErr := func(err error) {
		if err == nil {
			return
		}
		runErrOnce.Do(func() { runErr = err })
	}
	s.updateProgress(len(accounts), 0, 0, true)
	runAccountInspectionWorkers(
		len(accounts),
		settings.Workers,
		func() bool {
			if err := s.waitIfPaused(ctx); err != nil {
				setRunErr(err)
				return false
			}
			return true
		},
		func(index int) bool {
			account := accounts[index]
			limiter := providerLimiters[account.Provider]
			if limiter == nil {
				limiter = make(chan struct{}, accountInspectionMaxProviderConcurrency)
			}
			select {
			case limiter <- struct{}{}:
			case <-ctx.Done():
				setRunErr(ctx.Err())
				return false
			}
			progressMu.Lock()
			inFlight++
			s.updateProgress(len(accounts), completed, inFlight, false)
			progressMu.Unlock()
			results[index] = s.inspectAccount(ctx, account, settings, refreshLimiter)
			<-limiter
			progressMu.Lock()
			inFlight--
			completed++
			s.updateProgress(len(accounts), completed, inFlight, false)
			progressMu.Unlock()
			return true
		},
	)
	if runErr != nil {
		partial := completedInspectionResults(results)
		return partial, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, partial), runErr
	}
	if err := ctx.Err(); err != nil {
		partial := completedInspectionResults(results)
		return partial, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, partial), err
	}

	s.applyAutomaticActions(ctx, results, settings)
	return results, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, results), nil
}

func completedInspectionResults(results []accountInspectionResult) []accountInspectionResult {
	out := make([]accountInspectionResult, 0, len(results))
	for _, result := range results {
		if result.Key == "" {
			continue
		}
		out = append(out, result)
	}
	return out
}

func accountInspectionProviderLimiters() map[string]chan struct{} {
	limiters := make(map[string]chan struct{}, len(accountInspectionSupportedProviders))
	for provider := range accountInspectionSupportedProviders {
		limiters[provider] = make(chan struct{}, accountInspectionMaxProviderConcurrency)
	}
	return limiters
}

func (s *accountInspectionScheduler) auths() ([]*coreauth.Auth, error) {
	if s.h == nil {
		return nil, fmt.Errorf("management handler unavailable")
	}
	s.h.mu.Lock()
	manager := s.h.authManager
	s.h.mu.Unlock()
	if manager == nil {
		return nil, fmt.Errorf("core auth manager unavailable")
	}
	return manager.List(), nil
}

func (s *accountInspectionScheduler) filterExistingAccounts(accounts []accountInspectionAccount, existingPaths map[string]bool) []accountInspectionAccount {
	out := accounts[:0]
	for _, account := range accounts {
		if s.authFileExists(account.Auth, existingPaths) {
			out = append(out, account)
		}
	}
	return out
}

func (s *accountInspectionScheduler) authFileExists(auth *coreauth.Auth, existingPaths map[string]bool) bool {
	if auth == nil {
		return false
	}
	if isRuntimeOnlyAuth(auth) {
		return true
	}
	path := strings.TrimSpace(authAttribute(auth, "path"))
	if path == "" && s.h != nil && s.h.cfg != nil {
		fileName := strings.TrimSpace(auth.FileName)
		if fileName != "" {
			path = filepath.Join(s.h.cfg.AuthDir, filepath.Base(fileName))
		}
	}
	if path == "" {
		return true
	}
	if exists, ok := existingPaths[path]; ok {
		return exists
	}
	_, err := os.Stat(path)
	exists := err == nil || !os.IsNotExist(err)
	existingPaths[path] = exists
	return exists
}

func accountInspectionKey(fileName string, authIndex string) string {
	return fileName + "::" + firstNonEmptyStringValue(authIndex, "-")
}

func accountFromAuth(auth *coreauth.Auth) accountInspectionAccount {
	if auth == nil {
		return accountInspectionAccount{}
	}
	auth.EnsureIndex()
	provider := accountInspectionProvider(auth)
	fileName := strings.TrimSpace(auth.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(auth.ID)
	}
	name := firstNonEmptyAuthValue(auth, "name")
	email := accountInspectionAuthEmail(auth)
	displayName := firstNonEmptyStringValue(email, name)
	if displayName == "" {
		displayName = "-"
	}
	return accountInspectionAccount{
		Auth:        auth,
		Key:         accountInspectionKey(fileName, auth.Index),
		Provider:    provider,
		FileName:    fileName,
		DisplayName: displayName,
		Email:       email,
		Name:        name,
		AuthIndex:   auth.Index,
		Disabled:    auth.Disabled,
	}
}

func accountInspectionProvider(auth *coreauth.Auth) string {
	return strings.ToLower(strings.TrimSpace(auth.Provider))
}

func isAccountInspectionAPIKeyAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	label := strings.ToLower(strings.TrimSpace(auth.Label))
	if strings.Contains(label, "apikey") || strings.Contains(label, "api-key") {
		return true
	}
	source := strings.ToLower(strings.TrimSpace(authAttribute(auth, "source")))
	if strings.HasPrefix(source, "config:") && strings.TrimSpace(authAttribute(auth, "api_key")) != "" {
		return true
	}
	return strings.TrimSpace(authAttribute(auth, "api_key")) != "" && strings.TrimSpace(authAttribute(auth, "path")) == ""
}

func shouldInspectAccount(account accountInspectionAccount, targetType string) bool {
	if account.Auth == nil {
		return false
	}
	if isAccountInspectionAPIKeyAuth(account.Auth) {
		return false
	}
	if _, ok := accountInspectionSupportedProviders[account.Provider]; !ok {
		return false
	}
	return targetType == accountInspectionProviderAll || targetType == account.Provider
}

func sampleAccounts(accounts []accountInspectionAccount, sampleSize int) []accountInspectionAccount {
	if sampleSize <= 0 || sampleSize >= len(accounts) {
		return accounts
	}
	out := append([]accountInspectionAccount(nil), accounts...)
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out[:sampleSize]
}

func (s *accountInspectionScheduler) inspectAccount(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, refreshLimiter chan struct{}) accountInspectionResult {
	result := account.baseResult()
	if account.AuthIndex == "" {
		result.ActionReason = "缺少 auth_index，保留账号"
		result.Error = "missing auth_index"
		result.ErrorCode = "missing_auth_index"
		return result
	}
	if refreshed, refreshTriggered, refreshErr := s.refreshAccountIfDue(ctx, account, refreshLimiter); refreshErr != nil {
		result.TokenRefreshTriggered = refreshTriggered
		result.NextRefreshAt = account.nextRefreshAtMillis()
		if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
			result.Error = refreshErr.Error()
			result.ActionReason = "巡检已取消，保留账号"
			return result
		}
		result.TokenRefreshStatus = "failed"
		result.TokenRefreshError = refreshErr.Error()
		result.Error = refreshErr.Error()
		result.ErrorCode = "token_refresh_error"
		result.ActionReason = "刷新令牌失败，保留账号"
		s.syncInspectionAuthError(ctx, account, "token_refresh_error", refreshErr.Error(), 0)
		s.appendLog("warning", fmt.Sprintf("%s 刷新令牌失败，保留账号：%s", account.identity(), refreshErr.Error()))
		return result
	} else if refreshTriggered {
		account = refreshed
		result = account.baseResult()
		result.TokenRefreshTriggered = true
		result.TokenRefreshStatus = "success"
	} else if refreshed.Auth != nil {
		account = refreshed
		result = account.baseResult()
	}
	result.NextRefreshAt = account.nextRefreshAtMillis()
	var decision accountInspectionDecision
	var statusCode *int
	var err error
	switch account.Provider {
	case "antigravity":
		decision, statusCode, err = s.inspectAntigravity(ctx, account, settings)
	case "claude":
		decision, statusCode, err = s.inspectClaude(ctx, account, settings)
	case "codex":
		decision, statusCode, err = s.inspectCodex(ctx, account, settings)
	case "gemini-cli":
		decision, statusCode, err = s.inspectGeminiCLI(ctx, account, settings)
	case "kimi":
		decision, statusCode, err = s.inspectKimi(ctx, account, settings)
	case "xai":
		decision, statusCode, err = s.inspectXAI(ctx, account, settings)
	default:
		result.ActionReason = "暂不支持该 provider 巡检"
		result.Error = "unsupported provider"
		return result
	}
	if err != nil {
		result.StatusCode = statusCode
		result.Error = err.Error()
		result.ErrorCode = accountInspectionErrorCode(statusCode, "inspection_probe_error")
		result.ActionReason = "探测异常，保留账号"
		if statusCode != nil && isAccountErrorStatus(*statusCode) {
			s.syncInspectionAuthStatus(ctx, account, *statusCode)
		} else {
			s.syncInspectionAuthError(ctx, account, "inspection_probe_error", err.Error(), 0)
		}
		s.appendLog("warning", fmt.Sprintf("%s 探测异常，保留账号：%s", account.identity(), err.Error()))
		return result
	}
	result.StatusCode = statusCode
	result.Action = decision.Action
	result.ActionReason = decision.ActionReason
	result.UsedPercent = decision.UsedPercent
	result.IsQuota = decision.IsQuota
	result.Error = decision.Error
	result.ErrorDetail = decision.ErrorDetail
	result.ErrorCode = accountInspectionDecisionErrorCode(account.Provider, decision, statusCode)
	if decision.DeepProbeStatus != "" {
		result.DeepProbeTriggered = true
		result.DeepProbeStatus = string(decision.DeepProbeStatus)
		result.DeepProbeError = decision.DeepProbeError
	}
	if statusCode != nil && decision.DeepProbeStatus != accountInspectionDeepProbeTransientError {
		s.syncInspectionAuthStatus(ctx, account, *statusCode)
	}
	level := "info"
	if result.Action == accountInspectionActionDisable {
		level = "warning"
	} else if result.Action == accountInspectionActionEnable {
		level = "success"
	} else if result.Action == accountInspectionActionDelete {
		level = "error"
	}
	percent := "--"
	if result.UsedPercent != nil {
		percent = fmt.Sprintf("%.1f%%", *result.UsedPercent)
	}
	s.appendLog(level, fmt.Sprintf("%s -> %s (%s · 已用 %s)", account.identity(), result.Action, account.Provider, percent))
	return result
}

func (s *accountInspectionScheduler) refreshAccountIfDue(ctx context.Context, account accountInspectionAccount, refreshLimiter chan struct{}) (accountInspectionAccount, bool, error) {
	if account.Auth == nil || account.Auth.ID == "" || s == nil || s.h == nil || s.h.authManager == nil {
		return account, false, nil
	}
	if refreshLimiter != nil {
		select {
		case refreshLimiter <- struct{}{}:
			defer func() { <-refreshLimiter }()
		case <-ctx.Done():
			return account, false, ctx.Err()
		}
	}
	updated, refreshed, err := s.h.authManager.RefreshIfDueForInspection(ctx, account.Auth.ID)
	if err != nil {
		return account, true, err
	}
	if updated == nil {
		return account, false, nil
	}
	refreshedAccount := accountFromAuth(updated)
	if refreshed {
		s.appendLog("success", fmt.Sprintf("%s 刷新令牌成功", refreshedAccount.identity()))
	}
	return refreshedAccount, refreshed, nil
}

func (account accountInspectionAccount) nextRefreshAtMillis() int64 {
	if account.Auth == nil || account.Auth.NextRefreshAfter.IsZero() {
		return 0
	}
	return account.Auth.NextRefreshAfter.UnixMilli()
}

func (account accountInspectionAccount) baseResult() accountInspectionResult {
	return accountInspectionResult{
		Key:          account.Key,
		Provider:     account.Provider,
		FileName:     account.FileName,
		DisplayName:  account.DisplayName,
		Email:        account.Email,
		Name:         account.Name,
		AuthIndex:    account.AuthIndex,
		Disabled:     account.Disabled,
		Action:       accountInspectionActionKeep,
		ActionReason: "无需处理",
	}
}

func formatAccountInspectionIdentity(fileName string, email string, name string, displayName string) string {
	label := firstNonEmptyStringValue(email, name, displayName)
	if label != "" && label != "-" {
		if fileName != "" {
			return fmt.Sprintf("%s[%s]", label, fileName)
		}
		return label
	}
	return fileName
}

func (account accountInspectionAccount) identity() string {
	return formatAccountInspectionIdentity(account.FileName, account.Email, account.Name, account.DisplayName)
}

func (s *accountInspectionScheduler) apiCall(ctx context.Context, auth *coreauth.Auth, method string, url string, headers map[string]string, data string, timeoutMS int) (accountInspectionHTTPResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutMS <= 0 {
		timeoutMS = accountInspectionDefaultTimeoutMS
	}
	var body io.Reader
	if data != "" {
		body = bytes.NewBufferString(data)
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, body)
	if err != nil {
		return accountInspectionHTTPResult{}, err
	}
	resolvedHeaders := make(map[string]string, len(headers))
	var token string
	var tokenResolved bool
	for key, value := range headers {
		if strings.Contains(value, "$TOKEN$") {
			if !tokenResolved {
				token, err = s.h.resolveTokenForAuth(reqCtx, auth)
				tokenResolved = true
				if err != nil {
					return accountInspectionHTTPResult{}, err
				}
			}
			value = strings.ReplaceAll(value, "$TOKEN$", token)
		}
		resolvedHeaders[key] = value
	}
	for key, value := range resolvedHeaders {
		req.Header.Set(key, value)
	}
	if accountInspectionShouldUseExecutorHTTPRequest(auth) {
		if s == nil || s.h == nil || s.h.authManager == nil {
			return accountInspectionHTTPResult{}, fmt.Errorf("core auth manager unavailable")
		}
		resp, err := s.h.authManager.HttpRequest(reqCtx, auth, req)
		if err != nil {
			return accountInspectionHTTPResult{}, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		return accountInspectionHTTPResult{StatusCode: resp.StatusCode, Body: string(raw)}, nil
	}
	client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond, Transport: s.h.apiCallTransport(auth)}
	resp, err := client.Do(req)
	if err != nil {
		return accountInspectionHTTPResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return accountInspectionHTTPResult{StatusCode: resp.StatusCode, Body: string(raw)}, nil
}

func accountInspectionShouldUseExecutorHTTPRequest(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "gemini-cli", "xai":
		return true
	default:
		return false
	}
}

func (s *accountInspectionScheduler) withRetry(ctx context.Context, retries int, task func() (accountInspectionHTTPResult, error)) (accountInspectionHTTPResult, error) {
	var last accountInspectionHTTPResult
	var err error
	for i := 0; i <= retries; i++ {
		last, err = task()
		if err == nil {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		default:
		}
	}
	return last, err
}

func (s *accountInspectionScheduler) inspectAntigravity(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	projectID := antigravityProjectID(account.Auth)
	body := `{"project":"` + escapeJSONString(projectID) + `"}`
	urls := antigravityQuotaURLs()
	var priorityStatus *int
	var priorityDetail string
	for _, url := range urls {
		resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
			return s.apiCall(ctx, account.Auth, http.MethodPost, url, map[string]string{
				"Authorization": "Bearer $TOKEN$",
				"Content-Type":  "application/json",
				"User-Agent":    s.antigravityUserAgent(),
			}, body, settings.Timeout)
		})
		if err != nil {
			continue
		}
		status := intPtr(resp.StatusCode)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if isAccountErrorStatus(resp.StatusCode) {
				priorityStatus = status
				priorityDetail = resp.Body
			}
			continue
		}
		groups, err := buildAntigravityGroups(resp.Body)
		if err != nil {
			continue
		}
		quotaState := map[string]any{"groups": groups, "rawShapeHash": jsonShapeHash(resp.Body)}
		if subscription := s.fetchAntigravitySubscription(ctx, account, settings); subscription != nil {
			quotaState["subscription"] = subscription
			if plan := stringFromAny(subscription["plan"]); plan != "" {
				quotaState["plan"] = plan
				quotaState["planType"] = plan
			}
		}
		s.persistQuotaState(ctx, account, quotaSuccessState(quotaState))
		used := antigravityUsedPercent(groups, settings.AntigravityQuotaMode)
		decision := quotaDecision(account, used, used != nil, settings.UsedPercentThreshold)
		if settings.AntigravityDeepProbeEnabled && antigravityShouldDeepProbe(decision) {
			return s.applyAntigravityDeepProbe(ctx, account, settings, groups, decision, status)
		}
		return decision, status, nil
	}
	if priorityStatus != nil {
		return withInspectionHTTPErrorDetail(authErrorDecision(account, *priorityStatus), priorityDetail), priorityStatus, nil
	}
	return accountInspectionDecision{}, priorityStatus, fmt.Errorf("antigravity quota unavailable")
}

func antigravityQuotaURLs() []string {
	return []string{
		"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	}
}

func antigravityGenerateURLs() []string {
	return []string{
		"https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent",
		"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:generateContent",
		"https://cloudcode-pa.googleapis.com/v1internal:generateContent",
	}
}

func (s *accountInspectionScheduler) fetchAntigravitySubscription(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) map[string]any {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, antigravityCodeAssistURL, map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    s.antigravityUserAgent(),
		}, `{"metadata":{"ideType":"ANTIGRAVITY"}}`, settings.Timeout)
	})
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	payload, err := parseAntigravityQuotaPayload(resp.Body)
	if err != nil {
		return nil
	}
	return buildAntigravitySubscription(payload)
}

func antigravityShouldDeepProbe(decision accountInspectionDecision) bool {
	if decision.UsedPercent == nil || decision.IsQuota {
		return false
	}
	return decision.Action == accountInspectionActionKeep || decision.Action == accountInspectionActionEnable
}

func (s *accountInspectionScheduler) applyAntigravityDeepProbe(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, groups []map[string]any, decision accountInspectionDecision, quotaStatus *int) (accountInspectionDecision, *int, error) {
	model := selectAntigravityDeepProbeModel(groups, settings.AntigravityDeepProbeModel)
	projectID := antigravityProjectID(account.Auth)
	if model == "" || projectID == "" {
		decision.DeepProbeStatus = accountInspectionDeepProbeSkipped
		if model == "" {
			decision.DeepProbeError = "no available Claude/GPT model for deep probe"
		} else {
			decision.DeepProbeError = "missing Antigravity project id"
		}
		s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测跳过：%s", account.identity(), decision.DeepProbeError))
		return decision, quotaStatus, nil
	}

	s.appendLog("info", fmt.Sprintf("%s Antigravity 深度检测开始：%s", account.identity(), model))
	body := buildAntigravityDeepProbeBody(projectID, model)
	var lastStatus *int
	var lastMessage string
	var lastDetail string
	for _, url := range antigravityGenerateURLs() {
		resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
			return s.apiCall(ctx, account.Auth, http.MethodPost, url, map[string]string{
				"Authorization": "Bearer $TOKEN$",
				"Content-Type":  "application/json",
				"User-Agent":    s.antigravityUserAgent(),
			}, body, settings.Timeout)
		})
		if err != nil {
			lastMessage = err.Error()
			continue
		}
		lastStatus = intPtr(resp.StatusCode)
		probeStatus, probeMessage := classifyAntigravityDeepProbeResponse(resp)
		probeDetail := inspectionHTTPErrorDetail(resp.Body)
		switch probeStatus {
		case accountInspectionDeepProbeSuccess:
			s.clearInspectionAuthError(ctx, account)
			decision.DeepProbeStatus = accountInspectionDeepProbeSuccess
			decision.DeepProbeError = ""
			s.appendLog("success", fmt.Sprintf("%s Antigravity 深度检测通过", account.identity()))
			return decision, lastStatus, nil
		case accountInspectionDeepProbeAuthError:
			s.syncInspectionAuthStatus(ctx, account, resp.StatusCode)
			probeDecision := authErrorDecision(account, resp.StatusCode)
			probeDecision.UsedPercent = decision.UsedPercent
			probeDecision.DeepProbeStatus = accountInspectionDeepProbeAuthError
			probeDecision.DeepProbeError = probeMessage
			probeDecision.ErrorDetail = probeDetail
			s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测授权异常：%s", account.identity(), probeMessage))
			return probeDecision, lastStatus, nil
		case accountInspectionDeepProbeQuota:
			s.clearInspectionAuthError(ctx, account)
			probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "Antigravity 深度检测返回额度不可用，建议禁用账号", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: probeDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: probeMessage}
			if account.Disabled {
				probeDecision.Action = accountInspectionActionKeep
				probeDecision.ActionReason = "Antigravity 深度检测返回额度不可用，但账号已禁用"
			}
			s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测额度不可用：%s", account.identity(), probeMessage))
			return probeDecision, lastStatus, nil
		default:
			lastMessage = probeMessage
			lastDetail = probeDetail
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < http.StatusInternalServerError {
				break
			}
		}
	}
	if lastMessage == "" {
		lastMessage = "antigravity deep probe unavailable"
	}
	s.syncInspectionAuthError(ctx, account, "antigravity_deep_probe_error", lastMessage, statusValue(lastStatus))
	decision.Action = accountInspectionActionKeep
	decision.ActionReason = "Antigravity 深度检测临时异常，保留账号"
	decision.Error = lastMessage
	decision.ErrorDetail = lastDetail
	decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
	decision.DeepProbeError = lastMessage
	s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测临时异常：%s", account.identity(), lastMessage))
	return decision, firstStatus(lastStatus, quotaStatus), nil
}

func selectAntigravityDeepProbeModel(groups []map[string]any, preferredModel string) string {
	if model := strings.TrimSpace(preferredModel); model != "" {
		return model
	}
	return "claude-sonnet-4-6"
}

func buildAntigravityDeepProbeBody(projectID string, model string) string {
	raw, _ := json.Marshal(map[string]any{
		"project": projectID,
		"model":   model,
		"request": map[string]any{
			"contents": []map[string]any{{
				"role":  "user",
				"parts": []map[string]string{{"text": "ping"}},
			}},
			"generationConfig": map[string]any{"maxOutputTokens": 1},
		},
	})
	return string(raw)
}

func classifyAntigravityDeepProbeResponse(resp accountInspectionHTTPResult) (accountInspectionDeepProbeStatus, string) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if hasAntigravityGenerateContent(resp.Body) {
			return accountInspectionDeepProbeSuccess, ""
		}
		return accountInspectionDeepProbeTransientError, "Antigravity 深度检测响应为空或格式异常"
	}
	message := summarizeInspectionHTTPBody(resp.Body)
	if message == "" {
		message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if isAccountErrorStatus(resp.StatusCode) {
		return accountInspectionDeepProbeAuthError, message
	}
	if resp.StatusCode == http.StatusPaymentRequired || isAntigravityQuotaFailure(resp.Body) {
		return accountInspectionDeepProbeQuota, message
	}
	return accountInspectionDeepProbeTransientError, message
}

func hasAntigravityGenerateContent(body string) bool {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	if candidates, ok := nestedMap(payload, "response")["candidates"].([]any); ok && len(candidates) > 0 {
		return true
	}
	if candidates, ok := payload["candidates"].([]any); ok && len(candidates) > 0 {
		return true
	}
	return false
}

func isAntigravityQuotaFailure(body string) bool {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "quota_exhausted") || strings.Contains(lower, "quota exhausted") || strings.Contains(lower, "limit reached") {
		return true
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	if !strings.EqualFold(stringFromAny(nestedMap(payload, "error")["status"]), "RESOURCE_EXHAUSTED") {
		return false
	}
	details := anySlice(nestedMap(payload, "error")["details"])
	for _, raw := range details {
		detail, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(stringFromAny(detail["reason"]), "QUOTA_EXHAUSTED") {
			return true
		}
	}
	return false
}

func summarizeInspectionHTTPBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if message := nestedString(nestedMap(payload, "error"), "message", ""); message != "" {
			return message
		}
		if message := stringFromAny(payload["error"]); message != "" {
			return message
		}
		if message := stringFromAny(payload["message"]); message != "" {
			return message
		}
	}
	if len(body) > 240 {
		return body[:240]
	}
	return body
}

func inspectionHTTPErrorDetail(body string) string {
	return strings.TrimSpace(body)
}

func withInspectionHTTPErrorDetail(decision accountInspectionDecision, body string) accountInspectionDecision {
	decision.ErrorDetail = inspectionHTTPErrorDetail(body)
	return decision
}

func statusValue(status *int) int {
	if status == nil {
		return 0
	}
	return *status
}

func firstStatus(primary *int, fallback *int) *int {
	if primary != nil {
		return primary
	}
	return fallback
}

func (s *accountInspectionScheduler) inspectClaude(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	usageResp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", s.claudeHeaders(), "", settings.Timeout)
	})
	status := intPtr(usageResp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if usageResp.StatusCode < 200 || usageResp.StatusCode >= 300 {
		if isAccountErrorStatus(usageResp.StatusCode) {
			return withInspectionHTTPErrorDetail(authErrorDecision(account, usageResp.StatusCode), usageResp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", usageResp.StatusCode)
	}
	windows, extraUsage, err := buildClaudeWindows(usageResp.Body)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	planType := ""
	profileResp, profileErr := s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.anthropic.com/api/oauth/profile", s.claudeHeaders(), "", settings.Timeout)
	if profileErr == nil && profileResp.StatusCode >= 200 && profileResp.StatusCode < 300 {
		planType = resolveClaudePlan(profileResp.Body)
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"windows": windows, "extraUsage": extraUsage, "planType": emptyStringAsNil(planType), "rawShapeHash": jsonShapeHash(usageResp.Body)}))
	used := maxUsedPercentFromWindows(windows)
	return quotaDecision(account, used, len(windows) > 0, settings.UsedPercentThreshold), status, nil
}

func (s *accountInspectionScheduler) inspectCodex(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	accountID := codexAccountID(account.Auth)
	if accountID == "" {
		return accountInspectionDecision{}, nil, fmt.Errorf("missing ChatGPT account id")
	}
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", map[string]string{
			"Authorization":      "Bearer $TOKEN$",
			"Content-Type":       "application/json",
			"User-Agent":         s.codexUserAgent(),
			"Chatgpt-Account-Id": accountID,
		}, "", settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	payload, windows, used := buildCodexWindows(resp.Body)
	isQuota := resp.StatusCode == 402 || strings.Contains(strings.ToLower(resp.Body), "quota exhausted") || strings.Contains(strings.ToLower(resp.Body), "limit reached") || strings.Contains(strings.ToLower(resp.Body), "payment_required")
	if used != nil && *used >= float64(settings.UsedPercentThreshold) {
		isQuota = true
	}
	if payload != nil && len(windows) > 0 {
		s.persistQuotaState(ctx, account, quotaSuccessState(codexQuotaStateValues(account.Auth, payload, windows, resp.Body)))
	}
	decision := codexDecision(account, resp.StatusCode, used, isQuota, settings.UsedPercentThreshold)
	if isAccountErrorStatus(resp.StatusCode) {
		decision = withInspectionHTTPErrorDetail(decision, resp.Body)
	}
	return decision, status, nil
}

func (s *accountInspectionScheduler) inspectGeminiCLI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	projectID := geminiCLIProjectID(account.Auth)
	if projectID == "" {
		return accountInspectionDecision{}, nil, fmt.Errorf("missing Gemini CLI project id")
	}
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, geminiCLIQuotaURL, map[string]string{
			"Content-Type": "application/json",
		}, `{"project":"`+escapeJSONString(projectID)+`"}`, settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isAccountErrorStatus(resp.StatusCode) {
			return withInspectionHTTPErrorDetail(authErrorDecision(account, resp.StatusCode), resp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buckets, used, err := buildGeminiCLIQuotaBuckets(resp.Body)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	quotaState := map[string]any{
		"buckets":      buckets,
		"projectId":    projectID,
		"rawShapeHash": jsonShapeHash(resp.Body),
	}
	if subscription := s.fetchGeminiCLISubscription(ctx, account, projectID, settings); subscription != nil {
		quotaState["subscription"] = subscription
		if plan := stringFromAny(subscription["plan"]); plan != "" {
			quotaState["plan"] = plan
			quotaState["planType"] = plan
		}
		if tierID := stringFromAny(subscription["tierId"]); tierID != "" {
			quotaState["tierId"] = tierID
		}
		if tierLabel := stringFromAny(subscription["tierLabel"]); tierLabel != "" {
			quotaState["tierLabel"] = tierLabel
		}
		if creditBalance, ok := floatFromAny(subscription["creditBalance"]); ok {
			quotaState["creditBalance"] = creditBalance
		}
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(quotaState))
	return quotaDecision(account, used, len(buckets) > 0, settings.UsedPercentThreshold), status, nil
}

func (s *accountInspectionScheduler) fetchGeminiCLISubscription(ctx context.Context, account accountInspectionAccount, projectID string, settings accountInspectionSettings) map[string]any {
	body := map[string]any{
		"cloudaicompanionProject": projectID,
		"metadata": map[string]any{
			"ideType":     "IDE_UNSPECIFIED",
			"platform":    "PLATFORM_UNSPECIFIED",
			"pluginType":  "GEMINI",
			"duetProject": projectID,
		},
	}
	raw, _ := json.Marshal(body)
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, geminiCLICodeAssistURL, map[string]string{
			"Content-Type": "application/json",
		}, string(raw), settings.Timeout)
	})
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		return nil
	}
	return buildGeminiCLISubscription(payload)
}

func (s *accountInspectionScheduler) inspectKimi(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.kimi.com/coding/v1/usages", map[string]string{"Authorization": "Bearer $TOKEN$"}, "", settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isAccountErrorStatus(resp.StatusCode) {
			return withInspectionHTTPErrorDetail(authErrorDecision(account, resp.StatusCode), resp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	rows, used, err := buildKimiRows(resp.Body)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"rows": rows, "rawShapeHash": jsonShapeHash(resp.Body)}))
	return quotaDecision(account, used, len(rows) > 0, settings.UsedPercentThreshold), status, nil
}

func (s *accountInspectionScheduler) inspectXAI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	headers := xaiRequestHeaders(account.Auth)
	weeklyBilling, weeklyResp, weeklyErr := s.fetchXAIBillingSummary(ctx, account, settings, xaiBillingWeeklyURL(), headers)
	monthlyBilling, monthlyResp, monthlyErr := s.fetchXAIBillingSummary(ctx, account, settings, xaiBillingURL(), headers)
	status := firstInspectionStatus(monthlyResp.StatusCode, weeklyResp.StatusCode)
	billing := mergeXAIBillingSummaries(weeklyBilling, monthlyBilling)
	if billing == nil {
		if isAccountErrorStatus(weeklyResp.StatusCode) {
			return withInspectionHTTPErrorDetail(authErrorDecision(account, weeklyResp.StatusCode), weeklyResp.Body), status, nil
		}
		if isAccountErrorStatus(monthlyResp.StatusCode) {
			return withInspectionHTTPErrorDetail(authErrorDecision(account, monthlyResp.StatusCode), monthlyResp.Body), status, nil
		}
		if weeklyErr != nil {
			return accountInspectionDecision{}, status, weeklyErr
		}
		if monthlyErr != nil {
			return accountInspectionDecision{}, status, monthlyErr
		}
		return accountInspectionDecision{}, status, fmt.Errorf("empty xai billing config")
	}
	used := xaiSummaryUsedPercent(billing)
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{
		"billing":             billing,
		"rawShapeHash":        jsonShapeHashForBodies(map[string]string{"weekly": weeklyResp.Body, "monthly": monthlyResp.Body}),
		"weeklyRawShapeHash":  jsonShapeHash(weeklyResp.Body),
		"monthlyRawShapeHash": jsonShapeHash(monthlyResp.Body),
	}))
	decision := quotaDecision(account, used, billing != nil, settings.UsedPercentThreshold)
	if settings.XAIDeepProbeEnabled && accountInspectionShouldDeepProbe(decision) {
		return s.applyXAIDeepProbe(ctx, account, settings, decision, status)
	}
	return decision, status, nil
}

func accountInspectionShouldDeepProbe(decision accountInspectionDecision) bool {
	if decision.UsedPercent == nil || decision.IsQuota {
		return false
	}
	return decision.Action == accountInspectionActionKeep || decision.Action == accountInspectionActionEnable
}

func (s *accountInspectionScheduler) applyXAIDeepProbe(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, decision accountInspectionDecision, quotaStatus *int) (accountInspectionDecision, *int, error) {
	model := strings.TrimSpace(settings.XAIDeepProbeModel)
	if model == "" {
		decision.DeepProbeStatus = accountInspectionDeepProbeSkipped
		decision.DeepProbeError = "missing xAI deep probe model"
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测跳过：%s", account.identity(), decision.DeepProbeError))
		return decision, quotaStatus, nil
	}

	release, err := s.acquireXAIDeepProbe(ctx)
	if err != nil {
		return decision, quotaStatus, err
	}
	defer release()

	s.appendLog("info", fmt.Sprintf("%s xAI 深度检测开始：%s", account.identity(), model))
	resp, status, message, err := runXAIDeepProbeWithRetry(ctx, settings.Retries, accountInspectionXAIRetryDelay, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, xaiResponsesURL(account.Auth), map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"Accept":        "text/event-stream",
			"Connection":    "Keep-Alive",
		}, buildXAIDeepProbeBody(model), settings.Timeout)
	})
	var probeStatus *int
	if resp.StatusCode != 0 {
		probeStatus = intPtr(resp.StatusCode)
	}
	if err != nil {
		message := err.Error()
		s.syncInspectionAuthError(ctx, account, "xai_deep_probe_error", message, statusValue(probeStatus))
		decision.Action = accountInspectionActionKeep
		decision.ActionReason = "xAI 深度检测临时异常，保留账号"
		decision.Error = message
		decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
		decision.DeepProbeError = message
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测临时异常：%s", account.identity(), message))
		return decision, firstStatus(probeStatus, quotaStatus), nil
	}

	errorDetail := inspectionHTTPErrorDetail(resp.Body)
	switch status {
	case accountInspectionDeepProbeSuccess:
		s.clearInspectionAuthError(ctx, account)
		decision.DeepProbeStatus = accountInspectionDeepProbeSuccess
		decision.DeepProbeError = ""
		s.appendLog("success", fmt.Sprintf("%s xAI 深度检测通过", account.identity()))
		return decision, probeStatus, nil
	case accountInspectionDeepProbeAuthError:
		s.syncInspectionAuthStatus(ctx, account, resp.StatusCode)
		probeDecision := authErrorDecision(account, resp.StatusCode)
		probeDecision.UsedPercent = decision.UsedPercent
		probeDecision.DeepProbeStatus = accountInspectionDeepProbeAuthError
		probeDecision.DeepProbeError = message
		probeDecision.ErrorDetail = errorDetail
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测授权异常：%s", account.identity(), message))
		return probeDecision, probeStatus, nil
	case accountInspectionDeepProbeQuota:
		s.clearInspectionAuthError(ctx, account)
		probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "xAI 深度检测返回额度不可用，建议禁用账号", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: errorDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: message}
		if account.Disabled {
			probeDecision.Action = accountInspectionActionKeep
			probeDecision.ActionReason = "xAI 深度检测返回额度不可用，但账号已禁用"
		}
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测额度不可用：%s", account.identity(), message))
		return probeDecision, probeStatus, nil
	default:
		s.syncInspectionAuthError(ctx, account, "xai_deep_probe_error", message, resp.StatusCode)
		decision.Action = accountInspectionActionKeep
		decision.ActionReason = "xAI 深度检测临时异常，保留账号"
		decision.Error = message
		decision.ErrorDetail = errorDetail
		decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
		decision.DeepProbeError = message
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测临时异常：%s", account.identity(), message))
		return decision, probeStatus, nil
	}
}

func (s *accountInspectionScheduler) acquireXAIDeepProbe(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.xaiDeepProbeOnce.Do(func() {
		s.xaiDeepProbeGate = make(chan struct{}, 1)
	})
	select {
	case s.xaiDeepProbeGate <- struct{}{}:
		return func() { <-s.xaiDeepProbeGate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func runXAIDeepProbeWithRetry(
	ctx context.Context,
	retries int,
	retryDelay time.Duration,
	task func() (accountInspectionHTTPResult, error),
) (accountInspectionHTTPResult, accountInspectionDeepProbeStatus, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := retries + 1
	if attempts < accountInspectionXAIMinAttempts {
		attempts = accountInspectionXAIMinAttempts
	}
	if attempts > accountInspectionMaxRetries+1 {
		attempts = accountInspectionMaxRetries + 1
	}
	var last accountInspectionHTTPResult
	var lastStatus accountInspectionDeepProbeStatus
	var lastMessage string
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		last, lastErr = task()
		if lastErr == nil {
			lastStatus, lastMessage = classifyXAIDeepProbeResponse(last)
			if !shouldRetryXAIDeepProbe(lastStatus, lastMessage) {
				return last, lastStatus, lastMessage, nil
			}
		}
		if attempt+1 >= attempts {
			break
		}
		if retryDelay <= 0 {
			continue
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return last, lastStatus, lastMessage, ctx.Err()
		case <-timer.C:
		}
	}
	return last, lastStatus, lastMessage, lastErr
}

func shouldRetryXAIDeepProbe(status accountInspectionDeepProbeStatus, message string) bool {
	if status != accountInspectionDeepProbeTransientError {
		return false
	}
	return !strings.Contains(strings.ToLower(message), "content_filter")
}

func xaiResponsesURL(auth *coreauth.Auth) string {
	baseURL := ""
	if auth != nil {
		baseURL = strings.TrimSpace(auth.Attributes["base_url"])
		if baseURL == "" {
			baseURL = strings.TrimSpace(stringFromAny(auth.Metadata["base_url"]))
		}
	}
	if baseURL == "" {
		baseURL = "https://api.x.ai/v1"
	}
	return strings.TrimRight(baseURL, "/") + "/responses"
}

func buildXAIDeepProbeBody(model string) string {
	raw, _ := json.Marshal(map[string]any{
		"model":             strings.TrimSpace(model),
		"input":             "ping",
		"max_output_tokens": 1,
		"stream":            true,
	})
	return string(raw)
}

func classifyXAIDeepProbeResponse(resp accountInspectionHTTPResult) (accountInspectionDeepProbeStatus, string) {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return classifyXAIDeepProbeSuccessBody(resp.Body)
	}
	message := summarizeInspectionHTTPBody(resp.Body)
	if message == "" {
		message = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	if isAccountErrorStatus(resp.StatusCode) {
		return accountInspectionDeepProbeAuthError, message
	}
	if resp.StatusCode == http.StatusPaymentRequired || isXAIQuotaFailure(resp.Body) {
		return accountInspectionDeepProbeQuota, message
	}
	return accountInspectionDeepProbeTransientError, message
}

func classifyXAIDeepProbeSuccessBody(body string) (accountInspectionDeepProbeStatus, string) {
	lastEvent := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(line), &payload) != nil {
			continue
		}
		eventType := strings.ToLower(strings.TrimSpace(stringFromAny(payload["type"])))
		response := nestedMap(payload, "response")
		if eventType == "" {
			switch strings.ToLower(strings.TrimSpace(stringFromAny(payload["status"]))) {
			case "completed":
				eventType = "response.completed"
			case "incomplete":
				eventType = "response.incomplete"
				response = payload
			case "failed":
				eventType = "response.failed"
				response = payload
			}
		}
		if eventType != "" {
			lastEvent = eventType
		}
		switch eventType {
		case "response.completed":
			return accountInspectionDeepProbeSuccess, ""
		case "response.incomplete":
			reason := strings.ToLower(strings.TrimSpace(stringFromAny(nestedMap(response, "incomplete_details")["reason"])))
			if reason == "max_output_tokens" {
				return accountInspectionDeepProbeSuccess, ""
			}
			if reason == "" {
				reason = "unknown reason"
			}
			return accountInspectionDeepProbeTransientError, "xAI 深度检测响应未完成：" + reason
		case "response.failed", "error":
			message := nestedString(nestedMap(response, "error"), "message", "")
			if message == "" {
				message = nestedString(nestedMap(payload, "error"), "message", "")
			}
			if message == "" {
				message = "unknown response failure"
			}
			return accountInspectionDeepProbeTransientError, "xAI 深度检测响应失败：" + message
		}
	}
	if lastEvent != "" {
		return accountInspectionDeepProbeTransientError, "xAI 深度检测缺少终态事件，最后事件：" + lastEvent
	}
	return accountInspectionDeepProbeTransientError, "xAI 深度检测响应为空或格式异常"
}

func isXAIQuotaFailure(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "free-usage-exhausted") ||
		strings.Contains(lower, "quota_exhausted") ||
		strings.Contains(lower, "quota exhausted") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "included free usage")
}

func (s *accountInspectionScheduler) fetchXAIBillingSummary(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, url string, headers map[string]string) (map[string]any, accountInspectionHTTPResult, error) {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, url, headers, "", settings.Timeout)
	})
	if err != nil {
		return nil, resp, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	billing, _, err := buildXAIBillingSummary(resp.Body)
	if err != nil {
		return nil, resp, err
	}
	return billing, resp, nil
}

func xaiBillingURL() string {
	return "https://cli-chat-proxy.grok.com/v1/billing"
}

func xaiBillingWeeklyURL() string {
	return "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
}

func xaiRequestHeaders(auth *coreauth.Auth) map[string]string {
	headers := map[string]string{
		"Authorization":         "Bearer $TOKEN$",
		"x-xai-token-auth":      "xai-grok-cli",
		"x-grok-client-version": "0.2.91",
		"accept":                "*/*",
		"user-agent":            "grok-pager/0.2.91 grok-shell/0.2.91 (macos; aarch64)",
	}
	if userID := xaiUserID(auth); userID != "" {
		headers["x-userid"] = userID
	}
	return headers
}

func firstInspectionStatus(values ...int) *int {
	for _, value := range values {
		if value != 0 {
			return intPtr(value)
		}
	}
	return nil
}

func (s *accountInspectionScheduler) antigravityUserAgent() string {
	return misc.AntigravityUserAgent()
}

func (s *accountInspectionScheduler) codexUserAgent() string {
	if s != nil && s.h != nil && s.h.cfg != nil {
		if value := strings.TrimSpace(s.h.cfg.CodexHeaderDefaults.UserAgent); value != "" {
			return value
		}
	}
	return "codex_cli_rs/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9"
}

func (s *accountInspectionScheduler) claudeUserAgent() string {
	if s != nil && s.h != nil && s.h.cfg != nil {
		return strings.TrimSpace(s.h.cfg.ClaudeHeaderDefaults.UserAgent)
	}
	return ""
}

func (s *accountInspectionScheduler) claudeHeaders() map[string]string {
	headers := map[string]string{
		"Authorization":  "Bearer $TOKEN$",
		"Content-Type":   "application/json",
		"anthropic-beta": "oauth-2025-04-20",
	}
	if userAgent := s.claudeUserAgent(); userAgent != "" {
		headers["User-Agent"] = userAgent
	}
	return headers
}

func isAccountErrorStatus(status int) bool {
	return status == 400 || status == 401 || status == 403 || status == 404
}

func isInspectionAuthRecoveryStatus(status int) bool {
	return (status >= 200 && status < 300) || status == 402 || status == 429
}

func syncAuthInspectionLastError(auth *coreauth.Auth, lastError *coreauth.Error) {
	if auth == nil {
		return
	}
	auth.LastError = lastError
	if lastError == nil {
		if auth.Metadata != nil {
			delete(auth.Metadata, "last_error")
		}
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["last_error"] = map[string]any{
		"code":          lastError.Code,
		"message":       lastError.Message,
		"retryable":     lastError.Retryable,
		"http_status":   lastError.HTTPStatus,
		"source":        "account_inspection",
		"updated_at_ms": time.Now().UnixMilli(),
	}
}

func setAuthInspectionDisabledState(auth *coreauth.Auth, disabled bool) {
	if auth == nil {
		return
	}
	auth.Disabled = disabled
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	clearRoutingProtectionOwnership(auth)
	auth.Metadata["disabled"] = disabled
	if disabled {
		auth.Status = coreauth.StatusDisabled
		auth.StatusMessage = "disabled by scheduled account inspection"
	} else {
		code := authInspectionLastErrorCode(auth)
		if code != "" && !isInspectionAuthErrorCode(code) {
			auth.Status = coreauth.StatusError
			auth.Unavailable = true
			if auth.LastError != nil {
				auth.StatusMessage = strings.TrimSpace(auth.LastError.Message)
			} else if raw, ok := auth.Metadata["last_error"].(map[string]any); ok {
				auth.StatusMessage = strings.TrimSpace(stringFromAny(raw["message"]))
			}
		} else {
			auth.Status = coreauth.StatusActive
			auth.StatusMessage = ""
			auth.Unavailable = false
			if isInspectionAuthErrorCode(code) {
				syncAuthInspectionLastError(auth, nil)
			}
		}
	}
	auth.UpdatedAt = time.Now()
}

func pluginVirtualSourcePath(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	sourcePath := strings.TrimSpace(authAttribute(auth, coreauth.AttributeVirtualSource))
	if sourcePath == "" {
		sourcePath = strings.TrimSpace(authAttribute(auth, "path"))
	}
	return sourcePath
}

func sameAuthSourcePath(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func isPluginVirtualRuntimeOnlyAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(authAttribute(auth, "runtime_only")), "true")
}

func cloneAnyMapForInspection(in map[string]any) map[string]any {
	if in == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (s *accountInspectionScheduler) preferredAuthForPluginVirtualWrite(auth *coreauth.Auth) *coreauth.Auth {
	if auth == nil || !coreauth.IsPluginVirtualAuth(auth) || s == nil || s.h == nil || s.h.authManager == nil {
		return auth
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return auth
	}
	var firstVirtual *coreauth.Auth
	for _, candidate := range s.h.authManager.List() {
		if candidate == nil || !sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			continue
		}
		if !coreauth.IsPluginVirtualAuth(candidate) {
			return candidate
		}
		if firstVirtual == nil {
			firstVirtual = candidate
		}
		if !isPluginVirtualRuntimeOnlyAuth(candidate) {
			return candidate
		}
	}
	if firstVirtual != nil {
		return firstVirtual
	}
	return auth
}

func savePluginVirtualAuthToSourceFile(auth *coreauth.Auth) error {
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return fmt.Errorf("plugin virtual auth source path unavailable")
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["disabled"] = auth.Disabled
	if coreauth.IsPluginVirtualAuth(auth) {
		return savePluginVirtualManagedMetadataToSourceFile(sourcePath, auth)
	}
	type metadataSetter interface {
		SetMetadata(map[string]any)
	}
	if setter, ok := auth.Storage.(metadataSetter); ok {
		setter.SetMetadata(auth.Metadata)
	}
	if auth.Storage != nil {
		return auth.Storage.SaveTokenToFile(sourcePath)
	}
	raw, err := json.Marshal(auth.Metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(sourcePath, append(raw, '\n'), 0o600)
}

func savePluginVirtualManagedMetadataToSourceFile(sourcePath string, auth *coreauth.Auth) error {
	source, err := readPluginVirtualSourceMetadata(sourcePath)
	if err != nil {
		return err
	}
	return writePluginVirtualManagedMetadataToSourceFile(sourcePath, auth, source)
}

func readPluginVirtualSourceMetadata(sourcePath string) (map[string]any, error) {
	rawSource, err := os.ReadFile(sourcePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	source := make(map[string]any)
	if len(bytes.TrimSpace(rawSource)) > 0 {
		if err = json.Unmarshal(rawSource, &source); err != nil {
			return nil, fmt.Errorf("decode plugin virtual auth source: %w", err)
		}
		if source == nil {
			source = make(map[string]any)
		}
	}
	return source, nil
}

func writePluginVirtualManagedMetadataToSourceFile(sourcePath string, auth *coreauth.Auth, source map[string]any) error {
	if source == nil {
		source = make(map[string]any)
	}
	source["disabled"] = auth.Disabled
	if value, ok := auth.Metadata["last_error"]; ok {
		source["last_error"] = value
	} else {
		delete(source, "last_error")
	}
	delete(source, "quota_cache")
	if value, ok := auth.Metadata[routingProtectionMetadataKey]; ok {
		source[routingProtectionMetadataKey] = value
	} else {
		delete(source, routingProtectionMetadataKey)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return os.WriteFile(sourcePath, append(raw, '\n'), 0o600)
}

func (s *accountInspectionScheduler) updatePluginVirtualRuntimeAuths(ctx context.Context, sourceAuth *coreauth.Auth, mutate func(*coreauth.Auth)) {
	if s == nil || s.h == nil || s.h.authManager == nil || sourceAuth == nil || mutate == nil {
		return
	}
	sourcePath := pluginVirtualSourcePath(sourceAuth)
	if sourcePath == "" {
		mutate(sourceAuth)
		_, _ = s.h.authManager.Update(ctx, sourceAuth)
		return
	}
	for _, candidate := range s.h.authManager.List() {
		if candidate == nil || !sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			continue
		}
		mutate(candidate)
		_, _ = s.h.authManager.Update(ctx, candidate)
	}
}

func (s *accountInspectionScheduler) updateInspectionAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if s == nil || s.h == nil || s.h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	auth := s.h.authByIndex(authIndex)
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	if mutate == nil {
		return nil
	}
	if coreauth.IsPluginVirtualAuth(auth) {
		sourceAuth := s.preferredAuthForPluginVirtualWrite(auth)
		var sourceMetadata map[string]any
		if coreauth.IsPluginVirtualAuth(sourceAuth) {
			var err error
			sourceMetadata, err = readPluginVirtualSourceMetadata(pluginVirtualSourcePath(sourceAuth))
			if err != nil {
				return err
			}
		}
		mutate(sourceAuth)
		s.updatePluginVirtualRuntimeAuths(ctx, sourceAuth, mutate)
		if coreauth.IsPluginVirtualAuth(sourceAuth) {
			return writePluginVirtualManagedMetadataToSourceFile(pluginVirtualSourcePath(sourceAuth), sourceAuth, sourceMetadata)
		}
		if err := savePluginVirtualAuthToSourceFile(sourceAuth); err != nil {
			return err
		}
		return nil
	}
	mutate(auth)
	updated, err := s.h.authManager.Update(ctx, auth)
	if err != nil {
		return err
	}
	if updated == nil {
		return fmt.Errorf("auth not found")
	}
	return nil
}

func (s *accountInspectionScheduler) updateInspectionErrorAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if s == nil || s.h == nil || s.h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	auth := s.h.authByIndex(authIndex)
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	if !coreauth.IsPluginVirtualAuth(auth) {
		return s.updateInspectionAuth(ctx, authIndex, mutate)
	}
	if mutate == nil {
		return nil
	}
	mutate(auth)
	updated, err := s.h.authManager.Update(ctx, auth)
	if err != nil {
		return err
	}
	if updated == nil {
		return fmt.Errorf("auth not found")
	}
	return nil
}

func (s *accountInspectionScheduler) syncInspectionAuthError(ctx context.Context, account accountInspectionAccount, code string, message string, status int) {
	if s == nil || s.h == nil || s.h.authManager == nil || account.AuthIndex == "" {
		return
	}
	err := s.updateInspectionErrorAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		auth.Status = coreauth.StatusError
		auth.StatusMessage = message
		auth.Unavailable = true
		syncAuthInspectionLastError(auth, &coreauth.Error{Code: code, Message: message, HTTPStatus: status})
		auth.UpdatedAt = time.Now()
	})
	if err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 认证状态回写失败：%s", account.identity(), err.Error()))
	}
}

func (s *accountInspectionScheduler) clearInspectionAuthError(ctx context.Context, account accountInspectionAccount) {
	if s == nil || s.h == nil || s.h.authManager == nil || account.AuthIndex == "" {
		return
	}
	auth := s.h.authByIndex(account.AuthIndex)
	if auth == nil {
		return
	}
	if !isInspectionAuthErrorCode(authInspectionLastErrorCode(auth)) {
		return
	}
	err := s.updateInspectionErrorAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		if auth.Disabled {
			auth.Status = coreauth.StatusDisabled
		} else {
			auth.Status = coreauth.StatusActive
		}
		auth.StatusMessage = ""
		auth.Unavailable = false
		syncAuthInspectionLastError(auth, nil)
		auth.UpdatedAt = time.Now()
	})
	if err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 认证状态清理失败：%s", account.identity(), err.Error()))
	}
}

func authInspectionLastErrorCode(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.LastError != nil {
		return strings.TrimSpace(auth.LastError.Code)
	}
	raw, ok := auth.Metadata["last_error"].(map[string]any)
	if !ok {
		return ""
	}
	return stringFromAny(raw["code"])
}

func isInspectionAuthErrorCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "inspection_http_error", "inspection_probe_error", "antigravity_deep_probe_error", "xai_deep_probe_error", "token_refresh_error":
		return true
	default:
		return false
	}
}

func (s *accountInspectionScheduler) syncInspectionAuthStatus(ctx context.Context, account accountInspectionAccount, status int) {
	if isAccountErrorStatus(status) {
		message := fmt.Sprintf("HTTP %d", status)
		s.syncInspectionAuthError(ctx, account, "inspection_http_error", message, status)
		return
	}
	if isInspectionAuthRecoveryStatus(status) {
		s.clearInspectionAuthError(ctx, account)
	}
}

func authErrorDecision(account accountInspectionAccount, status int) accountInspectionDecision {
	if account.Disabled {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: fmt.Sprintf("接口返回 %d，但账号已禁用", status)}
	}
	return accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: fmt.Sprintf("接口返回 %d，建议禁用账号", status)}
}

func accountInspectionErrorCode(status *int, fallback string) string {
	if status != nil && isAccountErrorStatus(*status) {
		return "inspection_http_error"
	}
	return fallback
}

func accountInspectionDecisionErrorCode(provider string, decision accountInspectionDecision, status *int) string {
	if status != nil && isAccountErrorStatus(*status) {
		return "inspection_http_error"
	}
	switch decision.DeepProbeStatus {
	case accountInspectionDeepProbeAuthError, accountInspectionDeepProbeTransientError:
		if strings.EqualFold(strings.TrimSpace(provider), "xai") {
			return "xai_deep_probe_error"
		}
		return "antigravity_deep_probe_error"
	}
	if decision.Error != "" {
		return "inspection_probe_error"
	}
	return ""
}

func healthyDecision(account accountInspectionAccount) accountInspectionDecision {
	if account.Disabled {
		return accountInspectionDecision{Action: accountInspectionActionEnable, ActionReason: "账号恢复健康，建议重新启用"}
	}
	return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "无需处理"}
}

func quotaDecision(account accountInspectionAccount, used *float64, hasQuotaData bool, threshold int) accountInspectionDecision {
	over := used != nil && *used >= float64(threshold)
	if (over || !hasQuotaData) && account.Disabled {
		reason := "未获取到可判断额度，保留账号"
		if over {
			reason = "额度达到阈值，但账号已禁用"
		}
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: reason, UsedPercent: used, IsQuota: over}
	}
	if over {
		return accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "额度达到阈值，建议禁用账号", UsedPercent: used, IsQuota: true}
	}
	if !hasQuotaData {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "未获取到可判断额度，保留账号", UsedPercent: used}
	}
	if account.Disabled {
		return accountInspectionDecision{Action: accountInspectionActionEnable, ActionReason: "额度可用，建议重新启用账号", UsedPercent: used}
	}
	return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "额度可用，无需处理", UsedPercent: used}
}

func codexDecision(account accountInspectionAccount, status int, used *float64, isQuota bool, threshold int) accountInspectionDecision {
	if status == 401 {
		return accountInspectionDecision{Action: accountInspectionActionDelete, ActionReason: "接口返回 401，建议删除失效账号", UsedPercent: used}
	}
	if isAccountErrorStatus(status) {
		return authErrorDecision(account, status)
	}
	if isQuota || (used != nil && *used >= float64(threshold)) {
		if account.Disabled {
			return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "额度超阈值，但账号已禁用", UsedPercent: used, IsQuota: isQuota}
		}
		return accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "额度超阈值，建议禁用账号", UsedPercent: used, IsQuota: true}
	}
	if status == 200 && account.Disabled {
		return accountInspectionDecision{Action: accountInspectionActionEnable, ActionReason: "账号恢复健康，建议重新启用", UsedPercent: used}
	}
	return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "无需处理", UsedPercent: used, IsQuota: false}
}

func (item accountInspectionActionItem) toResult() accountInspectionResult {
	return accountInspectionResult{
		Key:         item.Key,
		Provider:    item.Provider,
		FileName:    item.FileName,
		DisplayName: item.DisplayName,
		Email:       item.Email,
		Name:        item.Name,
		AuthIndex:   item.AuthIndex,
		Disabled:    item.Disabled,
		Action:      item.Action,
	}
}

func accountInspectionActionItemFromResult(result accountInspectionResult, action accountInspectionAction) accountInspectionActionItem {
	return accountInspectionActionItem{
		Key:         result.Key,
		Provider:    result.Provider,
		FileName:    result.FileName,
		DisplayName: result.DisplayName,
		Email:       result.Email,
		Name:        result.Name,
		AuthIndex:   result.AuthIndex,
		Disabled:    result.Disabled,
		Action:      action,
	}
}

func (s *accountInspectionScheduler) bindActionItemToSnapshot(item accountInspectionActionItem) (accountInspectionActionItem, error) {
	if s == nil {
		return accountInspectionActionItem{}, fmt.Errorf("account inspection scheduler unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, result := range s.status.Results {
		if item.Key != "" {
			if result.Key != item.Key {
				continue
			}
		} else if result.FileName != item.FileName || result.AuthIndex != item.AuthIndex {
			continue
		}
		return accountInspectionActionItemFromResult(result, item.Action), nil
	}
	return accountInspectionActionItem{}, errAccountInspectionResultStale
}

func (s *accountInspectionScheduler) removeInspectionResultLocked(result accountInspectionResult) bool {
	for index, current := range s.status.Results {
		if !sameAccountInspectionResult(current, result) {
			continue
		}
		s.status.Summary = adjustAccountInspectionSummaryForResult(s.status.Summary, current, -1)
		s.healthCounts = adjustAccountInspectionHealthCountsForResult(s.healthCounts, current, -1)
		s.status.Results = append(s.status.Results[:index], s.status.Results[index+1:]...)
		return true
	}
	return false
}

func (s *accountInspectionScheduler) applyManualActionResultLocked(result accountInspectionResult) {
	if result.Key == "" {
		result.Key = accountInspectionKey(result.FileName, result.AuthIndex)
	}
	if result.Executed && result.Action == accountInspectionActionDelete {
		s.removeInspectionResultLocked(result)
		return
	}
	s.updateInspectionResultLocked(result, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		merged := current
		merged.Provider = result.Provider
		merged.FileName = result.FileName
		merged.DisplayName = result.DisplayName
		merged.Email = result.Email
		merged.Name = result.Name
		merged.AuthIndex = result.AuthIndex
		merged.Disabled = result.Disabled
		merged.Executed = result.Executed
		merged.ExecuteError = result.ExecuteError
		if result.Executed && (result.Action == accountInspectionActionDisable || result.Action == accountInspectionActionEnable) {
			merged.Action = accountInspectionActionKeep
			merged.ActionReason = "无需处理"
			merged.Error = ""
		}
		return merged, true
	})
}

func (s *accountInspectionScheduler) executeManualActions(ctx context.Context, items []accountInspectionActionItem) ([]accountInspectionActionOutcome, error) {
	s.mu.Lock()
	restoredSnapshot := s.status.RestoredSnapshot
	s.mu.Unlock()
	if restoredSnapshot {
		return nil, errAccountInspectionRestoredSnapshotReadOnly
	}
	boundItems := make([]accountInspectionActionItem, 0, len(items))
	for _, item := range items {
		if item.Action == accountInspectionActionKeep || item.Action == "" {
			continue
		}
		boundItem, err := s.bindActionItemToSnapshot(item)
		if err != nil {
			return nil, err
		}
		boundItems = append(boundItems, boundItem)
	}
	executableItems := dedupeExecutionActionItems(boundItems)
	outcomes := make([]accountInspectionActionOutcome, len(executableItems))
	executedResults := make([]accountInspectionResult, len(executableItems))
	runAccountInspectionWorkers(len(executableItems), accountInspectionMaxDeleteWorkers, nil, func(index int) bool {
		item := executableItems[index]
		result := item.toResult()
		action := item.Action
		outcome := accountInspectionActionOutcome{Action: action, FileName: item.FileName, DisplayName: item.DisplayName, Email: item.Email, Name: item.Name, Provider: item.Provider, AuthIndex: item.AuthIndex}
		if err := s.executeAction(ctx, result, action); err != nil {
			outcome.Error = err.Error()
			result.ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", resultIdentity(result), action, err.Error()))
		} else {
			outcome.Success = true
			result.Executed = true
			result.ExecuteError = ""
			if action == accountInspectionActionDisable {
				result.Disabled = true
			}
			if action == accountInspectionActionEnable {
				result.Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", resultIdentity(result), action))
		}
		outcomes[index] = outcome
		executedResults[index] = result
		return true
	})

	s.mu.Lock()
	for _, result := range executedResults {
		if result.FileName == "" {
			continue
		}
		s.applyManualActionResultLocked(result)
	}
	s.status.Results = sortAccountInspectionResults(s.status.Results)
	saveErr := s.saveResultSnapshotLocked()
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if saveErr != nil {
		return outcomes, fmt.Errorf("failed to save account inspection snapshot: %w", saveErr)
	}
	return outcomes, nil
}

func dedupeExecutionActionItems(items []accountInspectionActionItem) []accountInspectionActionItem {
	seen := make(map[string]struct{})
	out := make([]accountInspectionActionItem, 0, len(items))
	for _, item := range items {
		if item.Action == accountInspectionActionKeep || item.Action == "" || item.FileName == "" {
			continue
		}
		key := item.Key
		if key == "" {
			key = accountInspectionKey(item.FileName, item.AuthIndex)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if item.Key == "" {
			item.Key = key
		}
		out = append(out, item)
	}
	return out
}

func summarizeManualActionOutcomes(outcomes []accountInspectionActionOutcome) gin.H {
	success := 0
	failed := 0
	for _, outcome := range outcomes {
		if outcome.Success {
			success++
		} else {
			failed++
		}
	}
	return gin.H{"total": len(outcomes), "success": success, "failed": failed}
}

func (s *accountInspectionScheduler) applyAutomaticActions(ctx context.Context, results []accountInspectionResult, settings accountInspectionSettings) {
	workers := settings.DeleteWorkers
	if workers <= 0 {
		workers = settings.Workers
	}
	deletedFiles := make(map[string]struct{})
	var mu sync.Mutex
	runAccountInspectionWorkers(len(results), workers, nil, func(index int) bool {
		action := autoActionForResult(results[index], settings)
		if action == "" {
			s.clearAutoActionConfirmation(results[index])
			return true
		}
		confirmed, count, required := s.confirmAutoAction(results[index], action, settings.AutoExecuteConfirmations)
		if !confirmed {
			if results[index].ActionReason != "" {
				results[index].ActionReason += fmt.Sprintf("；等待连续确认 %d/%d 后自动执行", count, required)
			}
			s.appendLog("info", fmt.Sprintf("%s -> %s 等待连续确认 %d/%d", resultIdentity(results[index]), action, count, required))
			return true
		}
		if action == accountInspectionActionDelete {
			mu.Lock()
			if _, ok := deletedFiles[results[index].FileName]; ok {
				results[index].ExecuteError = "auth file already deleted in this inspection run"
				mu.Unlock()
				return true
			}
			deletedFiles[results[index].FileName] = struct{}{}
			mu.Unlock()
		}
		err := s.executeAction(ctx, results[index], action)
		mu.Lock()
		if err != nil {
			results[index].ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", resultIdentity(results[index]), action, err.Error()))
		} else {
			results[index].Executed = true
			results[index].Action = action
			s.clearAutoActionConfirmation(results[index])
			if action == accountInspectionActionDisable {
				results[index].Disabled = true
			}
			if action == accountInspectionActionEnable {
				results[index].Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", resultIdentity(results[index]), action))
		}
		mu.Unlock()
		return true
	})
}

func (s *accountInspectionScheduler) confirmAutoAction(result accountInspectionResult, action accountInspectionAction, required int) (bool, int, int) {
	if required <= 1 {
		return true, 1, 1
	}
	key := autoActionConfirmationKey(result, action)
	if key == "" {
		return true, 1, required
	}
	s.mu.Lock()
	if s.autoActionConfirmations == nil {
		s.autoActionConfirmations = make(map[string]int)
	}
	count := s.autoActionConfirmations[key] + 1
	s.autoActionConfirmations[key] = count
	s.mu.Unlock()
	return count >= required, count, required
}

func (s *accountInspectionScheduler) clearAutoActionConfirmation(result accountInspectionResult) {
	keyPrefix := result.Key
	if keyPrefix == "" {
		keyPrefix = result.FileName + ":" + result.AuthIndex
	}
	if keyPrefix == "" {
		return
	}
	s.mu.Lock()
	for key := range s.autoActionConfirmations {
		if strings.HasPrefix(key, keyPrefix+"|") {
			delete(s.autoActionConfirmations, key)
		}
	}
	s.mu.Unlock()
}

func autoActionConfirmationKey(result accountInspectionResult, action accountInspectionAction) string {
	key := result.Key
	if key == "" {
		key = result.FileName + ":" + result.AuthIndex
	}
	if key == "" || action == "" {
		return ""
	}
	category := "action"
	switch {
	case isAccountInspectionAccountInvalidResult(result):
		category = "account-invalid"
	case isAccountInspectionRequestErrorResult(result):
		category = "request-error"
	case result.IsQuota:
		category = "quota"
	case action == accountInspectionActionEnable:
		category = "recovery"
	}
	return key + "|" + string(action) + "|" + category
}

func accountInspectionAutoActionForError(result accountInspectionResult, action accountInspectionAction) accountInspectionAction {
	if action == accountInspectionActionDelete {
		return accountInspectionActionDelete
	}
	if action == accountInspectionActionDisable && !result.Disabled {
		return accountInspectionActionDisable
	}
	return ""
}

func isAccountInspectionAccountInvalidResult(result accountInspectionResult) bool {
	status := 0
	if result.StatusCode != nil {
		status = *result.StatusCode
	}
	return !result.IsQuota && isAccountErrorStatus(status)
}

func isAccountInspectionRequestErrorResult(result accountInspectionResult) bool {
	if result.IsQuota || result.Error == "" || isAccountInspectionAccountInvalidResult(result) {
		return false
	}
	return true
}

func autoActionForResult(result accountInspectionResult, settings accountInspectionSettings) accountInspectionAction {
	if isAccountInspectionAccountInvalidResult(result) {
		return accountInspectionAutoActionForError(result, settings.AutoExecuteAccountInvalidAction)
	}
	if isAccountInspectionRequestErrorResult(result) {
		return accountInspectionAutoActionForError(result, settings.AutoExecuteRequestErrorAction)
	}
	if result.Action == accountInspectionActionDisable && result.IsQuota && settings.AutoExecuteQuotaLimitDisable {
		return accountInspectionActionDisable
	}
	if result.Action == accountInspectionActionEnable && settings.AutoExecuteQuotaRecoveryEnable {
		return accountInspectionActionEnable
	}
	return ""
}

func (s *accountInspectionScheduler) executeAction(ctx context.Context, result accountInspectionResult, action accountInspectionAction) error {
	if s.h == nil || s.h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	s.actionMu.Lock()
	defer s.actionMu.Unlock()
	auth, err := s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	switch action {
	case accountInspectionActionDisable, accountInspectionActionEnable:
		return s.updateInspectionAuth(ctx, result.AuthIndex, func(auth *coreauth.Auth) {
			setAuthInspectionDisabledState(auth, action == accountInspectionActionDisable)
		})
	case accountInspectionActionDelete:
		if s.pluginVirtualSourceAuthCount(auth) > 1 {
			return errAccountInspectionSharedSourceDelete
		}
		_, _, err := s.h.deleteAuthFileByName(ctx, accountFromAuth(auth).FileName)
		return err
	default:
		return fmt.Errorf("unsupported action %s", action)
	}
}

func (s *accountInspectionScheduler) actionAuthForResult(result accountInspectionResult) (*coreauth.Auth, error) {
	if s == nil || s.h == nil || s.h.authManager == nil || strings.TrimSpace(result.AuthIndex) == "" {
		return nil, errAccountInspectionResultStale
	}
	auth := s.h.authByIndex(result.AuthIndex)
	if auth == nil {
		return nil, errAccountInspectionResultStale
	}
	account := accountFromAuth(auth)
	if result.Key != "" && result.Key != account.Key {
		return nil, errAccountInspectionResultStale
	}
	if result.FileName != "" && result.FileName != account.FileName {
		return nil, errAccountInspectionResultStale
	}
	if result.Provider != "" && !strings.EqualFold(strings.TrimSpace(result.Provider), account.Provider) {
		return nil, errAccountInspectionResultStale
	}
	return auth, nil
}

func (s *accountInspectionScheduler) pluginVirtualSourceAuthCount(auth *coreauth.Auth) int {
	if s == nil || s.h == nil || s.h.authManager == nil || auth == nil || !coreauth.IsPluginVirtualAuth(auth) {
		return 0
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return 0
	}
	count := 0
	for _, candidate := range s.h.authManager.List() {
		if candidate != nil && coreauth.IsPluginVirtualAuth(candidate) && sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			count++
		}
	}
	return count
}

func summarizeAccountInspection(totalFiles int, probeSetCount int, accounts []accountInspectionAccount, results []accountInspectionResult) accountInspectionSummary {
	summary := accountInspectionSummary{TotalFiles: totalFiles, ProbeSetCount: probeSetCount, SampledCount: len(results)}
	for _, account := range accounts {
		if account.Disabled {
			summary.DisabledCount++
		} else {
			summary.EnabledCount++
		}
	}
	for _, result := range results {
		switch result.Action {
		case accountInspectionActionDelete:
			summary.DeleteCount++
		case accountInspectionActionDisable:
			summary.DisableCount++
		case accountInspectionActionEnable:
			summary.EnableCount++
		default:
			summary.KeepCount++
		}
		if result.Error != "" {
			summary.ErrorCount++
		}
		if result.Executed {
			switch result.Action {
			case accountInspectionActionDelete:
				summary.ExecutedDeleteCount++
			case accountInspectionActionDisable:
				summary.ExecutedDisableCount++
			case accountInspectionActionEnable:
				summary.ExecutedEnableCount++
			}
		}
	}
	return summary
}

func sortAccountInspectionResults(results []accountInspectionResult) []accountInspectionResult {
	sorted := append([]accountInspectionResult(nil), results...)
	sort.Slice(sorted, func(i, j int) bool {
		left := sorted[i]
		right := sorted[j]
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return resultIdentity(left) < resultIdentity(right)
	})
	return sorted
}

func adjustAccountInspectionSummaryForResult(summary accountInspectionSummary, result accountInspectionResult, delta int) accountInspectionSummary {
	summary.SampledCount += delta
	switch result.Action {
	case accountInspectionActionDelete:
		summary.DeleteCount += delta
	case accountInspectionActionDisable:
		summary.DisableCount += delta
	case accountInspectionActionEnable:
		summary.EnableCount += delta
	default:
		summary.KeepCount += delta
	}
	if result.Error != "" {
		summary.ErrorCount += delta
	}
	if result.Executed {
		switch result.Action {
		case accountInspectionActionDelete:
			summary.ExecutedDeleteCount += delta
		case accountInspectionActionDisable:
			summary.ExecutedDisableCount += delta
		case accountInspectionActionEnable:
			summary.ExecutedEnableCount += delta
		}
	}
	return summary
}

func resultIdentity(result accountInspectionResult) string {
	return formatAccountInspectionIdentity(result.FileName, result.Email, result.Name, result.DisplayName)
}

func quotaSuccessState(values map[string]any) map[string]any {
	state := map[string]any{"status": "success", "schemaVersion": 2, "parserVersion": accountInspectionQuotaParserVersion, "cachedAt": time.Now().UnixMilli()}
	for key, value := range values {
		state[key] = value
	}
	return state
}

func jsonShapeHash(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	shape, err := json.Marshal(jsonShape(payload))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(shape)
	return hex.EncodeToString(sum[:])
}

func jsonShapeHashForBodies(bodies map[string]string) string {
	shape := make(map[string]any, len(bodies))
	for key, body := range bodies {
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			continue
		}
		shape[key] = jsonShape(payload)
	}
	if len(shape) == 0 {
		return ""
	}
	encoded, err := json.Marshal(shape)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func jsonShape(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(keys))
		for _, key := range keys {
			out[key] = jsonShape(typed[key])
		}
		return out
	case []any:
		if len(typed) == 0 {
			return []any{}
		}
		return []any{jsonShape(typed[0])}
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func (s *accountInspectionScheduler) persistQuotaState(ctx context.Context, account accountInspectionAccount, state map[string]any) {
	if err := persistQuotaState(ctx, account, state); err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 配额缓存写入失败：%s", account.identity(), err.Error()))
		return
	}
	if err := s.cleanupLegacyQuotaCacheFromAuth(ctx, account); err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 旧认证文件配额缓存清理失败：%s", account.identity(), err.Error()))
	}
}

func (s *accountInspectionScheduler) cleanupLegacyQuotaCacheFromAuth(ctx context.Context, account accountInspectionAccount) error {
	if s == nil || s.h == nil || s.h.authManager == nil || account.AuthIndex == "" {
		return nil
	}
	auth := s.h.authByIndex(account.AuthIndex)
	if auth == nil || auth.Metadata == nil {
		return nil
	}
	if _, exists := auth.Metadata["quota_cache"]; !exists {
		return nil
	}
	return s.updateInspectionAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		if auth.Metadata == nil {
			return
		}
		delete(auth.Metadata, "quota_cache")
		auth.UpdatedAt = time.Now()
	})
}

func (s *accountInspectionScheduler) cleanupLegacyQuotaCaches(ctx context.Context) {
	if s == nil || s.h == nil || s.h.authManager == nil {
		return
	}
	for _, auth := range s.h.authManager.List() {
		if auth == nil || auth.Metadata == nil {
			continue
		}
		if _, exists := auth.Metadata["quota_cache"]; !exists {
			continue
		}
		account := accountFromAuth(auth)
		if err := s.cleanupLegacyQuotaCacheFromAuth(ctx, account); err != nil {
			s.appendLog("warning", fmt.Sprintf("%s 启动清理旧认证文件配额缓存失败：%s", account.identity(), err.Error()))
		}
	}
}

func persistQuotaState(ctx context.Context, account accountInspectionAccount, state map[string]any) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	observedAt := now
	if cachedAt, ok := intFromAny(state["cachedAt"]); ok && cachedAt > 0 {
		observedAt = int64(cachedAt)
	}
	version := 1
	if schemaVersion, ok := intFromAny(state["schemaVersion"]); ok && schemaVersion > 0 {
		version = schemaVersion
	}
	fingerprintSource := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(account.Provider)),
		strings.ToLower(strings.TrimSpace(account.FileName)),
		strings.ToLower(strings.TrimSpace(account.Email)),
		strings.ToLower(strings.TrimSpace(account.Name)),
	}, "|")
	fingerprint := sha256.Sum256([]byte(fingerprintSource))
	return embeddedusage.SetQuotaCache(ctx, embeddedusage.QuotaCacheEntry{
		ID:                  account.Provider + ":" + account.FileName,
		Provider:            account.Provider,
		FileName:            account.FileName,
		AuthIndex:           account.AuthIndex,
		IdentityFingerprint: hex.EncodeToString(fingerprint[:]),
		Data:                raw,
		CachedAt:            observedAt,
		ObservedAt:          observedAt,
		AccessedAt:          now,
		Version:             version,
	})
}

func buildAntigravityGroups(body string) ([]map[string]any, error) {
	payload, err := parseAntigravityQuotaPayload(body)
	if err != nil {
		return nil, err
	}
	if groups := buildAntigravitySummaryGroups(payload); len(groups) > 0 {
		return groups, nil
	}
	return nil, fmt.Errorf("empty antigravity quota groups")
}

var antigravityPlanByTierID = map[string]string{
	"free-tier":          "free",
	"g1-pro-tier":        "pro",
	"g1-ultra-tier":      "ultra",
	"g1-ultra-lite-tier": "ultra-lite",
}

func buildAntigravitySubscription(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	rawCurrentTier := firstAny(payload, "currentTier", "current_tier")
	rawPaidTier := firstAny(payload, "paidTier", "paid_tier")
	currentTier := normalizeAntigravityTier(rawCurrentTier)
	paidTier := normalizeAntigravityTier(rawPaidTier)
	effectiveTier := currentTier
	source := "current"
	if stringFromAny(paidTier["id"]) != "" {
		effectiveTier = paidTier
		source = "paid"
	}
	tierID := stringFromAny(effectiveTier["id"])
	tierName := stringFromAny(effectiveTier["name"])
	if tierID == "" && tierName == "" {
		return nil
	}
	plan := antigravityPlanByTierID[tierID]
	if plan == "" {
		plan = "unknown"
	}
	subscription := map[string]any{
		"plan":     plan,
		"tierId":   emptyStringAsNil(tierID),
		"tierName": emptyStringAsNil(tierName),
		"source":   source,
	}
	if currentTier != nil {
		subscription["currentTier"] = currentTier
	}
	if paidTier != nil {
		subscription["paidTier"] = paidTier
		if paidTierPayload, ok := rawPaidTier.(map[string]any); ok {
			credits := normalizeAntigravityCredits(firstAny(paidTierPayload, "availableCredits", "available_credits"))
			if len(credits) > 0 {
				subscription["availableCredits"] = credits
			}
		}
	}
	if _, ok := subscription["availableCredits"]; !ok {
		if currentTierPayload, ok := rawCurrentTier.(map[string]any); ok {
			credits := normalizeAntigravityCredits(firstAny(currentTierPayload, "availableCredits", "available_credits"))
			if len(credits) > 0 {
				subscription["availableCredits"] = credits
			}
		}
	}
	return subscription
}

func normalizeAntigravityTier(value any) map[string]any {
	tier, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	id := stringFromAny(tier["id"])
	name := stringFromAny(tier["name"])
	if id == "" && name == "" {
		return nil
	}
	return map[string]any{
		"id":   emptyStringAsNil(id),
		"name": emptyStringAsNil(name),
	}
}

func normalizeAntigravityCredits(value any) []map[string]any {
	items := anySlice(value)
	if len(items) == 0 {
		return nil
	}
	credits := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		credit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		creditType := stringFromAny(firstAny(credit, "creditType", "credit_type"))
		creditAmount := normalizeAntigravityCreditValue(firstAny(credit, "creditAmount", "credit_amount"))
		minimum := normalizeAntigravityCreditValue(firstAny(credit, "minimumCreditAmountForUsage", "minimum_credit_amount_for_usage"))
		if creditType == "" && creditAmount == nil {
			continue
		}
		credits = append(credits, map[string]any{
			"creditType":                  emptyStringAsNil(creditType),
			"creditAmount":                creditAmount,
			"minimumCreditAmountForUsage": minimum,
		})
	}
	return credits
}

func normalizeAntigravityCreditValue(value any) any {
	if number, ok := floatFromAny(value); ok {
		return number
	}
	if text := stringFromAny(value); text != "" {
		return text
	}
	return nil
}

func parseAntigravityQuotaPayload(body string) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, err
	}
	if len(anySlice(payload["groups"])) > 0 {
		return payload, nil
	}
	bodyValue, ok := payload["body"]
	if !ok {
		return payload, nil
	}
	switch value := bodyValue.(type) {
	case string:
		var nested map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &nested); err != nil {
			return payload, nil
		}
		return nested, nil
	case map[string]any:
		return value, nil
	default:
		return payload, nil
	}
}

func buildAntigravitySummaryGroups(payload map[string]any) []map[string]any {
	rawGroups := anySlice(payload["groups"])
	if len(rawGroups) == 0 {
		return nil
	}
	groups := make([]map[string]any, 0, len(rawGroups))
	for groupIndex, rawGroup := range rawGroups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		label := firstNonEmptyStringValue(stringFromAny(firstAny(group, "displayName", "display_name")), fmt.Sprintf("Quota Group %d", groupIndex+1))
		description := firstNonEmptyStringValue(stringFromAny(group["description"]))
		groupID := canonicalAntigravityGroupID(label, description)
		if groupID == "" {
			groupID = fmt.Sprintf("quota-group-%d", groupIndex+1)
		}
		rawBuckets := anySlice(group["buckets"])
		buckets := make([]map[string]any, 0, len(rawBuckets))
		for bucketIndex, rawBucket := range rawBuckets {
			bucket, ok := rawBucket.(map[string]any)
			if !ok {
				continue
			}
			remaining, ok := floatFromAny(firstAny(bucket, "remainingFraction", "remaining_fraction"))
			if !ok {
				continue
			}
			window := firstNonEmptyStringValue(stringFromAny(bucket["window"]))
			fallbackID := fmt.Sprintf("%s-bucket-%d", groupID, bucketIndex+1)
			if window != "" {
				fallbackID = groupID + "-" + normalizeWindowID(window)
			}
			bucketID := firstNonEmptyStringValue(stringFromAny(firstAny(bucket, "bucketId", "bucket_id")), fallbackID)
			bucketLabel := firstNonEmptyStringValue(stringFromAny(firstAny(bucket, "displayName", "display_name")), bucketID)
			parsed := map[string]any{"id": bucketID, "label": bucketLabel, "remainingFraction": normalizeFraction(remaining)}
			if window != "" {
				parsed["window"] = window
			}
			if resetTime := firstNonEmptyStringValue(stringFromAny(firstAny(bucket, "resetTime", "reset_time"))); resetTime != "" {
				parsed["resetTime"] = resetTime
			}
			if description := firstNonEmptyStringValue(stringFromAny(bucket["description"])); description != "" {
				parsed["description"] = description
			}
			buckets = append(buckets, parsed)
		}
		if len(buckets) == 0 {
			continue
		}
		sort.SliceStable(buckets, func(i, j int) bool {
			leftOrder := antigravityBucketWindowOrder(stringFromAny(buckets[i]["window"]))
			rightOrder := antigravityBucketWindowOrder(stringFromAny(buckets[j]["window"]))
			if leftOrder != rightOrder {
				return leftOrder < rightOrder
			}
			return stringFromAny(buckets[i]["label"]) < stringFromAny(buckets[j]["label"])
		})
		parsedGroup := map[string]any{"id": groupID, "label": label, "buckets": buckets}
		if description != "" {
			parsedGroup["description"] = description
		}
		groups = append(groups, parsedGroup)
	}
	return groups
}

func canonicalAntigravityGroupID(label string, description string) string {
	normalizedLabel := normalizeWindowID(label)
	normalizedDescription := normalizeWindowID(description)
	combined := normalizedLabel + "-" + normalizedDescription
	switch {
	case strings.Contains(combined, "claude") && (strings.Contains(combined, "gpt") || strings.Contains(combined, "gpt-oss") || strings.Contains(combined, "openai")):
		return "claude-gpt"
	case strings.Contains(combined, "gemini"):
		return "gemini"
	default:
		return normalizedLabel
	}
}

func antigravityBucketWindowOrder(window string) int {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "weekly", "week":
		return 0
	case "5h", "five-hour", "five_hour":
		return 1
	default:
		return math.MaxInt
	}
}

func minRemainingFractionFromBuckets(buckets []map[string]any) *float64 {
	values := make([]float64, 0, len(buckets))
	for _, bucket := range buckets {
		if remaining, ok := floatFromAny(bucket["remainingFraction"]); ok {
			values = append(values, normalizeFraction(remaining))
		}
	}
	if len(values) == 0 {
		return nil
	}
	minValue := values[0]
	for _, value := range values[1:] {
		if value < minValue {
			minValue = value
		}
	}
	return &minValue
}

func earliestResetTimeFromBuckets(buckets []map[string]any) string {
	selected := ""
	var selectedTime time.Time
	for _, bucket := range buckets {
		raw := stringFromAny(bucket["resetTime"])
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			if selected == "" {
				selected = raw
			}
			continue
		}
		if selected == "" || selectedTime.IsZero() || parsed.Before(selectedTime) {
			selected = raw
			selectedTime = parsed
		}
	}
	return selected
}

func anyMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func antigravityUsedPercent(groups []map[string]any, mode accountInspectionAntigravityQuotaMode) *float64 {
	if mode == accountInspectionAntigravityQuotaModeMaxUsed {
		return antigravityMaxUsedPercent(groups)
	}
	if used := antigravityClaudeGptUsedPercent(groups); used != nil {
		return used
	}
	return antigravityMaxUsedPercent(groups)
}

func antigravityMaxUsedPercent(groups []map[string]any) *float64 {
	values := make([]float64, 0, len(groups))
	for _, group := range groups {
		if used := antigravityGroupUsedPercent(group); used != nil {
			values = append(values, *used)
		}
	}
	return maxFloatPtr(values)
}

func antigravityGroupUsedPercent(group map[string]any) *float64 {
	remaining, ok := antigravityGroupRemainingFraction(group)
	if !ok {
		return nil
	}
	used := math.Max(0, math.Min(100, (1-normalizeFraction(remaining))*100))
	return &used
}

func antigravityGroupRemainingFraction(group map[string]any) (float64, bool) {
	if remaining := minRemainingFractionFromBuckets(anyMapSlice(group["buckets"])); remaining != nil {
		return *remaining, true
	}
	return 0, false
}

func antigravityClaudeGptUsedPercent(groups []map[string]any) *float64 {
	for _, group := range groups {
		if !isAntigravityClaudeGptGroup(group) {
			continue
		}
		return antigravityGroupUsedPercent(group)
	}
	return nil
}

func isAntigravityClaudeGptGroup(group map[string]any) bool {
	id := normalizeWindowID(stringFromAny(group["id"]))
	label := normalizeWindowID(stringFromAny(group["label"]))
	if id == "claude-gpt" || label == "claude-gpt" {
		return true
	}
	combined := id + "-" + label
	return strings.Contains(combined, "claude") && (strings.Contains(combined, "gpt") || strings.Contains(combined, "openai"))
}

func buildClaudeWindows(body string) ([]map[string]any, any, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	defs := []struct{ Key, ID, LabelKey string }{
		{"five_hour", "five-hour", "claude_quota.five_hour"},
		{"seven_day", "seven-day", "claude_quota.seven_day"},
		{"seven_day_oauth_apps", "seven-day-oauth-apps", "claude_quota.seven_day_oauth_apps"},
		{"seven_day_opus", "seven-day-opus", "claude_quota.seven_day_opus"},
		{"seven_day_sonnet", "seven-day-sonnet", "claude_quota.seven_day_sonnet"},
		{"seven_day_cowork", "seven-day-cowork", "claude_quota.seven_day_cowork"},
		{"iguana_necktie", "iguana-necktie", "claude_quota.iguana_necktie"},
	}
	windows := make([]map[string]any, 0)
	for _, def := range defs {
		window, ok := payload[def.Key].(map[string]any)
		if !ok {
			continue
		}
		used, ok := floatFromAny(window["utilization"])
		if !ok {
			continue
		}
		windows = append(windows, map[string]any{"id": def.ID, "label": def.LabelKey, "labelKey": def.LabelKey, "usedPercent": used, "resetLabel": stringFromAny(window["resets_at"])})
	}
	return windows, payload["extra_usage"], nil
}

func resolveClaudePlan(body string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return ""
	}
	if account, ok := payload["account"].(map[string]any); ok {
		hasMax, hasMaxOK := boolValue(account["has_claude_max"])
		if hasMax {
			return "plan_max"
		}
		hasPro, hasProOK := boolValue(account["has_claude_pro"])
		if hasPro {
			return "plan_pro"
		}
		if hasMaxOK && hasProOK && !hasMax && !hasPro {
			return "plan_free"
		}
	}
	if org, ok := payload["organization"].(map[string]any); ok {
		if strings.EqualFold(stringFromAny(org["organization_type"]), "claude_team") && strings.EqualFold(stringFromAny(org["subscription_status"]), "active") {
			return "plan_team"
		}
	}
	return ""
}

func buildCodexWindows(body string) (map[string]any, []map[string]any, *float64) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, nil
	}
	windows := make([]map[string]any, 0)
	rateLimit, _ := firstAny(payload, "rate_limit", "rateLimit").(map[string]any)
	codeReviewLimit, _ := firstAny(payload, "code_review_rate_limit", "codeReviewRateLimit").(map[string]any)

	addCodexWindow := func(id string, labelKey string, labelParams map[string]any, window map[string]any, limitReached any, allowed any) {
		if window == nil {
			return
		}
		used, hasUsed := floatFromAny(firstAny(window, "used_percent", "usedPercent"))
		if !hasUsed {
			if (boolFromAny(limitReached) || allowed == false) && codexResetLabel(window) != "-" {
				used = 100
				hasUsed = true
			}
		}
		var usedValue any
		if hasUsed {
			usedValue = used
		} else {
			usedValue = nil
		}
		item := map[string]any{"id": id, "label": labelKey, "labelKey": labelKey, "usedPercent": usedValue, "resetLabel": codexResetLabel(window)}
		if labelParams != nil {
			item["labelParams"] = labelParams
		}
		windows = append(windows, item)
	}

	fiveHour, weekly := codexClassifiedWindows(rateLimit, true)
	addCodexWindow("five-hour", "codex_quota.primary_window", nil, fiveHour, firstAny(rateLimit, "limit_reached", "limitReached"), rateLimit["allowed"])
	secondaryID, secondaryLabel := codexSecondaryWindowMeta(weekly, "weekly", "codex_quota.secondary_window", "monthly", "codex_quota.team_secondary_window")
	addCodexWindow(secondaryID, secondaryLabel, nil, weekly, firstAny(rateLimit, "limit_reached", "limitReached"), rateLimit["allowed"])

	codeReviewFiveHour, codeReviewWeekly := codexClassifiedWindows(codeReviewLimit, true)
	addCodexWindow("code-review-five-hour", "codex_quota.code_review_primary_window", nil, codeReviewFiveHour, firstAny(codeReviewLimit, "limit_reached", "limitReached"), codeReviewLimit["allowed"])
	codeReviewSecondaryID, codeReviewSecondaryLabel := codexSecondaryWindowMeta(codeReviewWeekly, "code-review-weekly", "codex_quota.code_review_secondary_window", "code-review-monthly", "codex_quota.code_review_team_secondary_window")
	addCodexWindow(codeReviewSecondaryID, codeReviewSecondaryLabel, nil, codeReviewWeekly, firstAny(codeReviewLimit, "limit_reached", "limitReached"), codeReviewLimit["allowed"])

	for index, raw := range anySlice(firstAny(payload, "additional_rate_limits", "additionalRateLimits")) {
		limitItem, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rateInfo, _ := firstAny(limitItem, "rate_limit", "rateLimit").(map[string]any)
		if rateInfo == nil {
			continue
		}
		limitName := firstNonEmptyStringValue(stringFromAny(firstAny(limitItem, "limit_name", "limitName")), stringFromAny(firstAny(limitItem, "metered_feature", "meteredFeature")), fmt.Sprintf("additional-%d", index+1))
		idPrefix := normalizeWindowID(limitName)
		if idPrefix == "" {
			idPrefix = fmt.Sprintf("additional-%d", index+1)
		}
		primary, _ := firstAny(rateInfo, "primary_window", "primaryWindow").(map[string]any)
		secondary, _ := firstAny(rateInfo, "secondary_window", "secondaryWindow").(map[string]any)
		params := map[string]any{"name": limitName}
		addCodexWindow(fmt.Sprintf("%s-five-hour-%d", idPrefix, index), "codex_quota.additional_primary_window", params, primary, firstAny(rateInfo, "limit_reached", "limitReached"), rateInfo["allowed"])
		additionalSecondaryID, additionalSecondaryLabel := codexSecondaryWindowMeta(secondary, "weekly", "codex_quota.additional_secondary_window", "monthly", "codex_quota.additional_team_secondary_window")
		addCodexWindow(fmt.Sprintf("%s-%s-%d", idPrefix, additionalSecondaryID, index), additionalSecondaryLabel, params, secondary, firstAny(rateInfo, "limit_reached", "limitReached"), rateInfo["allowed"])
	}

	used := maxUsedPercentFromWindows(windows)
	return payload, windows, used
}

func codexClassifiedWindows(limitInfo map[string]any, allowOrderFallback bool) (map[string]any, map[string]any) {
	if limitInfo == nil {
		return nil, nil
	}
	primary, _ := firstAny(limitInfo, "primary_window", "primaryWindow").(map[string]any)
	secondary, _ := firstAny(limitInfo, "secondary_window", "secondaryWindow").(map[string]any)
	var fiveHour map[string]any
	var weekly map[string]any
	for _, window := range []map[string]any{primary, secondary} {
		seconds, ok := floatFromAny(firstAny(window, "limit_window_seconds", "limitWindowSeconds"))
		if !ok {
			continue
		}
		if int(seconds) == 18000 && fiveHour == nil {
			fiveHour = window
		} else if (int(seconds) == 604800 || isCodexMonthlyWindow(window)) && weekly == nil {
			weekly = window
		}
	}
	if allowOrderFallback {
		if fiveHour == nil {
			fiveHour = primary
		}
		if weekly == nil {
			weekly = secondary
		}
	}
	return fiveHour, weekly
}

func isCodexMonthlyWindow(window map[string]any) bool {
	if window == nil {
		return false
	}
	seconds, ok := floatFromAny(firstAny(window, "limit_window_seconds", "limitWindowSeconds"))
	if !ok {
		return false
	}
	return seconds >= 28*24*60*60 && seconds <= 31*24*60*60
}

func codexSecondaryWindowMeta(window map[string]any, weeklyID string, weeklyLabelKey string, monthlyID string, monthlyLabelKey string) (string, string) {
	if isCodexMonthlyWindow(window) {
		return monthlyID, monthlyLabelKey
	}
	return weeklyID, weeklyLabelKey
}

func codexResetLabel(window map[string]any) string {
	if window == nil {
		return "-"
	}
	if resetAt, ok := floatFromAny(firstAny(window, "reset_at", "resetAt")); ok && resetAt > 0 {
		return formatUnixSeconds(int64(resetAt))
	}
	if resetAfter, ok := floatFromAny(firstAny(window, "reset_after_seconds", "resetAfterSeconds")); ok && resetAfter > 0 {
		return formatUnixSeconds(time.Now().Unix() + int64(resetAfter))
	}
	return "-"
}

func geminiCLIProjectID(auth *coreauth.Auth) string {
	for _, key := range []string{"project_id", "projectId", "gemini_virtual_project"} {
		if value := firstNonEmptyAuthValue(auth, key); value != "" {
			if strings.Contains(value, ",") {
				parts := strings.Split(value, ",")
				for _, part := range parts {
					if trimmed := strings.TrimSpace(part); trimmed != "" {
						return trimmed
					}
				}
				continue
			}
			return value
		}
	}
	return ""
}

func buildGeminiCLIQuotaBuckets(body string) ([]map[string]any, *float64, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	rawBuckets := anySlice(payload["buckets"])
	if len(rawBuckets) == 0 {
		return nil, nil, fmt.Errorf("empty Gemini CLI quota buckets")
	}
	type geminiBucketGroup struct {
		ID              string
		Label           string
		TokenType       string
		ModelIDs        []string
		Remaining       *float64
		RemainingAmount *float64
		ResetTime       string
	}
	groups := make(map[string]*geminiBucketGroup)
	for _, rawBucket := range rawBuckets {
		bucket, ok := rawBucket.(map[string]any)
		if !ok {
			continue
		}
		modelID := normalizeGeminiCLIModelID(stringFromAny(firstAny(bucket, "modelId", "model_id")))
		if modelID == "" || isIgnoredGeminiCLIModel(modelID) {
			continue
		}
		tokenType := stringFromAny(firstAny(bucket, "tokenType", "token_type"))
		groupID, label := geminiCLIQuotaGroupMeta(modelID)
		mapKey := groupID + "::" + tokenType
		group := groups[mapKey]
		if group == nil {
			group = &geminiBucketGroup{ID: groupID, Label: label, TokenType: tokenType}
			groups[mapKey] = group
		}
		group.ModelIDs = append(group.ModelIDs, modelID)
		remaining := geminiCLIRemainingFraction(bucket)
		var remainingAmount *float64
		if value, ok := floatFromAny(firstAny(bucket, "remainingAmount", "remaining_amount")); ok {
			remainingAmount = &value
		}
		resetTime := stringFromAny(firstAny(bucket, "resetTime", "reset_time"))
		group.Remaining = minFloatPtr(group.Remaining, remaining)
		group.RemainingAmount = minFloatPtr(group.RemainingAmount, remainingAmount)
		group.ResetTime = pickEarlierResetTime(group.ResetTime, resetTime)
	}
	if len(groups) == 0 {
		return nil, nil, fmt.Errorf("empty Gemini CLI quota buckets")
	}
	out := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		item := map[string]any{
			"id":                geminiCLIQuotaBucketID(group.ID, group.TokenType),
			"label":             group.Label,
			"remainingFraction": floatPtrAny(group.Remaining),
			"remainingAmount":   floatPtrAny(group.RemainingAmount),
			"resetTime":         emptyStringAsNil(group.ResetTime),
			"tokenType":         emptyStringAsNil(group.TokenType),
			"modelIds":          uniqueStrings(group.ModelIDs),
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		leftOrder := geminiCLIQuotaGroupOrder(stringFromAny(out[i]["id"]))
		rightOrder := geminiCLIQuotaGroupOrder(stringFromAny(out[j]["id"]))
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		return stringFromAny(out[i]["id"]) < stringFromAny(out[j]["id"])
	})
	usedValues := make([]float64, 0, len(out))
	for _, bucket := range out {
		if remaining, ok := floatFromAny(bucket["remainingFraction"]); ok {
			usedValues = append(usedValues, math.Max(0, math.Min(100, (1-normalizeFraction(remaining))*100)))
		}
	}
	return out, maxFloatPtr(usedValues), nil
}

func normalizeGeminiCLIModelID(modelID string) string {
	modelID = strings.TrimSpace(modelID)
	return strings.TrimSuffix(modelID, "_vertex")
}

func isIgnoredGeminiCLIModel(modelID string) bool {
	return modelID == "gemini-2.0-flash" || strings.HasPrefix(modelID, "gemini-2.0-flash-")
}

func geminiCLIQuotaGroupMeta(modelID string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	if strings.HasPrefix(normalized, "gemini-") {
		switch {
		case strings.Contains(normalized, "-flash-lite"):
			return "gemini-flash-lite-series", "Gemini Flash Lite Series"
		case strings.Contains(normalized, "-flash"):
			return "gemini-flash-series", "Gemini Flash Series"
		case strings.Contains(normalized, "-pro"):
			return "gemini-pro-series", "Gemini Pro Series"
		}
	}
	return modelID, modelID
}

func geminiCLIQuotaBucketID(groupID string, tokenType string) string {
	if strings.TrimSpace(tokenType) == "" {
		return groupID
	}
	return groupID + "-" + tokenType
}

func geminiCLIQuotaGroupOrder(id string) int {
	normalized := normalizeWindowID(id)
	switch {
	case strings.HasPrefix(normalized, "gemini-flash-lite-series"):
		return 0
	case strings.HasPrefix(normalized, "gemini-flash-series"):
		return 1
	case strings.HasPrefix(normalized, "gemini-pro-series"):
		return 2
	default:
		return math.MaxInt
	}
}

func geminiCLIRemainingFraction(bucket map[string]any) *float64 {
	if remaining, ok := floatFromAny(firstAny(bucket, "remainingFraction", "remaining_fraction")); ok {
		normalized := normalizeFraction(remaining)
		return &normalized
	}
	if remainingAmount, ok := floatFromAny(firstAny(bucket, "remainingAmount", "remaining_amount")); ok && remainingAmount <= 0 {
		zero := 0.0
		return &zero
	}
	if resetTime := stringFromAny(firstAny(bucket, "resetTime", "reset_time")); resetTime != "" {
		zero := 0.0
		return &zero
	}
	return nil
}

func buildGeminiCLISubscription(payload map[string]any) map[string]any {
	if payload == nil {
		return nil
	}
	currentTier := firstMap(payload, "currentTier")
	if currentTier == nil {
		currentTier = firstMap(payload, "current_tier")
	}
	paidTier := firstMap(payload, "paidTier")
	if paidTier == nil {
		paidTier = firstMap(payload, "paid_tier")
	}
	tier := currentTier
	source := "current"
	if stringFromAny(paidTier["id"]) != "" {
		tier = paidTier
		source = "paid"
	}
	tierID := stringFromAny(tier["id"])
	tierName := stringFromAny(tier["name"])
	if tierID == "" && tierName == "" {
		return nil
	}
	subscription := map[string]any{
		"plan":      geminiCLIPlanFromTierID(tierID),
		"tierId":    emptyStringAsNil(tierID),
		"tierLabel": emptyStringAsNil(geminiCLITierLabel(tierID, tierName)),
		"tierName":  emptyStringAsNil(tierName),
		"source":    source,
	}
	if balance, ok := geminiCLICreditBalance(tier); ok {
		subscription["creditBalance"] = balance
	}
	return subscription
}

func geminiCLIPlanFromTierID(tierID string) string {
	switch strings.ToLower(strings.TrimSpace(tierID)) {
	case "free-tier":
		return "free"
	case "legacy-tier":
		return "legacy"
	case "standard-tier":
		return "standard"
	case "g1-pro-tier":
		return "pro"
	case "g1-ultra-tier":
		return "ultra"
	default:
		return "unknown"
	}
}

func geminiCLITierLabel(tierID string, tierName string) string {
	if label := strings.TrimSpace(tierName); label != "" {
		return label
	}
	switch strings.ToLower(strings.TrimSpace(tierID)) {
	case "free-tier":
		return "Free"
	case "legacy-tier":
		return "Legacy"
	case "standard-tier":
		return "Standard"
	case "g1-pro-tier":
		return "Google One AI Pro"
	case "g1-ultra-tier":
		return "Google One AI Ultra"
	default:
		return strings.TrimSpace(tierID)
	}
}

func geminiCLICreditBalance(tier map[string]any) (float64, bool) {
	for _, raw := range anySlice(firstAny(tier, "availableCredits", "available_credits")) {
		credit, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(stringFromAny(firstAny(credit, "creditType", "credit_type")), "GOOGLE_ONE_AI") {
			continue
		}
		return floatFromAny(firstAny(credit, "creditAmount", "credit_amount"))
	}
	return 0, false
}

func formatUnixSeconds(seconds int64) string {
	return time.Unix(seconds, 0).Format("01/02, 15:04")
}

func normalizeWindowID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func buildKimiRows(body string) ([]map[string]any, *float64, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	rows := make([]map[string]any, 0)
	if usage, ok := payload["usage"].(map[string]any); ok {
		if row := toKimiUsageRow(usage, map[string]any{"labelKey": "kimi_quota.weekly_limit"}); row != nil {
			row["id"] = "summary"
			rows = append(rows, row)
		}
	}
	for i, raw := range anySlice(payload["limits"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		detail := firstMap(item, "detail")
		if detail == nil {
			detail = item
		}
		window := firstMap(item, "window")
		if window == nil {
			window = map[string]any{}
		}
		if row := toKimiUsageRow(detail, kimiLimitLabel(item, detail, window, i)); row != nil {
			row["id"] = "limit-" + strconv.Itoa(i)
			rows = append(rows, row)
		}
	}
	usedValues := make([]float64, 0, len(rows))
	for _, row := range rows {
		used, okUsed := floatFromAny(row["used"])
		limit, okLimit := floatFromAny(row["limit"])
		if okUsed && okLimit && limit > 0 {
			usedValues = append(usedValues, math.Max(0, math.Min(100, (used/limit)*100)))
		}
	}
	return rows, maxFloatPtr(usedValues), nil
}

func buildXAIBillingSummary(body string) (map[string]any, *float64, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, nil, err
	}
	config := firstMap(payload, "config")
	if config == nil {
		return nil, nil, fmt.Errorf("empty xai billing config")
	}
	return buildXAIBillingSummaryFromConfig(config)
}

func buildXAIBillingSummaryFromConfig(config map[string]any) (map[string]any, *float64, error) {
	monthlyLimitCents, hasMonthlyLimit := xaiCentValue(firstAny(config, "monthlyLimit", "monthly_limit"))
	usedCents, hasUsed := xaiCentValue(config["used"])
	onDemandCapCents, hasOnDemandCap := xaiCentValue(firstAny(config, "onDemandCap", "on_demand_cap"))
	explicitOnDemandUsedCents, hasExplicitOnDemandUsed := xaiCentValue(firstAny(config, "onDemandUsed", "on_demand_used"))
	currentPeriod := firstMap(config, "currentPeriod", "current_period")
	periodType := xaiPeriodType(currentPeriod)
	creditUsagePercent, hasCreditUsagePercent := floatFromAny(firstAny(config, "creditUsagePercent", "credit_usage_percent"))
	productUsage := xaiProductUsage(firstAny(config, "productUsage", "product_usage"))
	billingPeriodStart := firstNonEmptyStringValue(stringFromAny(firstAny(config, "billingPeriodStart", "billing_period_start")))
	billingPeriodEnd := firstNonEmptyStringValue(stringFromAny(firstAny(config, "billingPeriodEnd", "billing_period_end")))
	periodStart := firstNonEmptyStringValue(stringFromAny(currentPeriod["start"]), billingPeriodStart)
	periodEnd := firstNonEmptyStringValue(stringFromAny(currentPeriod["end"]), billingPeriodEnd)
	hasWeeklyData := hasCreditUsagePercent || periodType == "weekly" || len(productUsage) > 0
	hasMonthlyData := hasMonthlyLimit || hasUsed || (!hasWeeklyData && (hasOnDemandCap || billingPeriodEnd != ""))
	if !hasWeeklyData && !hasMonthlyData {
		return nil, nil, fmt.Errorf("empty xai billing config")
	}
	summary := map[string]any{
		"periodType":          "unknown",
		"usagePercent":        nil,
		"productUsage":        productUsage,
		"monthlyLimitCents":   nil,
		"usedCents":           nil,
		"includedUsedCents":   nil,
		"onDemandCapCents":    nil,
		"onDemandUsedCents":   nil,
		"onDemandUsedPercent": nil,
		"usedPercent":         nil,
	}
	var includedUsedCents *float64
	if hasUsed {
		value := usedCents
		if hasMonthlyLimit && monthlyLimitCents > 0 {
			value = math.Min(usedCents, monthlyLimitCents)
		}
		includedUsedCents = &value
	}
	var onDemandUsedCents *float64
	if hasExplicitOnDemandUsed {
		value := explicitOnDemandUsedCents
		onDemandUsedCents = &value
	} else if hasUsed && hasMonthlyLimit {
		value := math.Max(0, usedCents-monthlyLimitCents)
		onDemandUsedCents = &value
	}
	var usedPercent *float64
	if hasMonthlyLimit && monthlyLimitCents > 0 && includedUsedCents != nil {
		value := (*includedUsedCents / monthlyLimitCents) * 100
		usedPercent = &value
	}
	var onDemandUsedPercent *float64
	if hasOnDemandCap && onDemandCapCents > 0 && onDemandUsedCents != nil {
		value := (*onDemandUsedCents / onDemandCapCents) * 100
		onDemandUsedPercent = &value
	}
	if hasWeeklyData {
		if periodType == "unknown" {
			summary["periodType"] = "weekly"
		} else {
			summary["periodType"] = periodType
		}
		if hasCreditUsagePercent {
			summary["usagePercent"] = creditUsagePercent
		}
		if periodStart != "" {
			summary["periodStart"] = periodStart
		}
		if periodEnd != "" {
			summary["periodEnd"] = periodEnd
		}
	} else {
		summary["periodType"] = "monthly"
		summary["usagePercent"] = floatPtrAny(usedPercent)
		if billingPeriodStart != "" {
			summary["periodStart"] = billingPeriodStart
		}
		if billingPeriodEnd != "" {
			summary["periodEnd"] = billingPeriodEnd
		}
	}
	if hasMonthlyLimit {
		summary["monthlyLimitCents"] = monthlyLimitCents
	}
	if hasUsed {
		summary["usedCents"] = usedCents
	}
	if includedUsedCents != nil {
		summary["includedUsedCents"] = *includedUsedCents
	}
	if hasOnDemandCap {
		summary["onDemandCapCents"] = onDemandCapCents
	}
	if onDemandUsedCents != nil {
		summary["onDemandUsedCents"] = *onDemandUsedCents
	}
	if onDemandUsedPercent != nil {
		summary["onDemandUsedPercent"] = *onDemandUsedPercent
	}
	if usedPercent != nil {
		summary["usedPercent"] = *usedPercent
	}
	if hasMonthlyData && billingPeriodStart != "" {
		summary["billingPeriodStart"] = billingPeriodStart
	}
	if hasMonthlyData && billingPeriodEnd != "" {
		summary["billingPeriodEnd"] = billingPeriodEnd
	}
	return summary, xaiSummaryUsedPercent(summary), nil
}

func xaiCentValue(value any) (float64, bool) {
	if mapped, ok := value.(map[string]any); ok {
		return floatFromAny(mapped["val"])
	}
	return floatFromAny(value)
}

func xaiPeriodType(period map[string]any) string {
	raw := strings.ToLower(stringFromAny(period["type"]))
	switch {
	case strings.Contains(raw, "weekly"):
		return "weekly"
	case strings.Contains(raw, "monthly"):
		return "monthly"
	default:
		return "unknown"
	}
}

func xaiProductUsage(value any) []map[string]any {
	items := make([]map[string]any, 0)
	for i, raw := range anySlice(value) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		product := firstNonEmptyStringValue(stringFromAny(item["product"]), fmt.Sprintf("Product %d", i+1))
		usagePercent, hasUsagePercent := floatFromAny(firstAny(item, "usagePercent", "usage_percent"))
		row := map[string]any{"product": product, "usagePercent": nil}
		if hasUsagePercent {
			row["usagePercent"] = usagePercent
		}
		items = append(items, row)
	}
	return items
}

func mergeXAIBillingSummaries(primary map[string]any, fallback map[string]any) map[string]any {
	if primary == nil {
		return fallback
	}
	if fallback == nil {
		return primary
	}
	merged := map[string]any{
		"periodType":          firstKnownXaiPeriodType(primary["periodType"], fallback["periodType"]),
		"usagePercent":        firstNonNilAny(primary["usagePercent"], fallback["usagePercent"]),
		"productUsage":        firstNonEmptyXaiProductUsage(primary["productUsage"], fallback["productUsage"]),
		"monthlyLimitCents":   firstNonNilAny(primary["monthlyLimitCents"], fallback["monthlyLimitCents"]),
		"usedCents":           firstNonNilAny(primary["usedCents"], fallback["usedCents"]),
		"includedUsedCents":   firstNonNilAny(primary["includedUsedCents"], fallback["includedUsedCents"]),
		"onDemandCapCents":    firstNonNilAny(primary["onDemandCapCents"], fallback["onDemandCapCents"]),
		"onDemandUsedCents":   firstNonNilAny(primary["onDemandUsedCents"], fallback["onDemandUsedCents"]),
		"onDemandUsedPercent": firstNonNilAny(primary["onDemandUsedPercent"], fallback["onDemandUsedPercent"]),
		"billingPeriodStart":  firstNonNilAny(primary["billingPeriodStart"], fallback["billingPeriodStart"]),
		"billingPeriodEnd":    firstNonNilAny(primary["billingPeriodEnd"], fallback["billingPeriodEnd"]),
		"usedPercent":         firstNonNilAny(primary["usedPercent"], fallback["usedPercent"]),
	}
	if value := firstNonNilAny(primary["periodStart"], fallback["periodStart"]); value != nil {
		merged["periodStart"] = value
	}
	if value := firstNonNilAny(primary["periodEnd"], fallback["periodEnd"]); value != nil {
		merged["periodEnd"] = value
	}
	return merged
}

func firstKnownXaiPeriodType(primary any, fallback any) any {
	if value := stringFromAny(primary); value != "" && value != "unknown" {
		return value
	}
	if value := stringFromAny(fallback); value != "" {
		return value
	}
	return "unknown"
}

func firstNonNilAny(primary any, fallback any) any {
	if primary != nil {
		return primary
	}
	return fallback
}

func firstNonEmptyXaiProductUsage(primary any, fallback any) any {
	if len(anySlice(primary)) > 0 {
		return primary
	}
	if len(anySlice(fallback)) > 0 {
		return fallback
	}
	return []map[string]any{}
}

func xaiSummaryUsedPercent(summary map[string]any) *float64 {
	for _, key := range []string{"usagePercent", "usage_percent", "usedPercent", "used_percent", "onDemandUsedPercent", "on_demand_used_percent"} {
		if value, ok := floatFromAny(summary[key]); ok {
			return &value
		}
	}
	values := make([]float64, 0)
	for _, raw := range anySlice(firstAny(summary, "productUsage", "product_usage")) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if value, ok := floatFromAny(firstAny(item, "usagePercent", "usage_percent")); ok {
			values = append(values, value)
		}
	}
	return maxFloatPtr(values)
}

func kimiLimitLabel(item map[string]any, detail map[string]any, window map[string]any, index int) map[string]any {
	for _, key := range []string{"name", "title", "scope"} {
		if value := firstNonEmptyStringValue(stringFromAny(item[key]), stringFromAny(detail[key])); value != "" {
			return map[string]any{"label": value}
		}
	}
	duration, ok := firstInt(window, item, detail, "duration")
	if ok && duration > 0 {
		return map[string]any{"labelKey": "kimi_quota.limit_window", "labelParams": map[string]any{"duration": kimiDurationToken(duration, firstAnyFromMaps([]map[string]any{window, item, detail}, "timeUnit"))}}
	}
	return map[string]any{"labelKey": "kimi_quota.limit_index", "labelParams": map[string]any{"index": index + 1}}
}

func toKimiUsageRow(data map[string]any, fallbackLabel map[string]any) map[string]any {
	limit, okLimit := intFromAny(data["limit"])
	used, okUsed := intFromAny(data["used"])
	if !okUsed {
		if remaining, okRemaining := intFromAny(data["remaining"]); okRemaining && okLimit {
			used = limit - remaining
			okUsed = true
		}
	}
	if !okLimit && !okUsed {
		return nil
	}
	row := make(map[string]any, len(fallbackLabel)+4)
	for key, value := range fallbackLabel {
		row[key] = value
	}
	if label := firstNonEmptyStringValue(stringFromAny(data["name"]), stringFromAny(data["title"])); label != "" {
		row["label"] = label
		delete(row, "labelKey")
		delete(row, "labelParams")
	}
	if okUsed {
		row["used"] = used
	} else {
		row["used"] = 0
	}
	if okLimit {
		row["limit"] = limit
	} else {
		row["limit"] = 0
	}
	row["resetHint"] = emptyStringAsNil(kimiResetHint(data))
	return row
}

func cloneFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func minFloatPtr(current *float64, next *float64) *float64 {
	if current == nil {
		return cloneFloatPtr(next)
	}
	if next == nil {
		return cloneFloatPtr(current)
	}
	value := math.Min(*current, *next)
	return &value
}

func pickEarlierResetTime(current string, next string) string {
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	nextTime, nextErr := time.Parse(time.RFC3339Nano, next)
	if currentErr != nil {
		return next
	}
	if nextErr != nil {
		return current
	}
	if currentTime.Before(nextTime) || currentTime.Equal(nextTime) {
		return current
	}
	return next
}

func floatPtrAny(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func firstAnyFromMaps(sources []map[string]any, key string) any {
	for _, source := range sources {
		if source == nil {
			continue
		}
		if value, ok := source[key]; ok {
			return value
		}
	}
	return nil
}

func firstInt(a map[string]any, b map[string]any, c map[string]any, key string) (int, bool) {
	for _, source := range []map[string]any{a, b, c} {
		if source == nil {
			continue
		}
		if value, ok := intFromAny(source[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func intFromAny(value any) (int, bool) {
	parsed, ok := floatFromAny(value)
	if !ok {
		return 0, false
	}
	return int(parsed), true
}

func kimiDurationToken(duration int, rawTimeUnit any) string {
	unit := strings.ToUpper(strings.TrimSpace(stringFromAny(rawTimeUnit)))
	switch unit {
	case "MINUTES":
		if duration%60 == 0 {
			return fmt.Sprintf("%dh", duration/60)
		}
		return fmt.Sprintf("%dm", duration)
	case "HOURS":
		return fmt.Sprintf("%dh", duration)
	case "DAYS":
		return fmt.Sprintf("%dd", duration)
	default:
		return fmt.Sprintf("%ds", duration)
	}
}

func kimiResetHint(data map[string]any) string {
	for _, key := range []string{"reset_at", "resetAt", "reset_time", "resetTime"} {
		raw := stringFromAny(data[key])
		if raw == "" {
			continue
		}
		truncated := regexpMustCompile(`(\.\d{6})\d+`).ReplaceAllString(raw, "$1")
		date, err := time.Parse(time.RFC3339Nano, truncated)
		if err != nil {
			continue
		}
		return kimiDurationHint(time.Until(date))
	}
	for _, key := range []string{"reset_in", "resetIn", "ttl"} {
		seconds, ok := intFromAny(data[key])
		if ok && seconds > 0 {
			return kimiDurationHint(time.Duration(seconds) * time.Second)
		}
	}
	return ""
}

func kimiDurationHint(delta time.Duration) string {
	if delta <= 0 {
		return ""
	}
	totalMinutes := int(delta / time.Minute)
	hours := totalMinutes / 60
	minutes := totalMinutes % 60
	if hours > 0 && minutes > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return "<1m"
}

func regexpMustCompile(expr string) *regexp.Regexp {
	return regexp.MustCompile(expr)
}

func maxUsedPercentFromWindows(windows []map[string]any) *float64 {
	values := make([]float64, 0, len(windows))
	for _, window := range windows {
		used, ok := floatFromAny(window["usedPercent"])
		if !ok {
			continue
		}
		values = append(values, used)
	}
	return maxFloatPtr(values)
}

func maxFloatPtr(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	maxValue := values[0]
	for _, value := range values[1:] {
		if value > maxValue {
			maxValue = value
		}
	}
	return &maxValue
}

func antigravityProjectID(auth *coreauth.Auth) string {
	for _, source := range []map[string]any{auth.Metadata, nestedMap(auth.Metadata, "installed"), nestedMap(auth.Metadata, "web")} {
		if source == nil {
			continue
		}
		if value := firstNonEmptyStringValue(stringFromAny(source["project_id"]), stringFromAny(source["projectId"])); value != "" {
			return value
		}
	}
	return "bamboo-precept-lgxtn"
}

func codexAccountID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := codexAccountIDFromMap(source); value != "" {
			return value
		}
	}
	return ""
}

func codexAccountIDFromMap(source map[string]any) string {
	if source == nil {
		return ""
	}
	for _, key := range []string{"chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"} {
		if value := stringFromAny(source[key]); value != "" {
			return value
		}
	}
	return idTokenClaim(source["id_token"], "chatgpt_account_id", "chatgptAccountId", "account_id", "accountId")
}

func xaiUserID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := xaiUserIDFromMap(source); value != "" {
			return value
		}
	}
	return ""
}

func xaiUserIDFromMap(source map[string]any) string {
	if source == nil {
		return ""
	}
	for _, key := range []string{"x_user_id", "xUserId", "user_id", "userId", "subject", "sub", "id"} {
		if value := stringFromAny(source[key]); value != "" {
			return value
		}
	}
	return idTokenStringClaim(source["id_token"], "sub", "id", "user_id", "userId")
}

func idTokenStringClaim(raw any, keys ...string) string {
	if mapped, ok := raw.(map[string]any); ok {
		for _, key := range keys {
			if value := stringFromAny(mapped[key]); value != "" {
				return value
			}
		}
		return ""
	}
	token := stringFromAny(raw)
	if token == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(token), &parsed); err == nil {
		for _, key := range keys {
			if value := stringFromAny(parsed[key]); value != "" {
				return value
			}
		}
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromAny(data[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringMapToAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func codexPlanType(auth *coreauth.Auth, payload map[string]any) any {
	if value := firstNonEmptyStringValue(stringFromAny(payload["plan_type"]), stringFromAny(payload["planType"])); value != "" {
		return value
	}
	for _, raw := range []any{auth.Metadata["plan_type"], auth.Metadata["planType"], auth.Attributes["plan_type"], auth.Attributes["planType"]} {
		if value := stringFromAny(raw); value != "" {
			return value
		}
	}
	return nil
}

func codexQuotaStateValues(auth *coreauth.Auth, payload map[string]any, windows []map[string]any, rawBody string) map[string]any {
	values := map[string]any{
		"windows":      windows,
		"planType":     codexPlanType(auth, payload),
		"rawShapeHash": jsonShapeHash(rawBody),
	}
	values["subscriptionActiveUntil"] = codexSubscriptionActiveUntil(auth)
	values["rateLimitResetCreditsAvailableCount"] = codexRateLimitResetCreditsAvailableCount(payload)
	return values
}

func codexSubscriptionActiveUntil(auth *coreauth.Auth) any {
	if auth == nil {
		return nil
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := codexSubscriptionActiveUntilFromMap(source); value != nil {
			return value
		}
	}
	return nil
}

func codexSubscriptionActiveUntilFromMap(source map[string]any) any {
	if source == nil {
		return nil
	}
	for _, key := range []string{"chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil", "subscription_active_until", "subscriptionActiveUntil"} {
		if value := dateLikeValue(source[key]); value != nil {
			return value
		}
	}
	for _, rawSubscription := range []any{source["subscription"], source["Subscription"]} {
		if subscription, ok := rawSubscription.(map[string]any); ok {
			for _, key := range []string{"active_until", "activeUntil"} {
				if value := dateLikeValue(subscription[key]); value != nil {
					return value
				}
			}
		}
	}
	if value := idTokenClaimAny(source["id_token"], "chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil", "subscription_active_until", "subscriptionActiveUntil"); value != nil {
		return value
	}
	return nil
}

func codexRateLimitResetCreditsAvailableCount(payload map[string]any) any {
	if payload == nil {
		return nil
	}
	resetCredits, _ := firstAny(payload, "rate_limit_reset_credits", "rateLimitResetCredits").(map[string]any)
	if resetCredits == nil {
		return nil
	}
	if value, ok := floatFromAny(firstAny(resetCredits, "available_count", "availableCount")); ok {
		return value
	}
	return nil
}

func dateLikeValue(value any) any {
	if number, ok := floatFromAny(value); ok {
		if number == 0 {
			return nil
		}
		return value
	}
	if text := stringFromAny(value); text != "" && text != "0" {
		return text
	}
	return nil
}

func idTokenClaim(raw any, keys ...string) string {
	value := idTokenClaimAny(raw, keys...)
	if text := stringFromAny(value); text != "" {
		return text
	}
	return ""
}

func idTokenClaimAny(raw any, keys ...string) any {
	switch value := raw.(type) {
	case map[string]any:
		for _, key := range keys {
			if claim := dateLikeValue(value[key]); claim != nil {
				return claim
			}
		}
		return nil
	}
	token := stringFromAny(raw)
	if token == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(token), &parsed); err == nil {
		for _, key := range keys {
			if value := dateLikeValue(parsed[key]); value != nil {
				return value
			}
		}
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil
	}
	for _, key := range keys {
		if value := dateLikeValue(data[key]); value != nil {
			return value
		}
	}
	return nil
}

func accountInspectionAuthEmail(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if value := firstNonEmptyAuthValue(auth, "email"); value != "" {
		return value
	}
	return idTokenClaim(auth.Metadata["id_token"], "email")
}

func firstNonEmptyAuthValue(auth *coreauth.Auth, keys ...string) string {
	if auth == nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromAny(auth.Metadata[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(auth.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstAny(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func firstMap(data map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := data[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func nestedMap(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}

func nestedString(data map[string]any, key string, child string) string {
	if child == "" {
		return stringFromAny(data[key])
	}
	return stringFromAny(nestedMap(data, key)[child])
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func firstNonEmptyStringValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func anySlice(value any) []any {
	switch items := value.(type) {
	case []any:
		return items
	case []map[string]any:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	case []string:
		out := make([]any, 0, len(items))
		for _, item := range items {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func formatResetTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "-"
	}
	return parsed.Local().Format("01/02, 15:04")
}

func floatFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(v))
		if trimmed == "true" || trimmed == "1" || trimmed == "yes" || trimmed == "y" || trimmed == "on" {
			return true, true
		}
		if trimmed == "false" || trimmed == "0" || trimmed == "no" || trimmed == "n" || trimmed == "off" {
			return false, true
		}
	case float64:
		return v != 0, true
	case int:
		return v != 0, true
	}
	return false, false
}

func boolFromAny(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	case float64:
		return v != 0
	default:
		return false
	}
}

func normalizeFraction(value float64) float64 {
	if value > 1 && value <= 100 {
		value = value / 100
	}
	return math.Max(0, math.Min(1, value))
}

func intPtr(value int) *int {
	return &value
}

func firstNonNilError(values ...error) error {
	for _, err := range values {
		if err != nil {
			return err
		}
	}
	return nil
}

func emptyStringAsNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func escapeJSONString(value string) string {
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), "\"")
}

func (h *Handler) RegisterAccountInspectionRoutes(group *gin.RouterGroup) {
	group.GET("/account-inspection/logs", h.StreamAccountInspectionLogs)
	group.GET("/account-inspection/schedule", h.GetAccountInspectionSchedule)
	group.PUT("/account-inspection/schedule", h.PutAccountInspectionSchedule)
	group.PATCH("/account-inspection/schedule", h.PutAccountInspectionSchedule)
	group.GET("/account-inspection/status", h.GetAccountInspectionStatus)
	group.POST("/account-inspection/run", h.RunAccountInspection)
	group.POST("/account-inspection/inspect-one", h.InspectOneAccount)
	group.POST("/account-inspection/refresh-token", h.RefreshAccountInspectionToken)
	group.POST("/account-inspection/pause", h.PauseAccountInspection)
	group.POST("/account-inspection/resume", h.ResumeAccountInspection)
	group.POST("/account-inspection/stop", h.StopAccountInspection)
	group.POST("/account-inspection/actions", h.ExecuteAccountInspectionActions)
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
	if err := scheduler.update(schedule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		statusCode := http.StatusOK
		if errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) || errors.Is(err, errAccountInspectionResultStale) {
			statusCode = http.StatusConflict
		}
		c.JSON(statusCode, gin.H{"error": err.Error(), "result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
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
		scheduler.status.Results = sortAccountInspectionResults(scheduler.status.Results)
		saveErr = scheduler.saveResultSnapshotLocked()
	}
	broadcast := scheduler.statusBroadcastLocked()
	scheduler.mu.Unlock()
	broadcast.send()
	err = firstNonNilError(err, saveErr)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		statusCode := http.StatusOK
		if errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) || errors.Is(err, errAccountInspectionResultStale) {
			statusCode = http.StatusConflict
		}
		c.JSON(statusCode, gin.H{"error": err.Error(), "result": result, "schedule": snapshot["schedule"], "status": snapshot["status"]})
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
	outcomes, err := scheduler.executeManualActions(c.Request.Context(), request.Items)
	snapshot := scheduler.snapshotForRequest(c)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, errAccountInspectionRestoredSnapshotReadOnly) || errors.Is(err, errAccountInspectionResultStale) {
			statusCode = http.StatusConflict
		}
		c.JSON(statusCode, gin.H{
			"error":    err.Error(),
			"outcomes": outcomes,
			"summary":  summarizeManualActionOutcomes(outcomes),
			"schedule": snapshot["schedule"],
			"status":   snapshot["status"],
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"outcomes": outcomes,
		"summary":  summarizeManualActionOutcomes(outcomes),
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

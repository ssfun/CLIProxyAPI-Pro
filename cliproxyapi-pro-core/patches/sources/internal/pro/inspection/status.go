package inspection

import "strings"

type LogEntry struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type Progress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	InFlight  int `json:"inFlight"`
	Pending   int `json:"pending"`
}

// RunProviderStats contains aggregate timings and counts for one inspection
// run. It carries no account identifiers or response bodies.
type RunProviderStats struct {
	Accounts          int   `json:"accounts"`
	Completed         int   `json:"completed"`
	Failed            int   `json:"failed"`
	Unknown           int   `json:"unknown"`
	HTTPRequests      int   `json:"httpRequests"`
	Retries           int   `json:"retries"`
	RealProbeRequests int   `json:"realProbeRequests"`
	QuotaKnown        int   `json:"quotaKnown"`
	WallTimeMs        int64 `json:"wallTimeMs"`
	QueueWaitMs       int64 `json:"queueWaitMs"`
	NetworkMs         int64 `json:"networkMs"`
	RefreshMs         int64 `json:"refreshMs"`
	PrimaryProbeMs    int64 `json:"primaryProbeMs"`
	ConfirmProbeMs    int64 `json:"confirmProbeMs"`
	MaxAccountMs      int64 `json:"maxAccountMs"`
}

type RunStats struct {
	WallTimeMs int64                       `json:"wallTimeMs"`
	Providers  map[string]RunProviderStats `json:"providers"`
}

type Status struct {
	State                RunState                `json:"state"`
	RunSettings          Settings                `json:"runSettings"`
	LastStartedAt        int64                   `json:"lastStartedAt"`
	LastFinishedAt       int64                   `json:"lastFinishedAt"`
	LastError            string                  `json:"lastError"`
	PersistenceError     string                  `json:"persistenceError,omitempty"`
	Progress             Progress                `json:"progress"`
	Summary              Summary                 `json:"summary"`
	RunStats             *RunStats               `json:"runStats,omitempty"`
	HealthCounts         *HealthCounts           `json:"healthCounts,omitempty"`
	ProviderHealthCounts map[string]HealthCounts `json:"providerHealthCounts,omitempty"`
	LogsPage             *PageInfo               `json:"logsPage,omitempty"`
	ResultsPage          *PageInfo               `json:"resultsPage,omitempty"`
	LogsLimited          bool                    `json:"logsLimited,omitempty"`
	ResultsLimited       bool                    `json:"resultsLimited,omitempty"`
	RestoredSnapshot     bool                    `json:"restoredSnapshot,omitempty"`
	Logs                 []LogEntry              `json:"logs"`
	Results              []Result                `json:"results"`
}

type SnapshotOptions struct {
	IncludeDetails    bool
	ResultPage        int
	ResultPageSize    int
	ResultFilter      string
	ResultPendingOnly bool
	ResultProvider    string
	ResultSearch      string
	LogPage           int
	LogPageSize       int
	LogLevel          string
}

type StreamMessageType string

const (
	StreamSnapshot StreamMessageType = "snapshot"
	StreamLog      StreamMessageType = "log"
	StreamStatus   StreamMessageType = "status"
)

type LogStreamMessage struct {
	Type     StreamMessageType `json:"type"`
	Schedule Schedule          `json:"schedule"`
	Status   Status            `json:"status"`
	Log      *LogEntry         `json:"log,omitempty"`
}

func PaginateLogs(logs []LogEntry, page, pageSize, maxPageSize int, level string) ([]LogEntry, PageInfo) {
	page = normalizePage(page)
	pageSize = normalizePageSize(pageSize, 100, maxPageSize)
	filtered := make([]LogEntry, 0, len(logs))
	for _, entry := range logs {
		if level == "" || level == "all" || strings.EqualFold(entry.Level, level) {
			filtered = append(filtered, entry)
		}
	}
	total := len(filtered)
	info := ResultPageInfo(total, page, pageSize)
	if total == 0 {
		return []LogEntry{}, info
	}
	end := total - (page-1)*pageSize
	if end <= 0 {
		return []LogEntry{}, info
	}
	start := end - pageSize
	if start < 0 {
		start = 0
	}
	return append([]LogEntry(nil), filtered[start:end]...), info
}

func ProjectStatus(status Status, healthCounts HealthCounts, options SnapshotOptions, maxResultPageSize, maxLogPageSize int) Status {
	if !options.IncludeDetails {
		status.HealthCounts = nil
		status.ProviderHealthCounts = nil
		status.LogsPage = nil
		status.ResultsPage = nil
		status.Logs = nil
		status.Results = nil
		status.LogsLimited = false
		status.ResultsLimited = false
		return status
	}

	logs, logsPage := PaginateLogs(status.Logs, options.LogPage, options.LogPageSize, maxLogPageSize, options.LogLevel)
	results, resultsPage := PaginateResults(status.Results, options.ResultPage, options.ResultPageSize, maxResultPageSize, options.ResultFilter, options.ResultPendingOnly, options.ResultProvider, options.ResultSearch)
	status.HealthCounts = &healthCounts
	status.ProviderHealthCounts = ResultProviderHealthCounts(status.Results)
	status.Logs = logs
	status.Results = results
	status.LogsPage = &logsPage
	status.ResultsPage = &resultsPage
	status.LogsLimited = logsPage.Total > len(logs)
	status.ResultsLimited = resultsPage.Total > len(results)
	return status
}

func MergeTokenRefreshResult(current, incoming Result) (Result, bool) {
	current.ObservedAt = incoming.ObservedAt
	current.ParentResultRef = incoming.ParentResultRef
	current.RunID = incoming.RunID
	current.RegistrationEpoch = incoming.RegistrationEpoch
	current.ResultRef = incoming.ResultRef
	current.Executed = false
	current.ExecutedAction = ""
	current.ExecutedEffect = ""
	current.ExecutedAt = 0
	current.ExecutedSuggested = false
	current.OperationAction = ""
	current.ExecuteError = ""
	current.Provider = incoming.Provider
	current.FileName = incoming.FileName
	current.DisplayName = incoming.DisplayName
	current.Email = incoming.Email
	current.Name = incoming.Name
	current.AuthIndex = incoming.AuthIndex
	current.Disabled = incoming.Disabled
	current.TokenRefreshTriggered = incoming.TokenRefreshTriggered
	current.TokenRefreshStatus = incoming.TokenRefreshStatus
	current.TokenRefreshError = incoming.TokenRefreshError
	current.NextRefreshAt = incoming.NextRefreshAt
	current.Action = ActionKeep
	current.ActionReason = "凭据刷新后需重新巡检"
	if incoming.TokenRefreshStatus == "failed" {
		current.Error = incoming.Error
		current.ErrorDetail = incoming.ErrorDetail
		current.ErrorCode = incoming.ErrorCode
		current.ActionReason = incoming.ActionReason
		return current, true
	}
	if incoming.TokenRefreshStatus == "success" && current.ErrorCode == "token_refresh_error" {
		current.Error = ""
		current.ErrorDetail = ""
		current.ErrorCode = ""
		current.ActionReason = incoming.ActionReason
		return current, true
	}
	return current, false
}

func MergeReinspectionResult(current, incoming Result) (Result, bool) {
	return incoming, true
}

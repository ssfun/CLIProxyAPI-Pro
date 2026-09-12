const (
	// QuotaSnapshotSchemaVersion is the normalized quota snapshot schema understood by this host.
	QuotaSnapshotSchemaVersion = 1
)

// QuotaSnapshot is a provider-neutral quota and subscription snapshot.
type QuotaSnapshot struct {
	SchemaVersion int            `json:"schema_version"`
	Provider      string         `json:"provider"`
	ObservedAtMS  int64          `json:"observed_at_ms"`
	Items         []QuotaItem    `json:"items"`
	Plan          *QuotaPlan     `json:"plan,omitempty"`
	Warnings      []QuotaWarning `json:"warnings,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// QuotaItem describes one normalized quota window or bucket.
type QuotaItem struct {
	ID                string         `json:"id"`
	Label             string         `json:"label"`
	Kind              string         `json:"kind,omitempty"`
	RemainingFraction *float64       `json:"remaining_fraction,omitempty"`
	UsedPercent       *float64       `json:"used_percent,omitempty"`
	RemainingAmount   *float64       `json:"remaining_amount,omitempty"`
	Limit             *float64       `json:"limit,omitempty"`
	Unit              string         `json:"unit,omitempty"`
	ResetAt           string         `json:"reset_at,omitempty"`
	ModelIDs          []string       `json:"model_ids,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// QuotaPlan describes subscription or account tier information.
type QuotaPlan struct {
	ID             string         `json:"id,omitempty"`
	Label          string         `json:"label,omitempty"`
	Kind           string         `json:"kind,omitempty"`
	CreditBalance  *float64       `json:"credit_balance,omitempty"`
	ObservedAtMS   int64          `json:"observed_at_ms,omitempty"`
	Stale          bool           `json:"stale,omitempty"`
	Error          string         `json:"error,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// QuotaWarning describes a non-fatal quota probe issue.
type QuotaWarning struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

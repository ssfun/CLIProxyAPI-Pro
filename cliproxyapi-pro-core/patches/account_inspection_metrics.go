package management

import (
	"context"
	"strings"
	"sync"
	"time"

	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

type inspectionRunMetricsKey struct{}
type inspectionAccountMetricsKey struct{}

type inspectionRunMetrics struct {
	mu        sync.Mutex
	startedAt time.Time
	providers map[string]*inspectionProviderMetricTotals
}

type inspectionProviderMetricTotals struct {
	stats        proinspection.RunProviderStats
	firstStart   time.Time
	lastFinish   time.Time
	queueWait    time.Duration
	network      time.Duration
	refresh      time.Duration
	primary      time.Duration
	confirmation time.Duration
	maxAccount   time.Duration
}

type inspectionAccountMetrics struct {
	mu                sync.Mutex
	provider          string
	startedAt         time.Time
	queueWait         time.Duration
	httpRequests      int
	retries           int
	realProbes        int
	network           time.Duration
	refresh           time.Duration
	primary           time.Duration
	confirmation      time.Duration
	confirmationStage time.Duration
}

func inspectionMetricsContext(ctx context.Context, metrics *inspectionRunMetrics) context.Context {
	if metrics == nil {
		return ctx
	}
	return context.WithValue(ctx, inspectionRunMetricsKey{}, metrics)
}

func inspectionMetricsStartAccount(ctx context.Context, provider string, queuedAt time.Time) (context.Context, func(accountInspectionResult)) {
	metrics, _ := ctx.Value(inspectionRunMetricsKey{}).(*inspectionRunMetrics)
	if metrics == nil {
		return ctx, func(accountInspectionResult) {}
	}
	startedAt := time.Now()
	wait := startedAt.Sub(queuedAt)
	if wait < 0 {
		wait = 0
	}
	account := &inspectionAccountMetrics{provider: provider, startedAt: startedAt, queueWait: wait}
	ctx = context.WithValue(ctx, inspectionAccountMetricsKey{}, account)
	return ctx, func(result accountInspectionResult) {
		account.mu.Lock()
		finishedAt := time.Now()
		elapsed := finishedAt.Sub(startedAt)
		accountCopy := inspectionAccountMetrics{
			queueWait: account.queueWait, httpRequests: account.httpRequests,
			retries: account.retries, realProbes: account.realProbes,
			network: account.network, refresh: account.refresh,
			primary: account.primary, confirmation: account.confirmation,
			confirmationStage: account.confirmationStage,
		}
		account.mu.Unlock()
		metrics.mu.Lock()
		defer metrics.mu.Unlock()
		total := metrics.providers[provider]
		if total == nil {
			total = &inspectionProviderMetricTotals{}
			metrics.providers[provider] = total
		}
		if total.firstStart.IsZero() || startedAt.Before(total.firstStart) {
			total.firstStart = startedAt
		}
		if finishedAt.After(total.lastFinish) {
			total.lastFinish = finishedAt
		}
		total.stats.Accounts++
		switch {
		case proinspection.HealthBucketOf(result) == proinspection.HealthUnknown:
			total.stats.Unknown++
		case result.Error != "" || result.ErrorCode != "":
			total.stats.Failed++
		default:
			total.stats.Completed++
		}
		if result.QuotaKnown {
			total.stats.QuotaKnown++
		}
		total.stats.HTTPRequests += accountCopy.httpRequests
		total.stats.Retries += accountCopy.retries
		total.stats.RealProbeRequests += accountCopy.realProbes
		total.queueWait += accountCopy.queueWait
		total.network += accountCopy.network
		total.refresh += accountCopy.refresh
		confirm := accountCopy.confirmationStage
		if confirm == 0 {
			confirm = accountCopy.confirmation
		}
		primary := accountCopy.primary - confirm
		if primary < 0 {
			primary = 0
		}
		total.primary += primary
		total.confirmation += confirm
		if elapsed > total.maxAccount {
			total.maxAccount = elapsed
		}
	}
}

func inspectionMetricsStartRefresh(ctx context.Context) func() {
	return inspectionMetricsStartStage(ctx, true)
}

func inspectionMetricsStartPrimary(ctx context.Context) func() {
	return inspectionMetricsStartStage(ctx, false)
}

func inspectionMetricsStartConfirmation(ctx context.Context) func() {
	account, _ := ctx.Value(inspectionAccountMetricsKey{}).(*inspectionAccountMetrics)
	if account == nil {
		return func() {}
	}
	startedAt := time.Now()
	return func() {
		account.mu.Lock()
		account.confirmationStage += time.Since(startedAt)
		account.mu.Unlock()
	}
}

func inspectionMetricsStartStage(ctx context.Context, refresh bool) func() {
	account, _ := ctx.Value(inspectionAccountMetricsKey{}).(*inspectionAccountMetrics)
	if account == nil {
		return func() {}
	}
	startedAt := time.Now()
	return func() {
		account.mu.Lock()
		if refresh {
			account.refresh += time.Since(startedAt)
		} else {
			account.primary += time.Since(startedAt)
		}
		account.mu.Unlock()
	}
}

func inspectionMetricsRecordRequest(ctx context.Context, url string, elapsed time.Duration) {
	account, _ := ctx.Value(inspectionAccountMetricsKey{}).(*inspectionAccountMetrics)
	if account == nil {
		return
	}
	account.mu.Lock()
	account.httpRequests++
	account.network += elapsed
	if inspectionIsRealProbeURL(url) {
		account.realProbes++
		account.confirmation += elapsed
	}
	account.mu.Unlock()
}

func inspectionMetricsRecordRetry(ctx context.Context) {
	account, _ := ctx.Value(inspectionAccountMetricsKey{}).(*inspectionAccountMetrics)
	if account == nil {
		return
	}
	account.mu.Lock()
	account.retries++
	account.mu.Unlock()
}

func inspectionIsRealProbeURL(url string) bool {
	url = strings.ToLower(url)
	return strings.Contains(url, "generatecontent") ||
		strings.Contains(url, "/chat/completions") ||
		strings.Contains(url, "/responses")
}

func (metrics *inspectionRunMetrics) snapshot(finishedAt time.Time) *proinspection.RunStats {
	if metrics == nil {
		return nil
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	stats := &proinspection.RunStats{
		WallTimeMs: finishedAt.Sub(metrics.startedAt).Milliseconds(),
		Providers:  make(map[string]proinspection.RunProviderStats, len(metrics.providers)),
	}
	for provider, total := range metrics.providers {
		providerStats := total.stats
		providerStats.QueueWaitMs = total.queueWait.Milliseconds()
		providerStats.WallTimeMs = total.lastFinish.Sub(total.firstStart).Milliseconds()
		providerStats.NetworkMs = total.network.Milliseconds()
		providerStats.RefreshMs = total.refresh.Milliseconds()
		providerStats.PrimaryProbeMs = total.primary.Milliseconds()
		providerStats.ConfirmProbeMs = total.confirmation.Milliseconds()
		providerStats.MaxAccountMs = total.maxAccount.Milliseconds()
		stats.Providers[provider] = providerStats
	}
	return stats
}

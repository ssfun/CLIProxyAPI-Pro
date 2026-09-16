package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/pool"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/socks5"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	proxy "golang.org/x/net/proxy"
)

type Status struct {
	Ready         bool                `json:"ready"`
	Listen        string              `json:"listen"`
	ProxyURL      string              `json:"proxy_url"`
	Strategy      string              `json:"strategy"`
	Generation    uint64              `json:"generation"`
	ActiveTunnels int64               `json:"active_tunnels"`
	TotalNodes    int                 `json:"total_nodes"`
	HealthyNodes  int                 `json:"healthy_nodes"`
	EligibleNodes int                 `json:"eligible_nodes"`
	IsolatedNodes int                 `json:"isolated_nodes"`
	LastError     string              `json:"last_error,omitempty"`
	StartedAt     time.Time           `json:"started_at,omitempty"`
	LastAppliedAt time.Time           `json:"last_applied_at,omitempty"`
	LastHealthAt  time.Time           `json:"last_health_at,omitempty"`
	Nodes         []pool.NodeSnapshot `json:"nodes"`
}

type ProbeResult struct {
	Success   bool   `json:"success"`
	NodeID    string `json:"node_id"`
	LatencyMS int64  `json:"latency_ms"`
	ExitIP    string `json:"exit_ip,omitempty"`
	Country   string `json:"country,omitempty"`
	Region    string `json:"region,omitempty"`
	City      string `json:"city,omitempty"`
	ISP       string `json:"isp,omitempty"`
	Location  string `json:"location,omitempty"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checked_at"`
}

type Engine struct {
	mu            sync.RWMutex
	cfg           proxyconfig.Config
	pool          *pool.Pool
	server        *socks5.Server
	lastError     string
	startedAt     time.Time
	lastAppliedAt time.Time
	lastHealthAt  time.Time
	generation    atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New() *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{ctx: ctx, cancel: cancel, startedAt: time.Now().UTC()}
}

func (e *Engine) ApplyConfig(cfg proxyconfig.Config) error {
	if err := cfg.NormalizeAndValidate(); err != nil {
		e.setLastError(err)
		return err
	}

	e.mu.Lock()
	currentServer := e.server
	currentListen := e.cfg.Listen
	currentPool := e.pool
	e.mu.Unlock()

	if currentServer == nil || currentListen != cfg.Listen {
		// Bind the replacement listener before mutating the active pool. A failed
		// listen-address change must leave the previous runtime fully usable.
		replacementPool := pool.New(cfg)
		listener, errListen := net.Listen("tcp", cfg.Listen)
		if errListen != nil {
			err := fmt.Errorf("listen on %s: %w", cfg.Listen, errListen)
			e.setLastError(err)
			return err
		}
		server, errServer := socks5.Start(listener, e.dial)
		if errServer != nil {
			_ = listener.Close()
			e.setLastError(errServer)
			return errServer
		}
		e.mu.Lock()
		oldServer := e.server
		e.server = server
		e.pool = replacementPool
		e.cfg = cfg
		e.lastError = ""
		e.lastAppliedAt = time.Now().UTC()
		e.mu.Unlock()
		if oldServer != nil {
			oldServer.Close()
		}
	} else {
		currentPool.Reconfigure(cfg)
		e.mu.Lock()
		e.pool = currentPool
		e.cfg = cfg
		e.lastError = ""
		e.lastAppliedAt = time.Now().UTC()
		e.mu.Unlock()
	}

	if e.generation.Add(1) == 1 {
		e.wg.Add(1)
		go e.healthLoop()
	}
	return nil
}

func (e *Engine) Close() {
	if e == nil {
		return
	}
	e.cancel()
	e.mu.Lock()
	server := e.server
	e.server = nil
	e.mu.Unlock()
	if server != nil {
		server.Close()
	}
	e.wg.Wait()
}

func (e *Engine) Status() Status {
	e.mu.RLock()
	cfg := e.cfg
	server := e.server
	poolRef := e.pool
	lastError := e.lastError
	startedAt := e.startedAt
	lastAppliedAt := e.lastAppliedAt
	lastHealthAt := e.lastHealthAt
	e.mu.RUnlock()
	proxyURL := ""
	if cfg.Listen != "" {
		proxyURL = "socks5://" + cfg.Listen
	}
	status := Status{
		Ready:         server != nil,
		Listen:        cfg.Listen,
		ProxyURL:      proxyURL,
		Strategy:      cfg.Strategy,
		Generation:    e.generation.Load(),
		LastError:     lastError,
		StartedAt:     startedAt,
		LastAppliedAt: lastAppliedAt,
		LastHealthAt:  lastHealthAt,
		Nodes:         []pool.NodeSnapshot{},
	}
	if poolRef == nil {
		return status
	}
	status.Nodes = poolRef.Snapshots()
	status.TotalNodes = len(status.Nodes)
	status.EligibleNodes = poolRef.EligibleCount(time.Now())
	for _, node := range status.Nodes {
		status.ActiveTunnels += node.ActiveTunnels
		switch node.State {
		case pool.HealthHealthy:
			status.HealthyNodes++
		case pool.HealthIsolated:
			status.IsolatedNodes++
		}
	}
	return status
}

func (e *Engine) ResetStats() {
	e.mu.RLock()
	poolRef := e.pool
	e.mu.RUnlock()
	if poolRef != nil {
		poolRef.ResetStats()
	}
}

func (e *Engine) Recover(nodeID string) error {
	e.mu.RLock()
	poolRef := e.pool
	e.mu.RUnlock()
	if poolRef == nil {
		return fmt.Errorf("proxy pool is not initialized")
	}
	if !poolRef.Recover(nodeID) {
		return fmt.Errorf("proxy node not found")
	}
	return nil
}

func (e *Engine) Probe(ctx context.Context, nodeID, rawURL string) ProbeResult {
	e.mu.RLock()
	poolRef := e.pool
	cfg := e.cfg
	e.mu.RUnlock()
	result := ProbeResult{NodeID: strings.TrimSpace(nodeID), CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	if poolRef == nil {
		result.Error = "proxy pool is not initialized"
		return result
	}
	node := poolRef.Node(result.NodeID)
	if node == nil {
		result.Error = "proxy node not found"
		return result
	}
	if strings.TrimSpace(rawURL) == "" {
		rawURL = cfg.HealthCheck.TestURL
	}
	return probeProxyURL(ctx, result, node.URL(), rawURL, cfg.HealthCheck.Timeout.Duration, node, cfg)
}

// ProbeDraft validates and tests a proxy URL without first persisting it in the
// persisted configuration. Draft probes do not mutate the health state or runtime
// counters of an existing node with the same ID.
func (e *Engine) ProbeDraft(ctx context.Context, nodeID, rawProxyURL, rawTestURL string) ProbeResult {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()
	result := ProbeResult{NodeID: strings.TrimSpace(nodeID), CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	// Draft probes are independent from the running takeover state. When the
	// pool has not been applied yet, cfg is the zero value and validating the
	// draft against it would reject the URL with "unsupported strategy \"\"".
	// Keep only the runtime values that are relevant to probing and use the
	// normal defaults for the rest.
	probeConfig := proxyconfig.Default()
	if strings.TrimSpace(cfg.Listen) != "" {
		probeConfig.Listen = cfg.Listen
	}
	if cfg.HealthCheck.Timeout.Duration > 0 {
		probeConfig.HealthCheck.Timeout = cfg.HealthCheck.Timeout
	}
	if strings.TrimSpace(cfg.HealthCheck.TestURL) != "" {
		probeConfig.HealthCheck.TestURL = cfg.HealthCheck.TestURL
	}
	validationConfig := probeConfig
	validationConfig.Nodes = []proxyconfig.NodeConfig{{ID: "draft", URL: strings.TrimSpace(rawProxyURL), Enabled: true, Weight: 1}}
	if errValidate := validationConfig.NormalizeAndValidate(); errValidate != nil {
		result.Error = errValidate.Error()
		return result
	}
	if strings.TrimSpace(rawTestURL) == "" {
		rawTestURL = probeConfig.HealthCheck.TestURL
	}
	return probeProxyURL(ctx, result, validationConfig.Nodes[0].URL, rawTestURL, probeConfig.HealthCheck.Timeout.Duration, nil, probeConfig)
}

func probeProxyURL(ctx context.Context, result ProbeResult, rawProxyURL, rawTestURL string, timeout time.Duration, node *pool.Node, cfg proxyconfig.Config) ProbeResult {
	rawURL := rawTestURL
	parsedURL, errURL := url.Parse(rawURL)
	if errURL != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		result.Error = "invalid probe URL"
		return result
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		return dialNode(dialCtx, rawProxyURL, address)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if errRequest != nil {
		result.Error = errRequest.Error()
		return result
	}
	started := time.Now()
	response, errDo := client.Do(request)
	result.LatencyMS = time.Since(started).Milliseconds()
	if errDo != nil {
		result.Error = errDo.Error()
		if node != nil {
			node.MarkFailure(errDo, cfg.HealthCheck.IsolationThreshold, cfg.HealthCheck.IsolationDuration.Duration)
		}
		return result
	}
	defer response.Body.Close()
	body, errRead := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if errRead != nil {
		result.Error = errRead.Error()
		if node != nil {
			node.MarkFailure(errRead, cfg.HealthCheck.IsolationThreshold, cfg.HealthCheck.IsolationDuration.Duration)
		}
		return result
	}
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		errStatus := fmt.Errorf("probe returned HTTP %d", response.StatusCode)
		result.Error = errStatus.Error()
		if node != nil {
			node.MarkFailure(errStatus, cfg.HealthCheck.IsolationThreshold, cfg.HealthCheck.IsolationDuration.Duration)
		}
		return result
	}
	decodeProbeBody(body, &result)
	result.Success = true
	if node != nil {
		node.MarkSuccess(time.Duration(result.LatencyMS) * time.Millisecond)
		node.SetProbeResult(result.ExitIP, result.Location, time.Duration(result.LatencyMS)*time.Millisecond)
	}
	return result
}

const (
	DefaultProbeConcurrency = 4
	MaxProbeConcurrency     = 8
)

func NormalizeProbeConcurrency(concurrency int) int {
	if concurrency <= 0 {
		return DefaultProbeConcurrency
	}
	if concurrency > MaxProbeConcurrency {
		return MaxProbeConcurrency
	}
	return concurrency
}

func (e *Engine) ProbeAll(ctx context.Context, concurrency int) []ProbeResult {
	e.mu.RLock()
	poolRef := e.pool
	e.mu.RUnlock()
	if poolRef == nil {
		return []ProbeResult{}
	}
	snapshots := poolRef.Snapshots()
	concurrency = NormalizeProbeConcurrency(concurrency)
	semaphore := make(chan struct{}, concurrency)
	results := make([]ProbeResult, len(snapshots))
	var wg sync.WaitGroup
	for index, snapshot := range snapshots {
		if !snapshot.Enabled {
			results[index] = ProbeResult{NodeID: snapshot.ID, Error: "proxy node is disabled", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
			continue
		}
		wg.Add(1)
		go func(index int, nodeID string) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = ProbeResult{NodeID: nodeID, Error: ctx.Err().Error(), CheckedAt: time.Now().UTC().Format(time.RFC3339)}
				return
			}
			results[index] = e.Probe(ctx, nodeID, "")
		}(index, snapshot.ID)
	}
	wg.Wait()
	return results
}

func (e *Engine) dial(ctx context.Context, target string) (socks5.DialResult, error) {
	e.mu.RLock()
	poolRef := e.pool
	cfg := e.cfg
	e.mu.RUnlock()
	if poolRef == nil {
		return socks5.DialResult{}, fmt.Errorf("proxy pool is not initialized")
	}
	excluded := make(map[string]struct{})
	attemptLimit := cfg.MaxFailoverAttempts
	if attemptLimit <= 0 || attemptLimit > len(poolRef.Snapshots()) {
		attemptLimit = len(poolRef.Snapshots())
	}
	var errorsSeen []error
	for attempt := 0; attempt < attemptLimit; attempt++ {
		node := poolRef.Select(excluded)
		if node == nil {
			break
		}
		excluded[node.ID()] = struct{}{}
		node.MarkAttempt()
		dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout.Duration)
		started := time.Now()
		conn, errDial := dialNode(dialCtx, node.URL(), target)
		cancel()
		if errDial != nil {
			node.MarkFailure(errDial, cfg.HealthCheck.IsolationThreshold, cfg.HealthCheck.IsolationDuration.Duration)
			errorsSeen = append(errorsSeen, fmt.Errorf("%s: %w", node.ID(), errDial))
			continue
		}
		node.MarkSuccess(time.Since(started))
		node.Acquire()
		return socks5.DialResult{Conn: conn, Release: node.Release}, nil
	}
	if len(errorsSeen) == 0 {
		if cfg.FailOpen {
			directCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout.Duration)
			defer cancel()
			conn, errDial := (&net.Dialer{}).DialContext(directCtx, "tcp", target)
			return socks5.DialResult{Conn: conn}, errDial
		}
		return socks5.DialResult{}, fmt.Errorf("no eligible proxy node")
	}
	if cfg.FailOpen {
		directCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout.Duration)
		defer cancel()
		conn, errDial := (&net.Dialer{}).DialContext(directCtx, "tcp", target)
		if errDial == nil {
			return socks5.DialResult{Conn: conn}, nil
		}
		errorsSeen = append(errorsSeen, fmt.Errorf("direct fallback: %w", errDial))
	}
	return socks5.DialResult{}, fmt.Errorf("all selected proxy nodes failed: %w", errors.Join(errorsSeen...))
}

func (e *Engine) healthLoop() {
	defer e.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-timer.C:
		}
		e.runHealthChecks(e.ctx)
		e.mu.RLock()
		interval := e.cfg.HealthCheck.Interval.Duration
		enabled := e.cfg.HealthCheck.Enabled
		e.mu.RUnlock()
		if !enabled {
			interval = 30 * time.Second
		}
		if interval <= 0 {
			interval = 30 * time.Second
		}
		timer.Reset(interval)
	}
}

func (e *Engine) runHealthChecks(ctx context.Context) {
	e.mu.RLock()
	poolRef := e.pool
	cfg := e.cfg
	e.mu.RUnlock()
	if poolRef == nil || !cfg.HealthCheck.Enabled {
		return
	}
	nodes := poolRef.NodesForCheck(time.Now())
	semaphore := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node *pool.Node) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			checkCtx, cancel := context.WithTimeout(ctx, cfg.HealthCheck.Timeout.Duration)
			started := time.Now()
			conn, errDial := dialNode(checkCtx, node.URL(), cfg.HealthCheck.ProbeAddress)
			cancel()
			if conn != nil {
				_ = conn.Close()
			}
			node.MarkCheck(time.Since(started), errDial, cfg.HealthCheck.IsolationThreshold, cfg.HealthCheck.IsolationDuration.Duration)
		}(node)
	}
	wg.Wait()
	e.mu.Lock()
	e.lastHealthAt = time.Now().UTC()
	e.mu.Unlock()
}

func (e *Engine) setLastError(err error) {
	e.mu.Lock()
	if err == nil {
		e.lastError = ""
	} else {
		e.lastError = err.Error()
	}
	e.mu.Unlock()
}

func dialNode(ctx context.Context, rawProxyURL, target string) (net.Conn, error) {
	// A pool node is an explicit internal hop. It must use the configured node
	// URL as-is; applying the process-wide takeover here would turn a node that
	// equals the original global proxy into a dial back to the pool listener.
	dialer, mode, errBuild := proxyutil.BuildDialerWithoutRuntimeOverride(rawProxyURL)
	if errBuild != nil {
		return nil, errBuild
	}
	if mode != proxyutil.ModeProxy || dialer == nil {
		return nil, fmt.Errorf("proxy node does not resolve to a concrete proxy")
	}
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		return contextDialer.DialContext(ctx, "tcp", target)
	}
	type result struct {
		conn net.Conn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, errDial := dialer.Dial("tcp", target)
		done <- result{conn: conn, err: errDial}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case outcome := <-done:
		return outcome.conn, outcome.err
	}
}

func decodeProbeBody(body []byte, result *ProbeResult) {
	if result == nil || len(body) == 0 {
		return
	}
	var payload map[string]any
	if errUnmarshal := json.Unmarshal(body, &payload); errUnmarshal != nil {
		return
	}
	result.ExitIP = firstString(payload, "ip", "query")
	result.Country = firstString(payload, "country", "country_name")
	result.Region = firstString(payload, "region", "regionName")
	result.City = firstString(payload, "city")
	result.ISP = firstString(payload, "connection.isp", "isp")
	parts := make([]string, 0, 3)
	for _, value := range []string{result.Country, result.Region, result.City} {
		if value != "" {
			parts = append(parts, value)
		}
	}
	result.Location = strings.Join(parts, " · ")
}

func firstString(payload map[string]any, paths ...string) string {
	for _, path := range paths {
		current := any(payload)
		for _, part := range strings.Split(path, ".") {
			mapping, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = mapping[part]
		}
		if value, ok := current.(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

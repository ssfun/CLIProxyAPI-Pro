package pool

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
)

type HealthState string

const (
	HealthUnknown  HealthState = "unknown"
	HealthHealthy  HealthState = "healthy"
	HealthDegraded HealthState = "degraded"
	HealthIsolated HealthState = "isolated"
	HealthDisabled HealthState = "disabled"
)

type Node struct {
	config proxyconfig.NodeConfig

	mu                  sync.RWMutex
	state               HealthState
	isolationUntil      time.Time
	lastCheck           time.Time
	lastSuccess         time.Time
	lastFailure         time.Time
	lastError           string
	lastLatency         time.Duration
	lastExitIP          string
	lastLocation        string
	consecutiveFailures int
	currentWeight       int64

	activeTunnels   atomic.Int64
	totalConnects   atomic.Uint64
	successConnects atomic.Uint64
	failedConnects  atomic.Uint64
}

type NodeSnapshot struct {
	ID                  string      `json:"id"`
	Label               string      `json:"label"`
	DisplayURL          string      `json:"display_url"`
	Enabled             bool        `json:"enabled"`
	Weight              int64       `json:"weight"`
	Order               int         `json:"order"`
	State               HealthState `json:"state"`
	IsolationUntil      time.Time   `json:"isolation_until,omitempty"`
	LastCheck           time.Time   `json:"last_check,omitempty"`
	LastSuccess         time.Time   `json:"last_success,omitempty"`
	LastFailure         time.Time   `json:"last_failure,omitempty"`
	LastError           string      `json:"last_error,omitempty"`
	LatencyMS           int64       `json:"latency_ms"`
	ExitIP              string      `json:"exit_ip,omitempty"`
	Location            string      `json:"location,omitempty"`
	ConsecutiveFailures int         `json:"consecutive_failures"`
	ActiveTunnels       int64       `json:"active_tunnels"`
	TotalConnects       uint64      `json:"total_connects"`
	SuccessConnects     uint64      `json:"success_connects"`
	FailedConnects      uint64      `json:"failed_connects"`
}

func newNode(cfg proxyconfig.NodeConfig) *Node {
	state := HealthUnknown
	if !cfg.Enabled {
		state = HealthDisabled
	}
	return &Node{config: cfg, state: state}
}

func (n *Node) ID() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.config.ID
}

func (n *Node) URL() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.config.URL
}

func (n *Node) Enabled() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.config.Enabled
}

func (n *Node) Weight() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.config.Weight
}

func (n *Node) hasURL(rawURL string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.config.URL == rawURL
}

func (n *Node) ActiveTunnels() int64 { return n.activeTunnels.Load() }

func (n *Node) eligible(now time.Time) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.refreshExpiredIsolationLocked(now)
	return n.config.Enabled && n.state != HealthIsolated
}

func (n *Node) checkable(now time.Time) bool {
	if n == nil {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.refreshExpiredIsolationLocked(now)
	return n.config.Enabled && n.state != HealthIsolated
}

func (n *Node) refreshExpiredIsolationLocked(now time.Time) {
	if n.state == HealthIsolated && !now.Before(n.isolationUntil) {
		n.state = HealthUnknown
		n.isolationUntil = time.Time{}
	}
}

func (n *Node) MarkAttempt() {
	n.totalConnects.Add(1)
}

func (n *Node) MarkSuccess(latency time.Duration) {
	n.successConnects.Add(1)
	n.updateSuccess(latency)
}

func (n *Node) updateSuccess(latency time.Duration) {
	n.mu.Lock()
	if n.config.Enabled {
		n.state = HealthHealthy
	} else {
		n.state = HealthDisabled
	}
	n.isolationUntil = time.Time{}
	n.lastSuccess = time.Now().UTC()
	n.lastError = ""
	n.lastLatency = latency
	n.consecutiveFailures = 0
	n.mu.Unlock()
}

func (n *Node) MarkFailure(err error, threshold int, isolationDuration time.Duration) {
	n.failedConnects.Add(1)
	n.updateFailure(err, threshold, isolationDuration)
}

func (n *Node) updateFailure(err error, threshold int, isolationDuration time.Duration) {
	n.mu.Lock()
	n.lastFailure = time.Now().UTC()
	if err != nil {
		n.lastError = err.Error()
	}
	n.consecutiveFailures++
	if !n.config.Enabled {
		n.state = HealthDisabled
		n.isolationUntil = time.Time{}
	} else if n.consecutiveFailures >= threshold {
		n.state = HealthIsolated
		n.isolationUntil = time.Now().Add(isolationDuration).UTC()
	} else {
		n.state = HealthDegraded
	}
	n.mu.Unlock()
}

func (n *Node) MarkCheck(latency time.Duration, err error, threshold int, isolationDuration time.Duration) {
	n.mu.Lock()
	n.lastCheck = time.Now().UTC()
	n.mu.Unlock()
	if err != nil {
		n.updateFailure(err, threshold, isolationDuration)
		return
	}
	n.updateSuccess(latency)
}

// MarkProbe updates health state without changing tunnel connection counters.
// Manual probes and background checks are observations, not SOCKS connection
// attempts, so they must not alter successConnects or failedConnects.
func (n *Node) MarkProbe(latency time.Duration, err error, threshold int, isolationDuration time.Duration) {
	n.MarkCheck(latency, err, threshold, isolationDuration)
}

func (n *Node) SetProbeResult(ip, location string, latency time.Duration) {
	n.mu.Lock()
	n.lastExitIP = strings.TrimSpace(ip)
	n.lastLocation = strings.TrimSpace(location)
	n.lastCheck = time.Now().UTC()
	n.lastLatency = latency
	n.mu.Unlock()
}

func (n *Node) Acquire() {
	n.activeTunnels.Add(1)
}

func (n *Node) Release() {
	for {
		current := n.activeTunnels.Load()
		if current <= 0 || n.activeTunnels.CompareAndSwap(current, current-1) {
			return
		}
	}
}

func (n *Node) ResetStats() {
	n.totalConnects.Store(0)
	n.successConnects.Store(0)
	n.failedConnects.Store(0)
	n.mu.Lock()
	n.lastSuccess = time.Time{}
	n.lastFailure = time.Time{}
	n.lastError = ""
	n.lastLatency = 0
	n.lastExitIP = ""
	n.lastLocation = ""
	n.consecutiveFailures = 0
	n.currentWeight = 0
	if n.config.Enabled {
		n.state = HealthUnknown
	} else {
		n.state = HealthDisabled
	}
	n.isolationUntil = time.Time{}
	n.mu.Unlock()
}

// Recover clears the transient health penalty for a node without discarding
// its connection counters. This is intentionally separate from ResetStats so
// an operator can put a repaired proxy back into rotation while preserving the
// evidence that led to its isolation.
func (n *Node) Recover() {
	n.mu.Lock()
	n.consecutiveFailures = 0
	n.isolationUntil = time.Time{}
	n.lastError = ""
	if n.config.Enabled {
		n.state = HealthUnknown
	} else {
		n.state = HealthDisabled
	}
	n.mu.Unlock()
}

func (n *Node) Snapshot() NodeSnapshot {
	n.mu.Lock()
	n.refreshExpiredIsolationLocked(time.Now())
	totalConnects := n.totalConnects.Load()
	successConnects := n.successConnects.Load()
	failedConnects := n.failedConnects.Load()
	// A connection that started before ResetStats can finish after the counters
	// were cleared. Keep the exported snapshot internally consistent so
	// operators never see more completed connections than total attempts.
	if completedConnects := successConnects + failedConnects; completedConnects > totalConnects {
		totalConnects = completedConnects
	}
	snapshot := NodeSnapshot{
		ID:                  n.config.ID,
		Label:               n.config.Label,
		DisplayURL:          RedactURL(n.config.URL),
		Enabled:             n.config.Enabled,
		Weight:              n.config.Weight,
		Order:               n.config.Order,
		State:               n.state,
		IsolationUntil:      n.isolationUntil,
		LastCheck:           n.lastCheck,
		LastSuccess:         n.lastSuccess,
		LastFailure:         n.lastFailure,
		LastError:           n.lastError,
		LatencyMS:           n.lastLatency.Milliseconds(),
		ExitIP:              n.lastExitIP,
		Location:            n.lastLocation,
		ConsecutiveFailures: n.consecutiveFailures,
		ActiveTunnels:       n.activeTunnels.Load(),
		TotalConnects:       totalConnects,
		SuccessConnects:     successConnects,
		FailedConnects:      failedConnects,
	}
	n.mu.Unlock()
	return snapshot
}

type Pool struct {
	mu       sync.RWMutex
	nodes    []*Node
	byID     map[string]*Node
	strategy string
	rr       atomic.Uint64
	selectMu sync.Mutex
}

func New(cfg proxyconfig.Config) *Pool {
	p := &Pool{}
	p.Reconfigure(cfg)
	return p
}

func (p *Pool) Reconfigure(cfg proxyconfig.Config) {
	p.mu.Lock()
	oldByID := p.byID
	nodes := make([]*Node, 0, len(cfg.Nodes))
	byID := make(map[string]*Node, len(cfg.Nodes))
	for _, item := range cfg.Nodes {
		node := oldByID[item.ID]
		if node == nil || !node.hasURL(item.URL) {
			node = newNode(item)
		} else {
			node.mu.Lock()
			node.config = item
			if !item.Enabled {
				node.state = HealthDisabled
			} else if node.state == HealthDisabled {
				node.state = HealthUnknown
			}
			node.mu.Unlock()
		}
		nodes = append(nodes, node)
		byID[item.ID] = node
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].config.Order == nodes[j].config.Order {
			return nodes[i].config.ID < nodes[j].config.ID
		}
		return nodes[i].config.Order < nodes[j].config.Order
	})
	p.nodes = nodes
	p.byID = byID
	p.strategy = cfg.Strategy
	p.mu.Unlock()
}

func (p *Pool) Node(id string) *Node {
	p.mu.RLock()
	node := p.byID[strings.TrimSpace(id)]
	p.mu.RUnlock()
	return node
}

func (p *Pool) NodesForCheck(now time.Time) []*Node {
	p.mu.RLock()
	nodes := make([]*Node, 0, len(p.nodes))
	for _, node := range p.nodes {
		if node.checkable(now) {
			nodes = append(nodes, node)
		}
	}
	p.mu.RUnlock()
	return nodes
}

// EligibleCount reports how many nodes can participate in selection at now.
// Keep this based on Node.eligible so status and Select cannot drift apart.
func (p *Pool) EligibleCount(now time.Time) int {
	p.mu.RLock()
	count := 0
	for _, node := range p.nodes {
		if node.eligible(now) {
			count++
		}
	}
	p.mu.RUnlock()
	return count
}

func (p *Pool) Select(excluded map[string]struct{}) *Node {
	p.mu.RLock()
	now := time.Now()
	eligible := make([]*Node, 0, len(p.nodes))
	for _, node := range p.nodes {
		if _, skip := excluded[node.ID()]; skip || !node.eligible(now) {
			continue
		}
		eligible = append(eligible, node)
	}
	strategy := p.strategy
	p.mu.RUnlock()
	if len(eligible) == 0 {
		return nil
	}

	switch strategy {
	case "weighted":
		return p.selectWeighted(eligible)
	case "least-connections":
		return selectLeastConnections(eligible)
	default:
		index := p.rr.Add(1) - 1
		return eligible[index%uint64(len(eligible))]
	}
}

func (p *Pool) selectWeighted(nodes []*Node) *Node {
	p.selectMu.Lock()
	defer p.selectMu.Unlock()
	var best *Node
	var bestWeight int64
	var total int64
	for _, node := range nodes {
		weight := node.Weight()
		if weight <= 0 {
			weight = 1
		}
		total += weight
		node.mu.Lock()
		node.currentWeight += weight
		currentWeight := node.currentWeight
		if best == nil || currentWeight > bestWeight {
			best = node
			bestWeight = currentWeight
		}
		node.mu.Unlock()
	}
	if best != nil {
		best.mu.Lock()
		best.currentWeight -= total
		best.mu.Unlock()
	}
	return best
}

func selectLeastConnections(nodes []*Node) *Node {
	best := nodes[0]
	for _, node := range nodes[1:] {
		if node.ActiveTunnels() < best.ActiveTunnels() {
			best = node
		}
	}
	return best
}

func (p *Pool) Snapshots() []NodeSnapshot {
	p.mu.RLock()
	nodes := append([]*Node(nil), p.nodes...)
	p.mu.RUnlock()
	result := make([]NodeSnapshot, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, node.Snapshot())
	}
	return result
}

func (p *Pool) ResetStats() {
	p.mu.RLock()
	nodes := append([]*Node(nil), p.nodes...)
	p.mu.RUnlock()
	for _, node := range nodes {
		node.ResetStats()
	}
	p.rr.Store(0)
}

func (p *Pool) Recover(id string) bool {
	node := p.Node(id)
	if node == nil {
		return false
	}
	node.Recover()
	return true
}

func RedactURL(raw string) string {
	value := strings.TrimSpace(raw)
	separator := strings.Index(value, "://")
	if separator < 0 {
		return "***"
	}
	prefix := value[:separator+3]
	rest := value[separator+3:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return value
	}
	userinfo := rest[:at]
	host := rest[at+1:]
	username := userinfo
	if colon := strings.Index(userinfo, ":"); colon >= 0 {
		username = userinfo[:colon]
	}
	if username == "" {
		return prefix + "***@" + host
	}
	return prefix + username + ":***@" + host
}

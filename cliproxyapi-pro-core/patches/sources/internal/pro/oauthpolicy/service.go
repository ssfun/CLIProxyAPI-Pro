package oauthpolicy

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	modelconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
	modelengine "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/policy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/settings"
)

type Status struct {
	Enabled        bool   `json:"enabled"`
	Refreshing     bool   `json:"refreshing"`
	CacheTTL       string `json:"cacheTTL"`
	MaxStale       string `json:"maxStale"`
	ResolveTimeout string `json:"resolveTimeout"`
	Providers      int    `json:"providers"`
	LastError      string `json:"lastError,omitempty"`
}

type PlanSnapshot = settings.PlanSnapshot

type authVersion struct {
	RegistrationEpoch uint64
	Generation        uint64
	Fingerprint       string
}

// PlanSnapshotReader is the narrow persistence port used by account policy to
// consume provider quota/inspection evidence without depending on SQLite.
// Multiple persistence rows can exist for the same auth. The adapter resolves
// them with the auth-card preference contract before returning one snapshot.
type PlanSnapshotReader interface {
	GetPlanSnapshot(context.Context, string, string, string) (settings.PlanSnapshot, bool, error)
}

// Service owns OAuth account-plan model filtering and its persisted policy.
type Service struct {
	mu                   sync.RWMutex
	store                settings.Store
	planStore            PlanSnapshotReader
	config               modelconfig.Config
	engine               *modelengine.Engine
	revision             uint64
	planEvidenceRevision uint64
	authEpochs           map[string]uint64
	authVersions         map[string]authVersion
	effective            map[string]modelengine.EffectivePolicy
	decisions            map[string]modelengine.Result
	onChange             func(context.Context)
	unregister           func()
	changeCtx            context.Context
	changeStop           context.CancelFunc
	changeRun            bool
	changeNext           bool
	planReset            bool
	closed               bool
}

func New(ctx context.Context, store settings.Store) (*Service, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return nil, fmt.Errorf("account policy settings store is required")
	}
	cfg, loadErr := loadConfig(ctx, store)
	if loadErr != nil {
		return nil, fmt.Errorf("load account policy config: %w", loadErr)
	}
	changeCtx, changeStop := context.WithCancel(context.Background())
	engine := modelengine.New()
	engine.ApplyConfig(cfg)
	service := &Service{
		store: store, config: cfg, engine: engine,
		revision: 1, planEvidenceRevision: 1,
		authEpochs: make(map[string]uint64), authVersions: make(map[string]authVersion),
		effective: make(map[string]modelengine.EffectivePolicy), decisions: make(map[string]modelengine.Result),
		changeCtx: changeCtx, changeStop: changeStop,
	}
	service.planStore, _ = store.(PlanSnapshotReader)
	service.unregister = store.Subscribe(settings.NamespaceOAuthPolicy, service.applyImportedSetting)
	return service, nil
}

func loadConfig(ctx context.Context, store settings.Store) (modelconfig.Config, error) {
	cfg, _ := modelconfig.Parse(nil)
	item, found, err := store.Get(ctx, settings.NamespaceOAuthPolicy)
	if err != nil {
		return cfg, err
	}
	migrate := !found
	if migrate {
		item, found, err = store.Get(ctx, settings.LegacyNamespaceOAuthModelPolicy)
		if err != nil || !found {
			return cfg, err
		}
	}
	cfg, err = parseSetting(item)
	if err != nil {
		return cfg, err
	}
	if migrate {
		item.Namespace = settings.NamespaceOAuthPolicy
		if err = store.Put(ctx, item); err != nil {
			return cfg, err
		}
		verified, verifiedFound, errVerify := store.Get(ctx, settings.NamespaceOAuthPolicy)
		if errVerify != nil {
			return cfg, errVerify
		}
		if !verifiedFound {
			return cfg, fmt.Errorf("verify migrated OAuth policy setting")
		}
		cfg, err = parseSetting(verified)
		if err != nil {
			return cfg, err
		}
	}
	// Only remove the legacy copy after the selected configuration is usable.
	if err = store.Delete(ctx, settings.LegacyNamespaceOAuthModelPolicy); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func parseSetting(item settings.Item) (modelconfig.Config, error) {
	if item.SchemaVersion != settings.SchemaVersionOne {
		return modelconfig.Config{}, fmt.Errorf("unsupported OAuth account policy schema version %d", item.SchemaVersion)
	}
	return modelconfig.Parse(item.Settings)
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	unregister := s.unregister
	s.unregister = nil
	s.onChange = nil
	changeStop := s.changeStop
	s.changeStop = nil
	s.mu.Unlock()
	if changeStop != nil {
		changeStop()
	}
	if unregister != nil {
		unregister()
	}
}

func (s *Service) SetChangeHandler(handler func(context.Context)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onChange = handler
	s.mu.Unlock()
}

func (s *Service) Config() modelconfig.Config {
	if s == nil {
		cfg, _ := modelconfig.Parse(nil)
		return cfg
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Service) UpdateConfig(ctx context.Context, cfg modelconfig.Config) error {
	if s == nil {
		return fmt.Errorf("account policy service is unavailable")
	}
	raw, err := modelconfig.Marshal(cfg)
	if err != nil {
		return err
	}
	normalized, err := modelconfig.Parse(raw)
	if err != nil {
		return err
	}
	operation := func(ctx context.Context, store settings.Store) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return fmt.Errorf("account policy service is closed")
		}
		if err := store.Put(ctx, settings.Item{
			Namespace: settings.NamespaceOAuthPolicy, SchemaVersion: settings.SchemaVersionOne, Settings: raw,
		}); err != nil {
			return err
		}
		s.config = normalized
		s.resetEngineLocked()
		return nil
	}
	if coordinator, ok := s.store.(settings.WriteCoordinator); ok {
		if err := coordinator.ExecuteWrite(ctx, operation); err != nil {
			return err
		}
	} else if err := operation(ctx, s.store); err != nil {
		return err
	}
	s.queueChange()
	return nil
}

func (s *Service) applyImportedSetting(_ context.Context, item settings.Item) error {
	cfg, err := parseSetting(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("account policy service is closed")
	}
	s.config = cfg
	s.resetEngineLocked()
	s.mu.Unlock()
	s.queueChange()
	return nil
}

// queueChange applies saved policy changes outside the management request. A
// refresh may resolve remote account plans, so waiting here would make a
// successful SQLite write look like a failed save when the client times out.
// Concurrent updates are coalesced into at most one additional refresh.
func (s *Service) queueChange() {
	if s == nil {
		return
	}
	s.mu.Lock()
	ctx, start := s.queueChangeLocked()
	s.mu.Unlock()
	if start {
		go s.runChanges(ctx)
	}
}

func (s *Service) queueChangeLocked() (context.Context, bool) {
	if s.closed || s.onChange == nil || s.changeCtx == nil {
		return nil, false
	}
	s.changeNext = true
	if s.changeRun {
		return nil, false
	}
	s.changeRun = true
	return s.changeCtx, true
}

func (s *Service) runChanges(ctx context.Context) {
	for {
		s.mu.Lock()
		if s.closed || !s.changeNext || ctx.Err() != nil {
			s.changeRun = false
			s.mu.Unlock()
			return
		}
		s.changeNext = false
		if s.planReset {
			s.resetEngineLocked()
			s.planReset = false
		}
		handler := s.onChange
		s.mu.Unlock()
		if handler != nil {
			handler(ctx)
		}
	}
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{LastError: "account policy service is unavailable"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{
		Enabled: s.config.Enabled, Refreshing: s.changeRun || s.changeNext, CacheTTL: s.config.CacheTTL.String(),
		MaxStale: s.config.MaxStale.String(), ResolveTimeout: s.config.ResolveTimeout.String(),
		Providers: len(s.config.Providers),
	}
}

func (s *Service) Filter(ctx context.Context, input modelengine.Input) modelengine.Result {
	if s == nil {
		return modelengine.Result{}
	}
	loadPlanSnapshot := s.planStore != nil && len(input.QuotaSnapshotJSON) == 0
	incomingVersion := authVersion{
		RegistrationEpoch: input.AuthRegistrationEpoch,
		Generation:        input.AuthGeneration,
		Fingerprint:       strings.TrimSpace(input.CredentialFingerprint),
	}
	for {
		s.mu.Lock()
		currentVersion, versionKnown := s.authVersions[input.AuthID]
		if versionKnown && authVersionOlder(incomingVersion, currentVersion) {
			s.mu.Unlock()
			return modelengine.Result{}
		}
		if !versionKnown || currentVersion != incomingVersion {
			if versionKnown {
				s.authEpochs[input.AuthID]++
				if s.engine != nil {
					s.engine.ForgetAuth(input.AuthID)
				}
				delete(s.effective, input.AuthID)
				delete(s.decisions, input.AuthID)
			}
			s.authVersions[input.AuthID] = incomingVersion
		}
		engine := s.engine
		closed := s.closed
		revision := s.revision
		planEvidenceRevision := s.planEvidenceRevision
		authEpoch := s.authEpochs[input.AuthID]
		s.mu.Unlock()
		if closed || engine == nil {
			return modelengine.Result{}
		}
		attemptInput := input
		if loadPlanSnapshot {
			attemptInput.QuotaSnapshotJSON = nil
			attemptInput.QuotaObservedAtMS = 0
			attemptInput.QuotaSnapshotError = ""
			snapshot, found, err := s.planStore.GetPlanSnapshot(
				ctx, attemptInput.AuthProvider, attemptInput.FileName, attemptInput.AuthIndex,
			)
			if found {
				attemptInput.QuotaSnapshotJSON = append([]byte(nil), snapshot.Data...)
				attemptInput.QuotaObservedAtMS = snapshot.ObservedAtMS
			}
			if err != nil {
				attemptInput.QuotaSnapshotError = err.Error()
			}
		}
		result := engine.Filter(ctx, attemptInput)
		s.mu.Lock()
		if s.closed || s.authEpochs[input.AuthID] != authEpoch {
			s.mu.Unlock()
			return modelengine.Result{}
		}
		if s.revision != revision || s.planEvidenceRevision != planEvidenceRevision {
			s.mu.Unlock()
			if ctx != nil && ctx.Err() != nil {
				return modelengine.Result{}
			}
			continue
		}
		if result.Handled {
			s.decisions[input.AuthID] = result
			s.effective[input.AuthID] = modelengine.EffectivePolicy{
				AuthID: input.AuthID, Provider: input.AuthProvider,
				PlanKey: result.Annotations["plan_key"], PlanSource: result.Annotations["plan_source"],
				MatchedRule: result.Annotations["matched_rule"], PlanError: result.Annotations["plan_error"],
				Prefix: effectivePrefix(attemptInput, result), Priority: effectivePriority(attemptInput, result), Weight: effectiveWeight(attemptInput, result),
				ExcludedCount: len(result.ExcludedModelIDs),
			}
		} else {
			delete(s.effective, input.AuthID)
			delete(s.decisions, input.AuthID)
		}
		s.mu.Unlock()
		return result
	}
}

// RefreshPlanDetection invalidates runtime decisions and schedules a
// generation-safe re-registration of every current auth account.
func (s *Service) RefreshPlanDetection() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.planEvidenceRevision++
	// A running refresh consumes one coalesced follow-up pass. Defer the engine
	// reset to that pass so repeated quota writes do not restart every in-flight
	// provider lookup, while a write that arrives during a pass still invalidates
	// the cache before the follow-up handler runs.
	if s.onChange == nil || s.changeCtx == nil {
		s.resetEngineLocked()
		s.mu.Unlock()
		return
	}
	s.planReset = true
	changeCtx, start := s.queueChangeLocked()
	s.mu.Unlock()
	if start {
		go s.runChanges(changeCtx)
	}
}

func (s *Service) resetEngineLocked() {
	engine := modelengine.New()
	engine.ApplyConfig(s.config)
	s.engine = engine
	s.revision++
	s.effective = make(map[string]modelengine.EffectivePolicy)
	s.decisions = make(map[string]modelengine.Result)
}

// ForgetAuth removes runtime-only policy state for an authentication account.
// Advancing the account epoch also prevents a provider lookup that started
// before deletion from restoring the removed decision after it completes.
func (s *Service) ForgetAuth(authID string) {
	if s == nil || strings.TrimSpace(authID) == "" {
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.authEpochs[authID]++
		if s.engine != nil {
			s.engine.ForgetAuth(authID)
		}
		delete(s.effective, authID)
		delete(s.decisions, authID)
		delete(s.authVersions, authID)
	}
	s.mu.Unlock()
}

func authVersionOlder(incoming, current authVersion) bool {
	if incoming.RegistrationEpoch != current.RegistrationEpoch {
		return incoming.RegistrationEpoch < current.RegistrationEpoch
	}
	return incoming.Generation < current.Generation
}

func effectivePrefix(input modelengine.Input, result modelengine.Result) string {
	if result.Prefix != nil {
		return *result.Prefix
	}
	return strings.TrimSpace(input.AuthPrefix)
}

func effectivePriority(input modelengine.Input, result modelengine.Result) int {
	if result.Priority != nil {
		return *result.Priority
	}
	value, _ := strconv.Atoi(strings.TrimSpace(input.Attributes["priority"]))
	return value
}

func effectiveWeight(input modelengine.Input, result modelengine.Result) int64 {
	if result.Weight != nil {
		return *result.Weight
	}
	value := int64(1)
	if parsed, err := strconv.ParseInt(strings.TrimSpace(input.Attributes["weight"]), 10, 64); err == nil {
		value = parsed
	}
	return value
}

func (s *Service) EffectivePolicies() []modelengine.EffectivePolicy {
	if s == nil {
		return []modelengine.EffectivePolicy{}
	}
	s.mu.RLock()
	out := make([]modelengine.EffectivePolicy, 0, len(s.effective))
	for _, policy := range s.effective {
		out = append(out, policy)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider == out[j].Provider {
			return out[i].AuthID < out[j].AuthID
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}

func (s *Service) EffectivePolicy(authID string) (modelengine.Result, bool) {
	if s == nil || authID == "" {
		return modelengine.Result{}, false
	}
	s.mu.RLock()
	policy, found := s.decisions[authID]
	s.mu.RUnlock()
	return policy, found
}

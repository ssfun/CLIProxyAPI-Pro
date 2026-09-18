package proxypool

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
	proxyengine "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/engine"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/settings"
)

// TransportOverride applies the selected pool listener to the host's final
// built-in provider transport path.
type TransportOverride interface {
	Set(baseProxyURL, targetProxyURL string)
	Clear()
}

// Service owns proxy-pool configuration, runtime health and takeover state.
type Service struct {
	mu           sync.RWMutex
	store        settings.Store
	override     TransportOverride
	config       proxyconfig.Config
	engine       *proxyengine.Engine
	baseProxyURL string
	configErr    string
	unregister   func()
	closed       bool
}

func New(ctx context.Context, store settings.Store, override TransportOverride, baseProxyURL string) (*Service, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return nil, fmt.Errorf("proxy pool settings store is required")
	}
	cfg, loadErr := loadConfig(ctx, store)
	service := &Service{
		store: store, override: override, config: cfg, engine: proxyengine.New(),
		baseProxyURL: strings.TrimSpace(baseProxyURL),
	}
	if loadErr != nil {
		service.configErr = loadErr.Error()
	}
	if cfg.Enabled && loadErr == nil {
		if err := service.engine.ApplyConfig(cfg); err != nil {
			service.configErr = err.Error()
		}
	}
	service.refreshOverrideLocked()
	service.unregister = store.Subscribe(settings.NamespaceProxyPool, service.applyImportedSetting)
	return service, nil
}

func loadConfig(ctx context.Context, store settings.Store) (proxyconfig.Config, error) {
	cfg := proxyconfig.Default()
	item, found, err := store.Get(ctx, settings.NamespaceProxyPool)
	if err != nil || !found {
		return cfg, err
	}
	if item.SchemaVersion != settings.SchemaVersionOne {
		return cfg, fmt.Errorf("unsupported proxy pool schema version %d", item.SchemaVersion)
	}
	return proxyconfig.Parse(item.Settings)
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
	engine := s.engine
	s.engine = nil
	if s.override != nil {
		s.override.Clear()
	}
	s.mu.Unlock()
	if unregister != nil {
		unregister()
	}
	if engine != nil {
		engine.Close()
	}
}

func (s *Service) SetBaseProxyURL(value string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.baseProxyURL = strings.TrimSpace(value)
	s.refreshOverrideLocked()
	s.mu.Unlock()
}

func (s *Service) BaseProxyURL() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.baseProxyURL
}

func (s *Service) Config() proxyconfig.Config {
	if s == nil {
		return proxyconfig.Default()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Service) UpdateConfig(ctx context.Context, cfg proxyconfig.Config) error {
	if s == nil {
		return fmt.Errorf("proxy pool service is unavailable")
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return err
	}
	raw, err := proxyconfig.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.executeWrite(ctx, func(ctx context.Context, store settings.Store) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return fmt.Errorf("proxy pool service is closed")
		}
		oldConfig := s.config
		oldRaw, _ := proxyconfig.Marshal(oldConfig)
		if err := store.Put(ctx, settings.Item{
			Namespace: settings.NamespaceProxyPool, SchemaVersion: settings.SchemaVersionOne, Settings: raw,
		}); err != nil {
			return err
		}
		if cfg.Enabled {
			if s.engine == nil {
				s.engine = proxyengine.New()
			}
			if err := s.engine.ApplyConfig(cfg); err != nil {
				rollbackErr := store.Put(context.Background(), settings.Item{
					Namespace: settings.NamespaceProxyPool, SchemaVersion: settings.SchemaVersionOne, Settings: oldRaw,
				})
				if rollbackErr != nil {
					s.configErr = fmt.Sprintf("%v (persisted configuration rollback failed: %v)", err, rollbackErr)
					return fmt.Errorf("apply proxy pool config: %w (persisted configuration rollback failed: %v)", err, rollbackErr)
				}
				s.configErr = err.Error()
				return err
			}
		} else {
			oldEngine := s.engine
			if s.override != nil {
				s.override.Clear()
			}
			if oldEngine != nil {
				oldEngine.StopAccepting()
			}
			s.engine = proxyengine.New()
			if oldEngine != nil {
				go oldEngine.Close()
			}
		}
		s.config = cfg
		s.configErr = ""
		s.refreshOverrideLocked()
		return nil
	})
}

func (s *Service) executeWrite(
	ctx context.Context,
	operation func(context.Context, settings.Store) error,
) error {
	if coordinator, ok := s.store.(settings.WriteCoordinator); ok {
		return coordinator.ExecuteWrite(ctx, operation)
	}
	return operation(ctx, s.store)
}

func (s *Service) applyImportedSetting(_ context.Context, item settings.Item) error {
	if item.SchemaVersion != settings.SchemaVersionOne {
		return fmt.Errorf("unsupported proxy pool schema version %d", item.SchemaVersion)
	}
	cfg, err := proxyconfig.Parse(item.Settings)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("proxy pool service is closed")
	}
	if cfg.Enabled {
		if s.engine == nil {
			s.engine = proxyengine.New()
		}
		if err := s.engine.ApplyConfig(cfg); err != nil {
			return err
		}
	} else if s.engine != nil {
		oldEngine := s.engine
		if s.override != nil {
			s.override.Clear()
		}
		oldEngine.StopAccepting()
		s.engine = proxyengine.New()
		go oldEngine.Close()
	}
	s.config = cfg
	s.configErr = ""
	s.refreshOverrideLocked()
	return nil
}

func (s *Service) refreshOverrideLocked() {
	if s.override == nil {
		return
	}
	if s.closed || !s.config.Enabled || !s.config.TakeoverEnabled || s.engine == nil || !s.engine.Status().Ready {
		s.override.Clear()
		return
	}
	s.override.Set(s.baseProxyURL, "socks5://"+s.config.Listen)
}

func (s *Service) Status() proxyengine.Status {
	if s == nil {
		return proxyengine.Status{LastError: "proxy pool service is unavailable"}
	}
	s.mu.RLock()
	engine := s.engine
	configErr := s.configErr
	cfg := s.config
	s.mu.RUnlock()
	if engine == nil {
		return proxyengine.Status{LastError: configErr}
	}
	status := engine.Status()
	if status.Listen == "" {
		status.Listen = cfg.Listen
		status.ProxyURL = "socks5://" + cfg.Listen
	}
	if status.Strategy == "" {
		status.Strategy = cfg.Strategy
	}
	if configErr != "" {
		status.LastError = configErr
	}
	return status
}

func (s *Service) Probe(ctx context.Context, nodeID, proxyURL, testURL string) proxyengine.ProbeResult {
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	if engine == nil {
		return proxyengine.ProbeResult{NodeID: nodeID, Error: "proxy pool is not initialized", CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	if strings.TrimSpace(proxyURL) != "" {
		return engine.ProbeDraft(ctx, nodeID, proxyURL, testURL)
	}
	return engine.Probe(ctx, nodeID, testURL)
}

func (s *Service) ProbeAll(ctx context.Context, concurrency int) []proxyengine.ProbeResult {
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	if engine == nil {
		return []proxyengine.ProbeResult{}
	}
	return engine.ProbeAll(ctx, concurrency)
}

func (s *Service) Recover(nodeID string) error {
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	if engine == nil {
		return fmt.Errorf("proxy pool is not initialized")
	}
	return engine.Recover(nodeID)
}

func (s *Service) ResetStats() {
	s.mu.RLock()
	engine := s.engine
	s.mu.RUnlock()
	if engine != nil {
		engine.ResetStats()
	}
}

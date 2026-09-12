// Package app is the static composition root for CLIProxyAPI Pro modules.
package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/host"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy"
	modelconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/config"
	proxyengine "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool/engine"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// App owns the lifecycle and wiring of stateful Pro modules on the proxy
// request path. Process-scoped observability and Management-scoped inspection
// controllers keep their natural host lifecycles and register owner-safe ports
// with the shared backup coordinator.
type App struct {
	proxyPool                 *proxypool.Service
	oauthPolicy               *oauthpolicy.Service
	apiKeyPolicy              *apikeypolicy.Service
	policyBackupUnregister    func()
	policyLifecycleUnregister func()
	policyDomainUnregister    func()
	closeOnce                 sync.Once
}

func New(ctx context.Context, configFilePath, baseProxyURL string) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	migrated, err := migrateLegacySettings(ctx, configFilePath)
	if err != nil {
		return nil, err
	}
	if migrated {
		baseProxyURL = baseProxyURLFromConfigFile(configFilePath, baseProxyURL)
	}
	store := observability.NewSettingsStore()
	policyStore, err := apikeypolicy.OpenStore(observability.LoadConfigForPath(configFilePath).DBPath)
	if err != nil {
		return nil, fmt.Errorf("initialize API key policy store: %w", err)
	}
	apiKeyPolicy, err := apikeypolicy.NewService(policyStore)
	if err != nil {
		_ = policyStore.Close()
		return nil, fmt.Errorf("initialize API key policy module: %w", err)
	}
	apiKeyPolicy.SetCatalogProvider(func() (apikeypolicy.ProfileCatalog, error) {
		modelRegistry := registry.GetGlobalRegistry()
		return apiKeyPolicyCatalogFromAvailableModels(
			modelRegistry.GetAvailableModelInfos(),
			modelRegistry.GetModelProviders,
			home.Current() != nil,
		), nil
	})
	apiKeyPolicy.SetCostEstimator(func(ctx context.Context, usage apikeypolicy.QuotaUsageDelta) (int64, error) {
		cost, err := observability.EstimateUsageCostMicros(ctx, observability.UsageCostInput{
			Provider: usage.Provider, AuthType: usage.AuthType, Model: usage.Model,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			ReasoningTokens: usage.ReasoningTokens, CachedTokens: usage.CachedTokens,
			CacheTokens: usage.CacheTokens, CacheReadTokens: usage.CacheReadTokens,
			CacheWriteTokens: usage.CacheWriteTokens, UncachedInputTokens: usage.UncachedInputTokens,
			AccountingQuality: usage.AccountingQuality, ServiceTier: usage.ServiceTier,
			EffectiveServiceTier: usage.EffectiveServiceTier, Speed: usage.Speed,
			EffectiveSpeed: usage.EffectiveSpeed,
		})
		if errors.Is(err, observability.ErrModelPriceUnavailable) {
			return 0, apikeypolicy.ErrQuotaPriceMissing
		}
		return cost, err
	})
	policyBackupUnregister := probackup.Default.RegisterAPIKeyPolicies(
		func() ([]byte, bool, error) {
			payload, err := apiKeyPolicy.ExportBackup(context.Background())
			return payload, err == nil, err
		},
		func(ctx context.Context, payload []byte) error {
			return apiKeyPolicy.ImportBackup(ctx, payload)
		},
		func(ctx context.Context, payload []byte) (probackup.PolicyBackupPreview, error) {
			return apiKeyPolicy.PreviewBackup(ctx, payload, apiKeyPolicy.ConfiguredHashes())
		},
	)
	policyLifecycleUnregister := probackup.Default.RegisterLifecycle(probackup.Lifecycle{
		Pause:  apiKeyPolicy.PauseQuotaRuntime,
		Resume: apiKeyPolicy.ResumeQuotaRuntime,
	})
	policyDomainUnregister := observability.RegisterDataDomainContributor("api-key-policy", observability.DataDomainContribution{
		InventoryFunc: func(ctx context.Context, _ *observability.Store) observability.DataDomainInventory {
			domain := observability.DataDomainInventory{
				Owner: "api-key-policy", SchemaVersion: 2, BackupIncluded: true,
				RestoreMode: "replace", Sensitivity: "sensitive",
				SecretClasses: []string{"credential_identifiers"}, Available: true,
			}
			payload, err := apiKeyPolicy.ExportBackup(ctx)
			if err != nil {
				domain.Available, domain.Error = false, err.Error()
				return domain
			}
			preview, err := apiKeyPolicy.PreviewBackup(ctx, payload, apiKeyPolicy.ConfiguredHashes())
			if err != nil {
				domain.Available, domain.Error = false, err.Error()
				return domain
			}
			domain.Records = int64(preview.TargetPolicies + preview.TargetProfiles + preview.TargetDisabledKeys + preview.TargetConcurrencyKeys)
			return domain
		},
		BackupRecordTypes: []string{"api_key_policies"},
	})
	proxyPool, err := proxypool.New(ctx, store, host.NewProxyOverride(), baseProxyURL)
	if err != nil {
		policyDomainUnregister()
		policyLifecycleUnregister()
		policyBackupUnregister()
		_ = apiKeyPolicy.Close()
		return nil, fmt.Errorf("initialize proxy pool module: %w", err)
	}
	oauthPolicy, err := oauthpolicy.New(ctx, store)
	if err != nil {
		proxyPool.Close()
		policyDomainUnregister()
		policyLifecycleUnregister()
		policyBackupUnregister()
		_ = apiKeyPolicy.Close()
		return nil, fmt.Errorf("initialize account policy module: %w", err)
	}
	return &App{proxyPool: proxyPool, oauthPolicy: oauthPolicy, apiKeyPolicy: apiKeyPolicy, policyBackupUnregister: policyBackupUnregister, policyLifecycleUnregister: policyLifecycleUnregister, policyDomainUnregister: policyDomainUnregister}, nil
}

func apiKeyPolicyCatalogFromAvailableModels(
	models []*registry.ModelInfo,
	providersForModel func(string) []string,
	includeHome bool,
) apikeypolicy.ProfileCatalog {
	modelIDs := make([]string, 0, len(models))
	providers := make([]string, 0, len(models))
	modelProviders := make(map[string][]string, len(models))
	if includeHome {
		// Home is a synthetic request provider backed by the active control-plane
		// client, not a registry client. Empty model selections still need to be
		// able to express "allow every Home model".
		providers = append(providers, "home")
	}
	for _, model := range models {
		if model == nil || model.ID == "" {
			continue
		}
		modelIDs = append(modelIDs, model.ID)
		availableProviders := []string(nil)
		if providersForModel != nil {
			availableProviders = append(availableProviders, providersForModel(model.ID)...)
		}
		if includeHome {
			// Home serves the same public model namespace dynamically. Linking the
			// current registry catalog to Home lets strict clients save a Home-only
			// Profile without weakening the runtime provider check.
			availableProviders = append(availableProviders, "home")
		}
		if providersForModel != nil || includeHome {
			providers = append(providers, availableProviders...)
			modelProviders[model.ID] = availableProviders
		}
	}
	return apikeypolicy.NewProfileCatalog(providers, modelIDs, modelProviders)
}

func (a *App) Close() {
	if a == nil {
		return
	}
	a.closeOnce.Do(func() {
		if a.policyDomainUnregister != nil {
			a.policyDomainUnregister()
		}
		if a.policyBackupUnregister != nil {
			a.policyBackupUnregister()
		}
		if a.policyLifecycleUnregister != nil {
			a.policyLifecycleUnregister()
		}
		if a.apiKeyPolicy != nil {
			_ = a.apiKeyPolicy.Close()
		}
		if a.oauthPolicy != nil {
			a.oauthPolicy.Close()
		}
		if a.proxyPool != nil {
			a.proxyPool.Close()
		}
	})
}

func (a *App) APIKeyPolicy() *apikeypolicy.Service {
	if a == nil {
		return nil
	}
	return a.apiKeyPolicy
}

func (a *App) ProxyPool() *proxypool.Service {
	if a == nil {
		return nil
	}
	return a.proxyPool
}

func (a *App) OAuthPolicy() *oauthpolicy.Service {
	if a == nil {
		return nil
	}
	return a.oauthPolicy
}

func (a *App) SetBaseProxyURL(value string) {
	if a != nil && a.proxyPool != nil {
		a.proxyPool.SetBaseProxyURL(value)
	}
}

func (a *App) BaseProxyURL() string {
	if a == nil || a.proxyPool == nil {
		return ""
	}
	return a.proxyPool.BaseProxyURL()
}

func (a *App) SetOAuthPolicyChangeHandler(handler func(context.Context)) {
	if a != nil && a.oauthPolicy != nil {
		a.oauthPolicy.SetChangeHandler(handler)
	}
}

func (a *App) ProxyConfig() proxyconfig.Config {
	if a == nil || a.proxyPool == nil {
		return proxyconfig.Default()
	}
	return a.proxyPool.Config()
}

func (a *App) UpdateProxyConfig(ctx context.Context, cfg proxyconfig.Config) error {
	if a == nil || a.proxyPool == nil {
		return fmt.Errorf("proxy pool module is unavailable")
	}
	return a.proxyPool.UpdateConfig(ctx, cfg)
}

func (a *App) ProxyStatus() proxyengine.Status {
	if a == nil || a.proxyPool == nil {
		return proxyengine.Status{LastError: "proxy pool module is unavailable"}
	}
	return a.proxyPool.Status()
}

func (a *App) ProbeProxy(ctx context.Context, nodeID, proxyURL, testURL string) proxyengine.ProbeResult {
	if a == nil || a.proxyPool == nil {
		return proxyengine.ProbeResult{NodeID: nodeID, Error: "proxy pool module is unavailable"}
	}
	return a.proxyPool.Probe(ctx, nodeID, proxyURL, testURL)
}

func (a *App) ProbeAllProxies(ctx context.Context, concurrency int) []proxyengine.ProbeResult {
	if a == nil || a.proxyPool == nil {
		return []proxyengine.ProbeResult{}
	}
	return a.proxyPool.ProbeAll(ctx, concurrency)
}

func (a *App) RecoverProxy(nodeID string) error {
	if a == nil || a.proxyPool == nil {
		return fmt.Errorf("proxy pool module is unavailable")
	}
	return a.proxyPool.Recover(nodeID)
}

func (a *App) ResetProxyStats() {
	if a != nil && a.proxyPool != nil {
		a.proxyPool.ResetStats()
	}
}

func (a *App) OAuthConfig() modelconfig.Config {
	if a == nil || a.oauthPolicy == nil {
		cfg, _ := modelconfig.Parse(nil)
		return cfg
	}
	return a.oauthPolicy.Config()
}

func (a *App) UpdateOAuthConfig(ctx context.Context, cfg modelconfig.Config) error {
	if a == nil || a.oauthPolicy == nil {
		return fmt.Errorf("account policy module is unavailable")
	}
	return a.oauthPolicy.UpdateConfig(ctx, cfg)
}

func (a *App) OAuthStatus() oauthpolicy.Status {
	if a == nil || a.oauthPolicy == nil {
		return oauthpolicy.Status{LastError: "account policy module is unavailable"}
	}
	return a.oauthPolicy.Status()
}

func (a *App) FilterModels(ctx context.Context, hostCfg *internalconfig.Config, auth *coreauth.Auth, models []*registry.ModelInfo, requester host.AuthHTTPRequester) []*registry.ModelInfo {
	if a == nil || a.oauthPolicy == nil {
		return models
	}
	return host.FilterModels(ctx, hostCfg, auth, models, a.oauthPolicy, requester)
}

func (a *App) RefreshAccountPolicies() {
	if a != nil && a.oauthPolicy != nil {
		a.oauthPolicy.RefreshPlanDetection()
	}
}

func (a *App) ApplyCachedAccountPolicy(auth *coreauth.Auth) *coreauth.Auth {
	if a == nil || a.oauthPolicy == nil {
		if auth == nil {
			return nil
		}
		return auth.Clone()
	}
	return host.ApplyCachedAccountPolicy(auth, a.oauthPolicy)
}

func (a *App) ForgetAccountPolicy(authID string) {
	if a != nil && a.oauthPolicy != nil {
		a.oauthPolicy.ForgetAuth(authID)
	}
}

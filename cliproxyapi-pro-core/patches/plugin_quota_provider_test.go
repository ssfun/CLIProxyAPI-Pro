package pluginhost

import (
	"context"
	"reflect"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type quotaProviderStub struct {
	identifier string
	fetch      func(context.Context, pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error)
}

func (p quotaProviderStub) Identifier() string { return p.identifier }
func (p quotaProviderStub) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	return p.fetch(ctx, req)
}

func TestFetchQuotaNormalizesAndRetainsPreviousPlan(t *testing.T) {
	remaining := 1.2
	previous := &pluginapi.QuotaSnapshot{Plan: &pluginapi.QuotaPlan{ID: "g1-pro-tier", Kind: "pro", ObservedAtMS: 123}}
	provider := quotaProviderStub{identifier: "gemini-cli", fetch: func(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
		if req.AuthID != "auth-1" || req.AuthProvider != "gemini-cli" || req.Previous == nil || req.HTTPClient == nil {
			t.Fatalf("FetchQuota request = %#v", req)
		}
		return pluginapi.QuotaFetchResponse{
			Snapshot:        pluginapi.QuotaSnapshot{Items: []pluginapi.QuotaItem{{ID: " pro ", Label: " Pro ", RemainingFraction: &remaining}}},
			PlanUnavailable: true,
			PlanError:       "tier endpoint timed out",
		}, nil
	}}
	host := newHostWithRecords(capabilityRecord{
		id:     "geminicli",
		meta:   pluginapi.Metadata{Name: "Gemini CLI", Version: "1.0.0"},
		plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: provider}},
	})
	result := host.FetchQuota(context.Background(), &coreauth.Auth{ID: "auth-1", Provider: "gemini-cli", FileName: "gemini.json"}, previous)
	if !result.Handled || result.Err != nil || result.PluginID != "geminicli" {
		t.Fatalf("FetchQuota() = %#v", result)
	}
	if result.Snapshot.SchemaVersion != pluginapi.QuotaSnapshotSchemaVersion || result.Snapshot.Provider != "gemini-cli" || result.Snapshot.ObservedAtMS <= 0 {
		t.Fatalf("normalized snapshot = %#v", result.Snapshot)
	}
	if got := *result.Snapshot.Items[0].RemainingFraction; got != 1 {
		t.Fatalf("remaining fraction = %v, want 1", got)
	}
	if result.Snapshot.Plan == nil || result.Snapshot.Plan.ID != "g1-pro-tier" || !result.Snapshot.Plan.Stale || result.Snapshot.Plan.Error == "" {
		t.Fatalf("retained plan = %#v", result.Snapshot.Plan)
	}
	if previous.Plan.Stale || previous.Plan.Error != "" {
		t.Fatalf("previous plan was mutated: %#v", previous.Plan)
	}
}

func TestHasQuotaProviderMatchesIdentifier(t *testing.T) {
	host := newHostWithRecords(capabilityRecord{id: "geminicli", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		QuotaProvider: quotaProviderStub{identifier: " Gemini-CLI ", fetch: func(context.Context, pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
			return pluginapi.QuotaFetchResponse{}, nil
		}},
	}}})
	if !host.HasQuotaProvider("gemini-cli") || host.HasQuotaProvider("codex") {
		t.Fatal("quota provider matching failed")
	}
	plugins := host.RegisteredPlugins()
	if len(plugins) != 1 || !plugins[0].SupportsQuota || plugins[0].QuotaProvider != "Gemini-CLI" || plugins[0].QuotaMode != "native" {
		t.Fatalf("registered quota capability = %#v", plugins)
	}
	if caps := rpcCapabilitiesFromPlugin(host.activeRecords()[0].plugin); !caps.QuotaProvider {
		t.Fatal("RPC registration omitted quota_provider capability")
	}
}

func TestFetchQuotaRejectsNewerSnapshotSchema(t *testing.T) {
	host := newHostWithRecords(capabilityRecord{id: "future", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{
		QuotaProvider: quotaProviderStub{identifier: "gemini-cli", fetch: func(context.Context, pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
			return pluginapi.QuotaFetchResponse{Snapshot: pluginapi.QuotaSnapshot{SchemaVersion: pluginapi.QuotaSnapshotSchemaVersion + 1}}, nil
		}},
	}}})
	result := host.FetchQuota(context.Background(), &coreauth.Auth{ID: "auth-1", Provider: "gemini-cli"}, nil)
	if !result.Handled || result.Err == nil {
		t.Fatalf("FetchQuota() = %#v, want handled schema error", result)
	}
}

func TestFetchQuotaConstrainsAuthUpdateToCurrentIdentity(t *testing.T) {
	provider := quotaProviderStub{identifier: "gemini-cli", fetch: func(context.Context, pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
		return pluginapi.QuotaFetchResponse{AuthUpdate: pluginapi.AuthData{
			Provider:    "codex",
			ID:          "other-auth",
			FileName:    "other.json",
			StorageJSON: []byte(`{"access_token":"refreshed"}`),
			Attributes:  map[string]string{"path": "/tmp/other.json", "runtime_only": "true"},
		}}, nil
	}}
	host := newHostWithRecords(capabilityRecord{id: "geminicli", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: provider}}})
	createdAt := time.Now().Add(-time.Hour).UTC()
	auth := &coreauth.Auth{
		ID:         "auth-1",
		Index:      "gemini-cli:auth-1",
		Provider:   "gemini-cli",
		FileName:   "gemini.json",
		CreatedAt:  createdAt,
		Attributes: map[string]string{"path": "/tmp/gemini.json", "project_id": "project-a"},
	}

	result := host.FetchQuota(context.Background(), auth, nil)
	if !result.Handled || result.Err != nil || result.Auth == nil {
		t.Fatalf("FetchQuota() = %#v", result)
	}
	if result.Auth.ID != auth.ID || result.Auth.Index != auth.Index || result.Auth.Provider != auth.Provider || result.Auth.FileName != auth.FileName {
		t.Fatalf("auth identity = id:%q index:%q provider:%q file:%q", result.Auth.ID, result.Auth.Index, result.Auth.Provider, result.Auth.FileName)
	}
	if !result.Auth.CreatedAt.Equal(createdAt) {
		t.Fatalf("CreatedAt = %v, want %v", result.Auth.CreatedAt, createdAt)
	}
	if !reflect.DeepEqual(result.Auth.Attributes, auth.Attributes) {
		t.Fatalf("Attributes = %#v, want %#v", result.Auth.Attributes, auth.Attributes)
	}
}

func TestFetchQuotaClampsFutureObservationTime(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UnixMilli()
	provider := quotaProviderStub{identifier: "gemini-cli", fetch: func(context.Context, pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
		return pluginapi.QuotaFetchResponse{Snapshot: pluginapi.QuotaSnapshot{ObservedAtMS: future, Plan: &pluginapi.QuotaPlan{ObservedAtMS: future}}}, nil
	}}
	host := newHostWithRecords(capabilityRecord{id: "geminicli", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{QuotaProvider: provider}}})
	before := time.Now().UnixMilli()
	result := host.FetchQuota(context.Background(), &coreauth.Auth{ID: "auth-1", Provider: "gemini-cli"}, nil)
	after := time.Now().UnixMilli()
	if !result.Handled || result.Err != nil {
		t.Fatalf("FetchQuota() = %#v", result)
	}
	if result.Snapshot.ObservedAtMS < before || result.Snapshot.ObservedAtMS > after {
		t.Fatalf("ObservedAtMS = %d, want host time in [%d,%d]", result.Snapshot.ObservedAtMS, before, after)
	}
	if result.Snapshot.Plan == nil || result.Snapshot.Plan.ObservedAtMS != result.Snapshot.ObservedAtMS {
		t.Fatalf("plan ObservedAtMS = %#v, want normalized snapshot time", result.Snapshot.Plan)
	}
}

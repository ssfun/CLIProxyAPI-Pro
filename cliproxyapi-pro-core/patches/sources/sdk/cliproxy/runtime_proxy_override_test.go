package cliproxy

import (
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestDefaultRoundTripperProviderTracksRuntimeProxyGeneration(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	provider := newDefaultRoundTripperProvider()
	auth := &coreauth.Auth{}
	if got := provider.RoundTripperFor(auth); got != nil {
		t.Fatalf("initial RoundTripperFor() = %T, want nil", got)
	}

	proxyutil.SetRuntimeProxyOverride("", "socks5://127.0.0.1:18318")
	first := provider.RoundTripperFor(auth)
	if first == nil {
		t.Fatal("RoundTripperFor() ignored empty-base runtime override")
	}
	if again := provider.RoundTripperFor(auth); again != first {
		t.Fatal("same generation did not reuse transport")
	}

	proxyutil.ClearRuntimeProxyOverride()
	if got := provider.RoundTripperFor(auth); got != nil {
		t.Fatalf("RoundTripperFor() after clear = %T, want nil", got)
	}
	proxyutil.SetRuntimeProxyOverride("", "socks5://127.0.0.1:18318")
	if second := provider.RoundTripperFor(auth); second == nil || second == first {
		t.Fatal("new runtime generation reused stale transport")
	}
}

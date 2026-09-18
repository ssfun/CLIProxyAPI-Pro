package live

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

func TestSidebandDialerResolvesEmptyBaseRuntimeOverride(t *testing.T) {
	proxyutil.ClearRuntimeProxyOverride()
	t.Cleanup(proxyutil.ClearRuntimeProxyOverride)
	proxyutil.SetRuntimeProxyOverride("", "socks5://127.0.0.1:18318")
	dialer := newSidebandDialer("")
	if dialer.NetDialContext == nil || dialer.Proxy != nil {
		t.Fatalf("runtime SOCKS5 dialer = %+v", dialer)
	}
	proxyutil.ClearRuntimeProxyOverride()
	dialer = newSidebandDialer("")
	if dialer.NetDialContext != nil || dialer.Proxy == nil {
		t.Fatalf("cleared dialer = %+v", dialer)
	}
}

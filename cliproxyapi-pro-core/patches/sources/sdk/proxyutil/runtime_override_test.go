package proxyutil

import "testing"

func TestRuntimeProxyOverrideOnlyReplacesBaseSetting(t *testing.T) {
	ClearRuntimeProxyOverride()
	t.Cleanup(ClearRuntimeProxyOverride)
	SetRuntimeProxyOverride("http://base.example:8080", "socks5://127.0.0.1:8318")
	if got := resolveRuntimeProxyOverride("http://base.example:8080"); got != "socks5://127.0.0.1:8318" {
		t.Fatalf("base override = %q", got)
	}
	if got := resolveRuntimeProxyOverride("http://credential.example:8080"); got != "http://credential.example:8080" {
		t.Fatalf("credential override changed to %q", got)
	}
	if got := resolveRuntimeProxyOverride("direct"); got != "direct" {
		t.Fatalf("direct override changed to %q", got)
	}
}

func TestRuntimeProxyOverrideSupportsEmptyBase(t *testing.T) {
	ClearRuntimeProxyOverride()
	t.Cleanup(ClearRuntimeProxyOverride)
	SetRuntimeProxyOverride("", "socks5://127.0.0.1:8318")
	resolution := ResolveEffectiveProxy("")
	if resolution.Effective != "socks5://127.0.0.1:8318" || !resolution.Overridden {
		t.Fatalf("empty base override = %+v", resolution)
	}
}

func TestRuntimeProxyOverrideGenerationChangesOnlyWithEffectiveState(t *testing.T) {
	ClearRuntimeProxyOverride()
	initial := ResolveEffectiveProxy("").Generation
	ClearRuntimeProxyOverride()
	if got := ResolveEffectiveProxy("").Generation; got != initial {
		t.Fatalf("no-op clear generation = %d, want %d", got, initial)
	}

	SetRuntimeProxyOverride("", "socks5://127.0.0.1:8318")
	first := ResolveEffectiveProxy("")
	if first.Generation <= initial {
		t.Fatalf("set generation = %d, want > %d", first.Generation, initial)
	}
	SetRuntimeProxyOverride("", "socks5://127.0.0.1:8318")
	if got := ResolveEffectiveProxy("").Generation; got != first.Generation {
		t.Fatalf("no-op set generation = %d, want %d", got, first.Generation)
	}

	ClearRuntimeProxyOverride()
	cleared := ResolveEffectiveProxy("")
	if cleared.Generation <= first.Generation || cleared.Overridden || cleared.Effective != "" {
		t.Fatalf("cleared resolution = %+v", cleared)
	}
}

func TestBuildDialerWithoutRuntimeOverridePreservesRawSetting(t *testing.T) {
	ClearRuntimeProxyOverride()
	t.Cleanup(ClearRuntimeProxyOverride)
	SetRuntimeProxyOverride("direct", "socks5://127.0.0.1:8318")

	_, overriddenMode, errBuild := BuildDialer("direct")
	if errBuild != nil || overriddenMode != ModeProxy {
		t.Fatalf("BuildDialer() mode = %v, err = %v; want runtime override", overriddenMode, errBuild)
	}
	dialer, mode, errBuild := BuildDialerWithoutRuntimeOverride("direct")
	if errBuild != nil || mode != ModeDirect || dialer == nil {
		t.Fatalf("BuildDialerWithoutRuntimeOverride() = dialer:%v mode:%v err:%v", dialer, mode, errBuild)
	}
}

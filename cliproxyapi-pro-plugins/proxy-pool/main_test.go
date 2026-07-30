package main

import "testing"

func TestPluginRegistrationTitle(t *testing.T) {
	if got := pluginRegistration().Metadata.Name; got != "Pro Proxy Pool" {
		t.Fatalf("plugin title = %q, want %q", got, "Pro Proxy Pool")
	}
}

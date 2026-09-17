package inspection

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNormalizeScheduleOwnsDefaultsAndBounds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	got := NormalizeSchedule(Schedule{
		Enabled: true,
		Settings: Settings{
			TargetType: "unknown", Workers: 99, ProviderWorkers: 99, DeleteWorkers: 99,
			Timeout: 1, Retries: 99, UsedPercentThreshold: 101,
		},
	}, now)
	if got.Settings.TargetType != ProviderAll || got.Settings.Workers != MaxWorkers || got.Settings.ProviderWorkers != MaxProviderWorkers || got.Settings.DeleteWorkers != MaxDeleteWorkers {
		t.Fatalf("normalized workers/providers = %+v", got.Settings)
	}
	if got.Settings.Timeout != MinTimeoutMS || got.Settings.Retries != MaxRetries || got.Settings.UsedPercentThreshold != 100 {
		t.Fatalf("normalized limits = %+v", got.Settings)
	}
	if got.NextRunAt != now.Add(DefaultIntervalMin*time.Minute).UnixMilli() {
		t.Fatalf("next run = %d", got.NextRunAt)
	}
}

func TestScheduleAcceptsFractionalQuotaThreshold(t *testing.T) {
	var schedule Schedule
	if err := json.Unmarshal([]byte(`{"settings":{"usedPercentThreshold":99.5}}`), &schedule); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	got := NormalizeSchedule(schedule, time.Now())
	if got.Settings.UsedPercentThreshold != 99.5 {
		t.Fatalf("used percent threshold = %v, want 99.5", got.Settings.UsedPercentThreshold)
	}
}

func TestNormalizeScheduleDefaultsProviderWorkersForLegacySettings(t *testing.T) {
	got := NormalizeSchedule(Schedule{Settings: Settings{Workers: 6}}, time.Now())
	if got.Settings.Workers != 6 || got.Settings.ProviderWorkers != DefaultSettings().ProviderWorkers {
		t.Fatalf("normalized legacy workers = %+v", got.Settings)
	}
}

func TestDefaultSettingsEnableQuotaRecovery(t *testing.T) {
	if !DefaultSettings().AutoExecuteQuotaRecoveryEnable {
		t.Fatal("quota recovery should be on by default")
	}
}

func TestLegacyScheduleJSONEnablesQuotaRecoveryByDefault(t *testing.T) {
	var schedule Schedule
	if err := json.Unmarshal([]byte(`{"settings":{"usedPercentThreshold":95}}`), &schedule); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !schedule.Settings.AutoExecuteQuotaRecoveryEnable {
		t.Fatal("legacy schedule JSON should enable quota recovery")
	}
	if err := json.Unmarshal([]byte(`{"settings":{"autoExecuteQuotaRecoveryEnable":false}}`), &schedule); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if schedule.Settings.AutoExecuteQuotaRecoveryEnable {
		t.Fatal("explicit false must remain disabled")
	}
}

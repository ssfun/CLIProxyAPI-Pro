package apikeypolicy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSelfQuotaReportsExhaustionWithoutAdmittingAndHonorsTakeover(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	identity := testIdentity(t, "self-own")
	limit := int64(1)
	policy, err := service.Create(ctx, identity, "PRIVATE_NAME", ProfileInput{Name: "PRIVATE_PROFILE"}, &QuotaInput{Enabled: true, Requests: &limit, Period: QuotaPeriod{Type: QuotaPeriodAllTime}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.db.Exec(`update api_key_policy_quotas set requests_used = 1 where policy_id = ?`, policy.ID); err != nil {
		t.Fatal(err)
	}
	result, err := service.QuerySelfQuota(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Enforced || result.State != QuotaAdmissionExhausted || result.Quota.Usage.RequestsUsed != 1 {
		t.Fatalf("unexpected quota %#v", result)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), identity.Hash()) {
		t.Fatalf("leaked policy: %s", data)
	}
	other, err := service.QuerySelfQuota(ctx, testIdentity(t, "self-other"))
	if err != nil || other.Quota != nil {
		t.Fatalf("cross-key quota: %#v %v", other, err)
	}
	if err = service.SetTakeover(ctx, false); err != nil {
		t.Fatal(err)
	}
	result, err = service.QuerySelfQuota(ctx, identity)
	if err != nil || result.Enforced || result.State != QuotaAdmissionDisabled {
		t.Fatalf("takeover disabled %#v %v", result, err)
	}
}

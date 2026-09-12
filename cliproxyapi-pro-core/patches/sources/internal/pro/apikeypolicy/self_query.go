package apikeypolicy

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// SelfQuota is a read-only projection; it never exposes policy or account rules.
type SelfQuota struct {
	Enforced        bool   `json:"enforced"`
	Quota           *Quota `json:"quota,omitempty"`
	State           string `json:"state"`
	NextRecoverAtMS int64  `json:"nextRecoverAtMs,omitempty"`
}

func (s *Service) QuerySelfQuota(ctx context.Context, identity AuthenticatedAPIKeyIdentity) (SelfQuota, error) {
	if s == nil || s.store == nil || !identity.Valid() || !s.Healthy() {
		return SelfQuota{}, ErrUnavailable
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result := SelfQuota{Enforced: s.TakeoverEnabled(), State: QuotaAdmissionDisabled}
	var policyID string
	err := s.store.db.QueryRowContext(ctx, `select id from api_key_policies where api_key_hash = ?`, identity.Hash()).Scan(&policyID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return SelfQuota{}, err
	}
	result.Quota, err = getPolicyQuotaAt(ctx, s.store.db, policyID, time.Now().UnixMilli())
	if err != nil {
		return SelfQuota{}, err
	}
	if !result.Enforced || result.Quota == nil || !result.Quota.Enabled {
		return result, nil
	}
	result.State = QuotaAdmissionAvailable
	if len(result.Quota.Usage.Exhausted) > 0 {
		result.State = QuotaAdmissionExhausted
		result.NextRecoverAtMS, err = quotaNextRecoverAt(ctx, s.store.db, policyID, *result.Quota)
		if err != nil {
			return SelfQuota{}, err
		}
	}
	s.pendingMu.Lock()
	key := quotaPricingBlockKey{policyID: policyID, epoch: result.Quota.Epoch}
	if s.pricingBlocked[key] > 0 || s.settlementBlocked[key] > 0 {
		result.State = QuotaAdmissionBlocked
	}
	s.pendingMu.Unlock()
	return result, nil
}

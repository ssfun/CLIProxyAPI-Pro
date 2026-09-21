package auth

const (
	accountPolicyBasePrefix   = "pro.oauth-policy.base-prefix"
	accountPolicyBasePriority = "pro.oauth-policy.base-priority"
	accountPolicyBaseWeight   = "pro.oauth-policy.base-weight"
	accountPolicyMissingValue = "\x00"
)

// AccountPolicyResolver derives an execution-only auth snapshot. Implementations
// must not mutate or persist the supplied base auth.
type AccountPolicyResolver func(*Auth) *Auth

func (m *Manager) SetAccountPolicyResolver(resolver AccountPolicyResolver) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.accountPolicyResolver = resolver
	m.mu.Unlock()
	// Policy changes do not change the base auth generation. Refresh entries
	// explicitly so upstream scheduler version checks cannot skip the new policy.
	m.RefreshSchedulerAll()
}

func (m *Manager) applyAccountPolicy(auth *Auth) *Auth {
	if auth == nil {
		return nil
	}
	m.mu.RLock()
	resolver := m.accountPolicyResolver
	m.mu.RUnlock()
	return resolveAccountPolicy(auth, resolver)
}

func resolveAccountPolicy(auth *Auth, resolver AccountPolicyResolver) *Auth {
	if auth == nil {
		return nil
	}
	if resolver == nil {
		return auth.Clone()
	}
	resolved := resolver(auth)
	if resolved == nil {
		return auth.Clone()
	}
	return resolved
}

func RememberAccountPolicyBase(auth *Auth) {
	if auth == nil {
		return
	}
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	if _, found := auth.Attributes[accountPolicyBasePrefix]; !found {
		auth.Attributes[accountPolicyBasePrefix] = accountPolicyMissingValue
		if auth.Prefix != "" {
			auth.Attributes[accountPolicyBasePrefix] = auth.Prefix
		}
	}
	for marker, source := range map[string]string{
		accountPolicyBasePriority: "priority",
		accountPolicyBaseWeight:   AttributeWeight,
	} {
		if _, found := auth.Attributes[marker]; found {
			continue
		}
		auth.Attributes[marker] = accountPolicyMissingValue
		if value, found := auth.Attributes[source]; found {
			auth.Attributes[marker] = value
		}
	}
}

func RestoreAccountPolicyBase(auth *Auth) {
	if auth == nil || auth.Attributes == nil {
		return
	}
	if value, found := auth.Attributes[accountPolicyBasePrefix]; found {
		auth.Prefix = value
		if value == accountPolicyMissingValue {
			auth.Prefix = ""
		}
		delete(auth.Attributes, accountPolicyBasePrefix)
	}
	for marker, target := range map[string]string{
		accountPolicyBasePriority: "priority",
		accountPolicyBaseWeight:   AttributeWeight,
	} {
		value, found := auth.Attributes[marker]
		if !found {
			continue
		}
		if value == accountPolicyMissingValue {
			delete(auth.Attributes, target)
		} else {
			auth.Attributes[target] = value
		}
		delete(auth.Attributes, marker)
	}
}

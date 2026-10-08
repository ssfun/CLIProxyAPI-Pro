package inspection

import (
	"math/rand"
)

func ShouldInspectCandidate(hasAuth, apiKey bool, provider, targetType string) bool {
	if !hasAuth || apiKey {
		return false
	}
	provider = CanonicalProvider(provider)
	targetType = CanonicalProvider(targetType)
	if !IsSupportedProvider(provider) {
		return false
	}
	return targetType == ProviderAll || targetType == provider
}

func Sample[T any](items []T, sampleSize int, seed int64) []T {
	if sampleSize <= 0 || sampleSize >= len(items) {
		return items
	}
	out := append([]T(nil), items...)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out[:sampleSize]
}

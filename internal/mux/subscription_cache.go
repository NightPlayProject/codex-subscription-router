package mux

import (
	"context"
	"sync"
	"time"
)

// Subscription validation is expensive because it crosses into the app-server.
// Keep a short-lived positive cache so rapid chat switching does not repeatedly
// block on account/read while preserving correctness after longer idle periods.
var subscriptionRefreshCache = struct {
	sync.Mutex
	entries map[string]time.Time
}{entries: make(map[string]time.Time)}

const subscriptionRefreshTTL = 30 * time.Second

func (m *Multiplexer) refreshSubscriptionFast(ctx context.Context, accountID string, child appServerRequester) error {
	now := time.Now()
	subscriptionRefreshCache.Lock()
	if refreshed, ok := subscriptionRefreshCache.entries[accountID]; ok && now.Sub(refreshed) < subscriptionRefreshTTL {
		subscriptionRefreshCache.Unlock()
		return nil
	}
	subscriptionRefreshCache.Unlock()

	if err := refreshSignedInSubscription(ctx, child); err != nil {
		return err
	}

	subscriptionRefreshCache.Lock()
	subscriptionRefreshCache.entries[accountID] = now
	subscriptionRefreshCache.Unlock()
	return nil
}

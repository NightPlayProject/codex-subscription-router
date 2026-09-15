package mux

import "time"

const migrationCacheTTL = 5 * time.Minute

func migrationCacheKey(threadID, accountID string) string {
	return threadID + "\x00" + accountID
}

func (m *Multiplexer) hasRecentMigration(threadID, accountID string) bool {
	key := migrationCacheKey(threadID, accountID)
	m.migrationCacheMu.RLock()
	when, ok := m.migrationCache[key]
	m.migrationCacheMu.RUnlock()
	return ok && time.Since(when) < migrationCacheTTL
}

func (m *Multiplexer) markMigrationComplete(threadID, accountID string) {
	key := migrationCacheKey(threadID, accountID)
	m.migrationCacheMu.Lock()
	if m.migrationCache == nil {
		m.migrationCache = make(map[string]time.Time)
	}
	m.migrationCache[key] = time.Now()
	m.migrationCacheMu.Unlock()
}
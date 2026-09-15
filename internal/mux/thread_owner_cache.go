package mux

// Fast owner lookup for hot routing paths. The persistent store remains the
// source of truth; this only avoids repeated reads while a chat is active.

func (m *Multiplexer) cachedThreadOwner(threadID string) (string, bool) {
	m.threadOwnerMu.RLock()
	owner, ok := m.threadOwnerCache[threadID]
	m.threadOwnerMu.RUnlock()
	if ok {
		return owner, true
	}
	owner, ok = m.store.ThreadOwner(threadID)
	if ok {
		m.threadOwnerMu.Lock()
		m.threadOwnerCache[threadID] = owner
		m.threadOwnerMu.Unlock()
	}
	return owner, ok
}

func (m *Multiplexer) cacheThreadOwner(threadID, owner string) {
	if threadID == "" || owner == "" {
		return
	}
	m.threadOwnerMu.Lock()
	m.threadOwnerCache[threadID] = owner
	m.threadOwnerMu.Unlock()
}


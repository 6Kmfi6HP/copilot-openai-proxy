package copilot

import "time"

func (m *sessionManager) release(conversationID string) {
	m.mu.Lock()

	entry, ok := m.sessions[conversationID]
	if !ok {
		m.mu.Unlock()
		return
	}

	entry.status = sessionStatusDraining
	entry.updatedAt = time.Now()
	if entry.session != nil {
		entry.session.LastUsedAt = entry.updatedAt
	}
	releaseCapacity := entry.holdsCapacity
	entry.holdsCapacity = false
	delete(m.sessions, conversationID)
	m.compactLocked()
	session := entry.session
	m.mu.Unlock()

	if releaseCapacity {
		m.capacity.Release(1)
	}
	closeSession(session)
	m.maybeRefill()
}

func (m *sessionManager) invalidate(conversationID string) {
	m.mu.Lock()

	entry, ok := m.sessions[conversationID]
	if !ok {
		m.mu.Unlock()
		return
	}

	entry.status = sessionStatusClosed
	entry.updatedAt = time.Now()
	releaseCapacity := entry.holdsCapacity
	entry.holdsCapacity = false
	delete(m.sessions, conversationID)
	m.compactLocked()
	session := entry.session
	m.mu.Unlock()

	if releaseCapacity {
		m.capacity.Release(1)
	}
	closeSession(session)
	m.maybeRefill()
}

func (m *sessionManager) remove(id string) {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		m.compactLocked()
	}
	m.mu.Unlock()

	if ok && entry.holdsCapacity {
		m.capacity.Release(1)
	}
}

func (m *sessionManager) replaceByAgeLocked(from, to string) {
	for idx, id := range m.byAge {
		if id == from {
			m.byAge[idx] = to
			return
		}
	}
	m.byAge = append(m.byAge, to)
}

func (m *sessionManager) compactLocked() {
	alive := make([]string, 0, len(m.byAge))
	for _, id := range m.byAge {
		if _, ok := m.sessions[id]; ok {
			alive = append(alive, id)
		}
	}
	m.byAge = alive
}

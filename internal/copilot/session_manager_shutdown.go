package copilot

import (
	"context"
	"time"
)

func (m *sessionManager) evictExpiredIdle(now time.Time, ttl time.Duration) {
	if ttl <= 0 {
		return
	}

	victims, released := m.takeExpiredIdle(now, ttl)
	if released > 0 {
		m.capacity.Release(int64(released))
	}
	closeSessions(victims)
	if released > 0 {
		m.maybeRefill()
	}
}

func (m *sessionManager) shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	m.stopRefill()

	victims, released := m.takeShutdownVictims()
	if released > 0 {
		m.capacity.Release(int64(released))
	}
	closeSessions(victims)

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if m.sessionCount() == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *sessionManager) stopRefill() {
	if m.cancelRefill != nil {
		m.cancelRefill()
	}
}

func (m *sessionManager) takeExpiredIdle(now time.Time, ttl time.Duration) ([]*SessionState, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	victims := make([]*SessionState, 0)
	released := 0
	for _, id := range m.byAge {
		entry, ok := m.sessions[id]
		if !ok || entry.status != sessionStatusIdle {
			continue
		}

		lastTouched := entry.updatedAt
		if entry.session != nil && entry.session.LastUsedAt.After(lastTouched) {
			lastTouched = entry.session.LastUsedAt
		}
		if now.Sub(lastTouched) < ttl {
			continue
		}

		entry.status = sessionStatusClosed
		delete(m.sessions, id)
		if entry.holdsCapacity {
			entry.holdsCapacity = false
			released++
		}
		victims = append(victims, entry.session)
	}
	if len(victims) > 0 {
		m.compactLocked()
	}
	return victims, released
}

func (m *sessionManager) takeShutdownVictims() ([]*SessionState, int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	victims := make([]*SessionState, 0)
	released := 0
	for id, entry := range m.sessions {
		switch entry.status {
		case sessionStatusIdle, sessionStatusDraining, sessionStatusClosed:
			entry.status = sessionStatusClosed
			delete(m.sessions, id)
			if entry.holdsCapacity {
				entry.holdsCapacity = false
				released++
			}
			victims = append(victims, entry.session)
		case sessionStatusWarming:
			if !entry.pooled {
				continue
			}
			delete(m.sessions, id)
			if entry.holdsCapacity {
				entry.holdsCapacity = false
				released++
			}
		}
	}
	if len(victims) > 0 || released > 0 {
		m.compactLocked()
	}
	return victims, released
}

func (m *sessionManager) sessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.sessions)
}

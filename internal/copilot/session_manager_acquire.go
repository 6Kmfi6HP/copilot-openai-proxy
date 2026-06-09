package copilot

import (
	"context"
	"time"
)

func (m *sessionManager) acquire(ctx context.Context) (*sessionLease, error) {
	if lease, ok := m.tryActivateIdleLease(); ok {
		m.maybeRefill()
		return lease, nil
	}

	if err := m.capacity.Acquire(ctx, 1); err != nil {
		return nil, NewCapacityError("session capacity exhausted", err)
	}

	if lease, ok := m.tryActivateIdleLease(); ok {
		m.capacity.Release(1)
		m.maybeRefill()
		return lease, nil
	}

	warmingID := m.reserveWarming(false)
	session, err := m.startSession(ctx)
	if err != nil {
		m.remove(warmingID)
		return nil, err
	}

	lease := m.activateLease(warmingID, session)
	m.maybeRefill()
	return lease, nil
}

func (m *sessionManager) acquireFresh(ctx context.Context) (*sessionLease, error) {
	if err := m.capacity.Acquire(ctx, 1); err != nil {
		return nil, NewCapacityError("session capacity exhausted", err)
	}

	warmingID := m.reserveWarming(false)
	session, err := m.startSession(ctx)
	if err != nil {
		m.remove(warmingID)
		return nil, err
	}

	return m.activateLease(warmingID, session), nil
}

func (m *sessionManager) reserveWarming(pooled bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.warmingSeq++
	warmingID := newWarmingID(m.warmingSeq)
	m.sessions[warmingID] = &managedSession{
		status:        sessionStatusWarming,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        pooled,
	}
	m.byAge = append(m.byAge, warmingID)

	return warmingID
}

func (m *sessionManager) activateLease(warmingID string, session *SessionState) *sessionLease {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, warmingID)
	m.replaceByAgeLocked(warmingID, session.ConversationID)
	m.sessions[session.ConversationID] = &managedSession{
		session:       session,
		status:        sessionStatusLeased,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        false,
	}

	return &sessionLease{
		manager:        m,
		conversationID: session.ConversationID,
		session:        session,
	}
}

func (m *sessionManager) activateIdle(warmingID string, session *SessionState) {
	m.mu.Lock()

	entry, ok := m.sessions[warmingID]
	if !ok || m.refillCtx.Err() != nil {
		releaseCapacity := ok && entry.holdsCapacity
		if ok {
			delete(m.sessions, warmingID)
			m.compactLocked()
		}
		m.mu.Unlock()

		if releaseCapacity {
			m.capacity.Release(1)
		}
		closeSession(session)
		return
	}

	delete(m.sessions, warmingID)
	m.replaceByAgeLocked(warmingID, session.ConversationID)
	m.sessions[session.ConversationID] = &managedSession{
		session:       session,
		status:        sessionStatusIdle,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        true,
	}
	m.mu.Unlock()
}

func (m *sessionManager) tryActivateIdleLease() (*sessionLease, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range m.byAge {
		entry, ok := m.sessions[id]
		if !ok || entry.status != sessionStatusIdle || !entry.pooled {
			continue
		}

		entry.status = sessionStatusLeased
		entry.pooled = false
		entry.updatedAt = time.Now()
		if entry.session != nil {
			entry.session.LastUsedAt = entry.updatedAt
		}

		return &sessionLease{
			manager:        m,
			conversationID: id,
			session:        entry.session,
		}, true
	}

	return nil, false
}

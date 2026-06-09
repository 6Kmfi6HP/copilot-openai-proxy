package copilot

import (
	"context"
	"errors"
	"time"
)

func (m *sessionManager) maybeRefill() {
	m.mu.Lock()
	if m.refillCtx.Err() != nil || !m.needsWarmIdleLocked() {
		m.mu.Unlock()
		return
	}
	if m.refillRunning {
		m.refillRequested = true
		m.mu.Unlock()
		return
	}
	m.refillRunning = true
	m.mu.Unlock()

	go m.refillLoop()
}

func (m *sessionManager) refillLoop() {
	for {
		select {
		case <-m.refillCtx.Done():
			m.finishRefillFailure()
			return
		default:
		}

		warmingID, ok := m.reserveWarmIdleSlot()
		if !ok {
			if !m.finishRefillPass() {
				return
			}
			continue
		}

		session, err := m.startSession(m.refillCtx)
		if err != nil {
			m.remove(warmingID)
			m.finishRefillFailure()
			if errors.Is(err, context.Canceled) {
				return
			}
			return
		}

		m.activateIdle(warmingID, session)
	}
}

func (m *sessionManager) reserveWarmIdleSlot() (string, bool) {
	if m.refillCtx.Err() != nil || !m.capacity.TryAcquire(1) {
		return "", false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.refillCtx.Err() != nil || !m.needsWarmIdleLocked() {
		m.capacity.Release(1)
		return "", false
	}

	m.warmingSeq++
	warmingID := newWarmingID(m.warmingSeq)
	m.sessions[warmingID] = &managedSession{
		status:        sessionStatusWarming,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        true,
	}
	m.byAge = append(m.byAge, warmingID)

	return warmingID, true
}

func (m *sessionManager) finishRefillPass() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.refillCtx.Err() != nil {
		m.refillRequested = false
		m.refillRunning = false
		return false
	}

	if m.refillRequested && m.needsWarmIdleLocked() {
		m.refillRequested = false
		return true
	}

	m.refillRequested = false
	m.refillRunning = false
	return false
}

func (m *sessionManager) finishRefillFailure() {
	m.mu.Lock()
	m.refillRequested = false
	m.refillRunning = false
	m.mu.Unlock()
}

func (m *sessionManager) needsWarmIdleLocked() bool {
	if m.refillCtx.Err() != nil || m.warmSessions <= 0 || len(m.sessions) >= m.maxSessions {
		return false
	}

	pooled := 0
	for _, entry := range m.sessions {
		if !entry.pooled {
			continue
		}
		switch entry.status {
		case sessionStatusIdle, sessionStatusWarming:
			pooled++
		}
	}

	return pooled < m.warmSessions
}

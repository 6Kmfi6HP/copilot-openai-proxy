package copilot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

type sessionStatus string

const (
	sessionStatusWarming  sessionStatus = "warming"
	sessionStatusIdle     sessionStatus = "idle"
	sessionStatusLeased   sessionStatus = "leased"
	sessionStatusDraining sessionStatus = "draining"
	sessionStatusClosed   sessionStatus = "closed"
)

type managedSession struct {
	session       *SessionState
	status        sessionStatus
	updatedAt     time.Time
	holdsCapacity bool
	pooled        bool
}

type sessionLease struct {
	manager        *sessionManager
	conversationID string
	session        *SessionState
	once           sync.Once
}

func (l *sessionLease) Session() *SessionState {
	if l == nil {
		return nil
	}
	return l.session
}

func (l *sessionLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.manager.release(l.conversationID)
	})
}

type sessionManager struct {
	mu              sync.RWMutex
	sessions        map[string]*managedSession
	byAge           []string
	maxSessions     int
	warmSessions    int
	capacity        *semaphore.Weighted
	startSession    func(context.Context) (*SessionState, error)
	warmingSeq      uint64
	refillRunning   bool
	refillRequested bool
}

func newSessionManager(maxSessions int, startSession func(context.Context) (*SessionState, error)) *sessionManager {
	return newSessionManagerWithWarmPool(maxSessions, 0, startSession)
}

func newSessionManagerWithWarmPool(maxSessions, warmSessions int, startSession func(context.Context) (*SessionState, error)) *sessionManager {
	if maxSessions <= 0 {
		maxSessions = 1
	}
	if warmSessions < 0 {
		warmSessions = 0
	}
	if warmSessions > maxSessions {
		warmSessions = maxSessions
	}

	manager := &sessionManager{
		sessions:     make(map[string]*managedSession),
		byAge:        make([]string, 0, maxSessions),
		maxSessions:  maxSessions,
		warmSessions: warmSessions,
		capacity:     semaphore.NewWeighted(int64(maxSessions)),
		startSession: startSession,
	}
	manager.maybeRefill()
	return manager
}

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

func (m *sessionManager) reserveWarming(pooled bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.warmingSeq++
	warmingID := fmt.Sprintf("warming:%d", m.warmingSeq)
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

	delete(m.sessions, warmingID)
	m.replaceByAgeLocked(warmingID, session.ConversationID)
	m.sessions[session.ConversationID] = &managedSession{
		session:       session,
		status:        sessionStatusLeased,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        false,
	}
	evicted := m.trimEvictableLocked()
	m.mu.Unlock()
	closeSessions(evicted)

	return &sessionLease{
		manager:        m,
		conversationID: session.ConversationID,
		session:        session,
	}
}

func (m *sessionManager) activateIdle(warmingID string, session *SessionState) {
	m.mu.Lock()

	delete(m.sessions, warmingID)
	m.replaceByAgeLocked(warmingID, session.ConversationID)
	m.sessions[session.ConversationID] = &managedSession{
		session:       session,
		status:        sessionStatusIdle,
		updatedAt:     time.Now(),
		holdsCapacity: true,
		pooled:        true,
	}
	evicted := m.trimEvictableLocked()
	m.mu.Unlock()

	closeSessions(evicted)
}

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

func (m *sessionManager) trimEvictableLocked() []*SessionState {
	if m.maxSessions <= 0 {
		return nil
	}

	victims := make([]*SessionState, 0)
	for len(m.sessions) > m.maxSessions {
		session, ok := m.takeOldestEvictableLocked()
		if !ok {
			break
		}
		victims = append(victims, session)
	}
	if len(victims) > 0 {
		m.compactLocked()
	}
	return victims
}

func (m *sessionManager) takeOldestEvictableLocked() (*SessionState, bool) {
	for _, id := range m.byAge {
		entry, ok := m.sessions[id]
		if !ok {
			continue
		}
		switch entry.status {
		case sessionStatusWarming, sessionStatusLeased:
			continue
		default:
			entry.status = sessionStatusClosed
			delete(m.sessions, id)
			return entry.session, true
		}
	}
	return nil, false
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

func (m *sessionManager) maybeRefill() {
	if m.warmSessions <= 0 {
		return
	}

	m.mu.Lock()
	if !m.needsWarmIdleLocked() {
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
		warmingID, ok := m.reserveWarmIdleSlot()
		if !ok {
			if !m.finishRefillPass() {
				return
			}
			continue
		}

		session, err := m.startSession(context.Background())
		if err != nil {
			m.remove(warmingID)
			m.finishRefillFailure()
			return
		}

		m.activateIdle(warmingID, session)
	}
}

func (m *sessionManager) reserveWarmIdleSlot() (string, bool) {
	if !m.capacity.TryAcquire(1) {
		return "", false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.needsWarmIdleLocked() {
		m.capacity.Release(1)
		return "", false
	}

	m.warmingSeq++
	warmingID := fmt.Sprintf("warming:%d", m.warmingSeq)
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
	if m.warmSessions <= 0 || len(m.sessions) >= m.maxSessions {
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

func closeSessions(sessions []*SessionState) {
	for _, session := range sessions {
		closeSession(session)
	}
}

func closeSession(session *SessionState) {
	if session == nil {
		return
	}
	session.Connected = false
	if session.Conn != nil {
		_ = session.Conn.Close()
	}
}

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
	refillCtx       context.Context
	cancelRefill    context.CancelFunc
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

	refillCtx, cancelRefill := context.WithCancel(context.Background())
	manager := &sessionManager{
		sessions:     make(map[string]*managedSession),
		byAge:        make([]string, 0, maxSessions),
		maxSessions:  maxSessions,
		warmSessions: warmSessions,
		capacity:     semaphore.NewWeighted(int64(maxSessions)),
		startSession: startSession,
		refillCtx:    refillCtx,
		cancelRefill: cancelRefill,
	}
	manager.maybeRefill()
	return manager
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
	session.setConnected(false)
	if session.Conn != nil {
		_ = session.Conn.Close()
	}
}

func newWarmingID(seq uint64) string {
	return fmt.Sprintf("warming:%d", seq)
}

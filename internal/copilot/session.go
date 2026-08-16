package copilot

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type SessionState struct {
	Conn                *websocket.Conn
	ConversationID      string
	ClientSessionID     string
	TemporarySessionKey string
	Cookies             []*http.Cookie
	CreatedAt           time.Time
	LastUsedAt          time.Time
	lease               *sessionLease
	connected           atomic.Bool
}

func (s *SessionState) IsConnected() bool {
	if s == nil {
		return false
	}

	return s.connected.Load()
}

func (s *SessionState) setConnected(connected bool) {
	if s == nil {
		return
	}

	s.connected.Store(connected)
}

func (c *Client) getOrCreateSession(ctx context.Context) (*SessionState, error) {
	sessionCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()

	lease, err := c.sessionMgr.acquire(sessionCtx)
	if err != nil {
		return nil, err
	}

	session := lease.Session()
	if session == nil {
		lease.Release()
		return nil, fmt.Errorf("copilot session manager returned nil session")
	}
	session.lease = lease
	return session, nil
}

func (c *Client) getFreshSession(ctx context.Context) (*SessionState, error) {
	sessionCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()

	lease, err := c.sessionMgr.acquireFresh(sessionCtx)
	if err != nil {
		return nil, err
	}

	session := lease.Session()
	if session == nil {
		lease.Release()
		return nil, fmt.Errorf("copilot session manager returned nil session")
	}
	session.lease = lease
	return session, nil
}

func (c *Client) releaseSession(session *SessionState) {
	if session == nil || session.lease == nil {
		return
	}
	session.lease.Release()
}

func (c *Client) InvalidateSession(conversationID string) {
	if c.sessionMgr == nil {
		return
	}
	c.sessionMgr.invalidate(conversationID)
}

func (c *Client) Close(ctx context.Context) error {
	if c == nil || c.sessionMgr == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	c.stopJanitor()
	return c.sessionMgr.shutdown(ctx)
}

func (c *Client) withRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.timeout <= 0 {
		return ctx, func() {}
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

func (c *Client) startJanitor() {
	if c.sessionMgr == nil || c.warmSessions <= 0 || c.sessionTTL <= 0 || c.cleanupInt <= 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.janitorCancel = cancel
	c.janitorDone = make(chan struct{})

	go func() {
		defer close(c.janitorDone)

		ticker := time.NewTicker(c.cleanupInt)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case tickAt := <-ticker.C:
				c.sessionMgr.evictExpiredIdle(tickAt, c.sessionTTL)
			}
		}
	}()
}

func (c *Client) stopJanitor() {
	if c.janitorCancel != nil {
		c.janitorCancel()
		c.janitorCancel = nil
	}
	if c.janitorDone != nil {
		<-c.janitorDone
		c.janitorDone = nil
	}
}

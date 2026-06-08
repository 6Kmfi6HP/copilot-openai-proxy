package copilot

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

type SessionState struct {
	Conn            *websocket.Conn
	ConversationID  string
	ClientSessionID string
	Cookies         []*http.Cookie
	Connected       bool
	CreatedAt       time.Time
	LastUsedAt      time.Time
	lease           *sessionLease
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

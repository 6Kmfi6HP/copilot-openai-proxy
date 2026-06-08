package copilot

import (
	"context"
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
}

func (c *Client) getOrCreateSession(ctx context.Context) (*SessionState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	session, err := c.startAnon(ctx)
	if err != nil {
		return nil, err
	}

	c.sessions[session.ConversationID] = session
	c.byAge = append(c.byAge, session.ConversationID)
	c.maybeEvictLocked()

	return session, nil
}

func (c *Client) maybeEvictLocked() {
	for len(c.sessions) > c.maxSessions {
		c.evictOldestLocked()
	}
	c.compactLocked()
}

func (c *Client) evictOldestLocked() {
	for _, id := range c.byAge {
		if s, ok := c.sessions[id]; ok {
			s.Connected = false
			if s.Conn != nil {
				s.Conn.Close()
			}
			delete(c.sessions, id)
			return
		}
	}
}

func (c *Client) compactLocked() {
	var alive []string
	for _, id := range c.byAge {
		if _, ok := c.sessions[id]; ok {
			alive = append(alive, id)
		}
	}
	c.byAge = alive
}

func (c *Client) InvalidateSession(conversationID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s, ok := c.sessions[conversationID]
	if !ok {
		return
	}
	s.Connected = false
	if s.Conn != nil {
		s.Conn.Close()
	}
	delete(c.sessions, conversationID)
	c.compactLocked()
}

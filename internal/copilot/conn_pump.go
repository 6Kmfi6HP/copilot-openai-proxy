package copilot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type writeRequest struct {
	message any
	result  chan error
}

type connPump struct {
	ctx          context.Context
	conn         *websocket.Conn
	debug        bool
	readTimeout  time.Duration
	writeTimeout time.Duration
	pingInterval time.Duration
	writes       chan writeRequest
	closeCh      chan struct{}
	closeOnce    sync.Once
}

func newConnPump(ctx context.Context, conn *websocket.Conn, debug bool) *connPump {
	if ctx == nil {
		ctx = context.Background()
	}
	return &connPump{
		ctx:     ctx,
		conn:    conn,
		debug:   debug,
		writes:  make(chan writeRequest, 16),
		closeCh: make(chan struct{}),
	}
}

func (p *connPump) run(events chan<- StreamEvent) {
	p.configureReadDeadline()
	writerDone := make(chan struct{})
	go p.writeLoop(writerDone)

	p.readLoop(events)
	p.close()
	<-writerDone
}

func (p *connPump) send(ctx context.Context, message any) error {
	if ctx == nil {
		ctx = p.ctx
	}

	req := writeRequest{
		message: message,
		result:  make(chan error, 1),
	}

	select {
	case p.writes <- req:
	case <-p.closeCh:
		return fmt.Errorf("copilot websocket pump closed")
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-req.result:
		return err
	case <-p.closeCh:
		select {
		case err := <-req.result:
			return err
		default:
			return fmt.Errorf("copilot websocket pump closed")
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *connPump) readLoop(events chan<- StreamEvent) {
	for {
		_, msg, err := p.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				events <- StreamEvent{Type: EventDone}
				return
			}
			events <- StreamEvent{Type: EventError, Err: fmt.Errorf("copilot websocket read failed: %w", err)}
			return
		}

		evt, err := parseServerEvent(msg)
		if err != nil {
			continue
		}

		if p.debug {
			log.Printf("copilot event raw bytes=%d data=%q", len(msg), string(msg))
		}
		p.bumpReadDeadline()

		if evt.Type == EventChallenge {
			answer := solveHashcash(evt.ChallengeParam)
			log.Printf("copilot challenge solved: param=%s answer=%s", evt.ChallengeParam, answer)
			if err := p.send(p.ctx, newChallengeAnswer(answer)); err != nil {
				log.Printf("copilot failed to send challenge answer: %v", err)
			}
			continue
		}
		if evt.Type == EventIgnore {
			continue
		}

		events <- evt
	}
}

func (p *connPump) writeLoop(done chan<- struct{}) {
	defer close(done)

	var pingTicker *time.Ticker
	if p.pingInterval > 0 {
		pingTicker = time.NewTicker(p.pingInterval)
		defer pingTicker.Stop()
	}

	for {
		select {
		case req := <-p.writes:
			p.bumpWriteDeadline()
			err := sendEvent(p.conn, req.message)
			req.result <- err
			if err != nil {
				p.close()
				return
			}
		case <-p.pingTick(pingTicker):
			if err := p.writeControl(websocket.PingMessage, nil); err != nil {
				p.close()
				return
			}
		case <-p.closeCh:
			return
		}
	}
}

func (p *connPump) close() {
	p.closeOnce.Do(func() {
		close(p.closeCh)
		_ = p.conn.Close()
	})
}

func (p *connPump) configureReadDeadline() {
	if p.readTimeout <= 0 {
		return
	}
	p.bumpReadDeadline()
	p.conn.SetPongHandler(func(string) error {
		p.bumpReadDeadline()
		return nil
	})
}

func (p *connPump) bumpReadDeadline() {
	if p.readTimeout <= 0 {
		return
	}
	_ = p.conn.SetReadDeadline(time.Now().Add(p.readTimeout))
}

func (p *connPump) bumpWriteDeadline() {
	if p.writeTimeout <= 0 {
		return
	}
	_ = p.conn.SetWriteDeadline(time.Now().Add(p.writeTimeout))
}

func (p *connPump) writeControl(messageType int, data []byte) error {
	deadline := time.Time{}
	if p.writeTimeout > 0 {
		deadline = time.Now().Add(p.writeTimeout)
	}
	return p.conn.WriteControl(messageType, data, deadline)
}

func (p *connPump) pingTick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

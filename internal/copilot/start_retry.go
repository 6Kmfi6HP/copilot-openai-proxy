package copilot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const startSessionMaxAttempts = 3

var (
	errRetryableSessionStart = errors.New("copilot retryable session start failure")
	startSessionRetryDelays  = [...]time.Duration{
		100 * time.Millisecond,
		250 * time.Millisecond,
	}
)

type retryableSessionStartError struct {
	message string
	cause   error
}

func newRetryableSessionStartError(message string, cause error) error {
	return &retryableSessionStartError{
		message: message,
		cause:   cause,
	}
}

func newRetryableSessionStartMessage(message string) error {
	return &retryableSessionStartError{message: message}
}

func (e *retryableSessionStartError) Error() string {
	if e == nil {
		return errRetryableSessionStart.Error()
	}
	if e.cause == nil {
		return e.message
	}
	if e.message == "" {
		return e.cause.Error()
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e *retryableSessionStartError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *retryableSessionStartError) Is(target error) bool {
	return target == errRetryableSessionStart
}

func (c *Client) startAnon(ctx context.Context) (*SessionState, error) {
	var lastErr error

	for attempt := 0; attempt < startSessionMaxAttempts; attempt++ {
		session, err := c.startAnonOnce(ctx)
		if err == nil {
			return session, nil
		}
		lastErr = err
		if !shouldRetrySessionStart(ctx, err, attempt) {
			return nil, err
		}

		log.Printf("copilot session start retry attempt=%d max_attempts=%d error=%v",
			attempt+2, startSessionMaxAttempts, err)
		if err := waitBeforeSessionStartRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}

	return nil, fmt.Errorf("copilot session start exhausted retries: %w", lastErr)
}

func shouldRetrySessionStart(ctx context.Context, err error, attempt int) bool {
	if err == nil || attempt >= startSessionMaxAttempts-1 {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || IsBlocked(err) {
		return false
	}
	return errors.Is(err, errRetryableSessionStart)
}

func waitBeforeSessionStartRetry(ctx context.Context, attempt int) error {
	delay := sessionStartRetryDelay(attempt)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sessionStartRetryDelay(attempt int) time.Duration {
	if attempt < len(startSessionRetryDelays) {
		return startSessionRetryDelays[attempt]
	}
	return startSessionRetryDelays[len(startSessionRetryDelays)-1]
}

func isRetryableStartStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func closeResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		log.Printf("copilot websocket handshake response close error: %v", err)
	}
}

func closeSessionConn(conn *websocket.Conn) {
	if conn == nil {
		return
	}
	if err := conn.Close(); err != nil {
		log.Printf("copilot websocket close error: %v", err)
	}
}

package copilot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	ErrCapacity = errors.New("copilot capacity exhausted")
	ErrTimeout  = errors.New("copilot upstream timeout")
)

// UpstreamError represents an error returned by the Copilot upstream.
type UpstreamError struct {
	Message string
}

func (e *UpstreamError) Error() string {
	return e.Message
}

func upstreamErrorf(format string, args ...any) *UpstreamError {
	return &UpstreamError{Message: fmt.Sprintf(format, args...)}
}

type BlockedError struct {
	Message string
}

func (e *BlockedError) Error() string {
	return e.Message
}

type CapacityError struct {
	Message string
	Cause   error
}

func (e *CapacityError) Error() string {
	if e == nil {
		return ErrCapacity.Error()
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return ErrCapacity.Error()
}

func (e *CapacityError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *CapacityError) Is(target error) bool {
	return target == ErrCapacity
}

type TimeoutError struct {
	Operation string
	Cause     error
}

func (e *TimeoutError) Error() string {
	if e == nil {
		return ErrTimeout.Error()
	}
	if e.Operation == "" {
		return ErrTimeout.Error()
	}
	return fmt.Sprintf("copilot %s timeout", e.Operation)
}

func (e *TimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *TimeoutError) Is(target error) bool {
	return target == ErrTimeout
}

func NewBlockedError(msg string) error {
	return &BlockedError{Message: msg}
}

func NewCapacityError(msg string, cause error) error {
	return &CapacityError{Message: msg, Cause: cause}
}

func NewTimeoutError(operation string, cause error) error {
	return &TimeoutError{Operation: operation, Cause: cause}
}

// IsBlocked checks if the error indicates a blocked user.
func IsBlocked(err error) bool {
	var blocked *BlockedError
	if errors.As(err, &blocked) {
		return true
	}

	var ue *UpstreamError
	if errors.As(err, &ue) {
		return strings.Contains(strings.ToLower(ue.Message), "blocked")
	}
	return false
}

func IsCapacity(err error) bool {
	if errors.Is(err, ErrCapacity) {
		return true
	}

	var ue *UpstreamError
	if errors.As(err, &ue) {
		msg := strings.ToLower(ue.Message)
		return strings.Contains(msg, "too-many-messages") || strings.Contains(msg, "too many messages")
	}

	return false
}

func IsTimeout(err error) bool {
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func IsClientCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

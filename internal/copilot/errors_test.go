package copilot

import (
	"context"
	"errors"
	"testing"
)

func TestErrors_ClassifyCapacityAndTimeout(t *testing.T) {
	t.Parallel()

	capacityErr := NewCapacityError("session capacity exhausted", context.DeadlineExceeded)
	if !errors.Is(capacityErr, ErrCapacity) {
		t.Fatal("capacity error does not match ErrCapacity")
	}
	if !errors.Is(capacityErr, context.DeadlineExceeded) {
		t.Fatal("capacity error does not unwrap to context deadline exceeded")
	}
	if !IsCapacity(capacityErr) {
		t.Fatal("IsCapacity() = false, want true for typed capacity error")
	}

	upstreamCapacityErr := &UpstreamError{Message: "upstream_error: too-many-messages"}
	if !IsCapacity(upstreamCapacityErr) {
		t.Fatal("IsCapacity() = false, want true for upstream too-many-messages")
	}

	timeoutErr := NewTimeoutError("read", context.DeadlineExceeded)
	if !errors.Is(timeoutErr, ErrTimeout) {
		t.Fatal("timeout error does not match ErrTimeout")
	}
	if !errors.Is(timeoutErr, context.DeadlineExceeded) {
		t.Fatal("timeout error does not unwrap to context deadline exceeded")
	}

	blockedErr := NewBlockedError("blocked upstream identity")
	if !IsBlocked(blockedErr) {
		t.Fatal("IsBlocked() = false, want true for blocked error")
	}
	if IsBlocked(errors.New("plain error")) {
		t.Fatal("IsBlocked() = true, want false for unrelated error")
	}
}

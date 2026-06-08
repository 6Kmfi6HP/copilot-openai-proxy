package copilot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionManager_AcquireRunsStartOutsideRegistryLock(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	releaseStart := make(chan struct{})
	manager := newSessionManager(8, func(ctx context.Context) (*SessionState, error) {
		close(started)
		<-releaseStart
		return &SessionState{ConversationID: "conv-lock", Connected: true}, nil
	})

	errCh := make(chan error, 1)
	go func() {
		lease, err := manager.acquire(context.Background())
		if err == nil {
			lease.Release()
		}
		errCh <- err
	}()

	<-started

	lockAcquired := make(chan struct{})
	go func() {
		manager.mu.Lock()
		manager.mu.Unlock()
		close(lockAcquired)
	}()

	select {
	case <-lockAcquired:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("manager mutex stayed locked while startSession was in flight")
	}

	close(releaseStart)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("acquire() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for acquire() to finish")
	}
}

func TestSessionManager_LeaseStateTransitions(t *testing.T) {
	t.Parallel()

	manager := newSessionManager(8, func(ctx context.Context) (*SessionState, error) {
		return &SessionState{ConversationID: "conv-lease", Connected: true}, nil
	})

	lease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}

	if state := manager.sessions["conv-lease"].status; state != sessionStatusLeased {
		t.Fatalf("state after acquire = %v, want %v", state, sessionStatusLeased)
	}

	lease.Release()

	manager.mu.RLock()
	_, ok := manager.sessions["conv-lease"]
	manager.mu.RUnlock()

	if ok {
		t.Fatal("released session remained in registry, want retirement")
	}
}

func TestSessionManager_RetiresUsedConversationBeforeReuse(t *testing.T) {
	t.Parallel()

	nextID := 0
	manager := newSessionManager(1, func(ctx context.Context) (*SessionState, error) {
		nextID++
		return &SessionState{
			ConversationID: fmt.Sprintf("conv-%d", nextID),
			Connected:      true,
		}, nil
	})

	firstLease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire() error = %v", err)
	}
	firstID := firstLease.Session().ConversationID
	firstLease.Release()

	secondLease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("second acquire() error = %v", err)
	}
	secondID := secondLease.Session().ConversationID
	secondLease.Release()

	if secondID == firstID {
		t.Fatalf("second conversationID = %q, want distinct fresh session", secondID)
	}
}

func TestWarmPool_MaintainsConfiguredIdleTarget(t *testing.T) {
	t.Parallel()

	started := make(chan string, 4)
	nextID := 0
	manager := newSessionManagerWithWarmPool(3, 1, func(ctx context.Context) (*SessionState, error) {
		nextID++
		id := fmt.Sprintf("conv-%d", nextID)
		started <- id
		return &SessionState{ConversationID: id, Connected: true}, nil
	})

	waitForWarmStart(t, started, "conv-1")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 1, idle: 1})

	lease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	if got := lease.Session().ConversationID; got != "conv-1" {
		t.Fatalf("leased conversationID = %q, want %q", got, "conv-1")
	}

	waitForWarmStart(t, started, "conv-2")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 1, idle: 1})

	lease.Release()
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 1, idle: 1})
}

func TestWarmPool_NeverExceedsMaxSessions(t *testing.T) {
	t.Parallel()

	started := make(chan string, 6)
	nextID := 0
	manager := newSessionManagerWithWarmPool(2, 2, func(ctx context.Context) (*SessionState, error) {
		nextID++
		id := fmt.Sprintf("conv-%d", nextID)
		started <- id
		return &SessionState{ConversationID: id, Connected: true}, nil
	})

	waitForWarmStart(t, started, "conv-1")
	waitForWarmStart(t, started, "conv-2")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, idle: 2})

	firstLease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire() error = %v", err)
	}
	secondLease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("second acquire() error = %v", err)
	}

	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 2})

	select {
	case id := <-started:
		t.Fatalf("unexpected extra warm start %q while max sessions are already live", id)
	case <-time.After(100 * time.Millisecond):
	}

	firstLease.Release()
	waitForWarmStart(t, started, "conv-3")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 1, idle: 1})

	secondLease.Release()
	waitForWarmStart(t, started, "conv-4")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, idle: 2})

	select {
	case id := <-started:
		t.Fatalf("unexpected extra warm start %q after warm target was restored", id)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSessionManager_CancelledAcquireLeavesNoDanglingState(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	manager := newSessionManager(8, func(ctx context.Context) (*SessionState, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := manager.acquire(ctx)
		errCh <- err
	}()

	<-started

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("acquire() error = nil, want cancellation error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for acquire() cancellation")
	}

	manager.mu.RLock()
	defer manager.mu.RUnlock()

	if got := len(manager.sessions); got != 0 {
		t.Fatalf("session count after cancelled acquire = %d, want 0", got)
	}
	if got := len(manager.byAge); got != 0 {
		t.Fatalf("age index count after cancelled acquire = %d, want 0", got)
	}
}

type sessionSnapshot struct {
	total  int
	idle   int
	leased int
}

func waitForWarmStart(t *testing.T, started <-chan string, want string) {
	t.Helper()

	select {
	case got := <-started:
		if got != want {
			t.Fatalf("warm start conversationID = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for warm start %q", want)
	}
}

func waitForSessionSnapshot(t *testing.T, manager *sessionManager, want sessionSnapshot) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := snapshotSessionManager(manager)
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("session snapshot = %+v, want %+v", snapshotSessionManager(manager), want)
}

func snapshotSessionManager(manager *sessionManager) sessionSnapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	got := sessionSnapshot{total: len(manager.sessions)}
	for _, entry := range manager.sessions {
		switch entry.status {
		case sessionStatusIdle:
			got.idle++
		case sessionStatusLeased:
			got.leased++
		}
	}
	return got
}

func TestSessionManager_AcquireParallelizesWarmups(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	releaseStart := make(chan struct{})
	manager := newSessionManager(2, func(ctx context.Context) (*SessionState, error) {
		started <- "started"
		<-releaseStart
		return &SessionState{ConversationID: time.Now().String(), Connected: true}, nil
	})

	errCh := make(chan error, 3)
	for range 3 {
		go func() {
			lease, err := manager.acquire(context.Background())
			if err == nil {
				lease.Release()
			}
			errCh <- err
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("warmup %d did not start before timeout", i+1)
		}
	}

	select {
	case <-started:
		t.Fatal("third warmup started before a capacity slot was released")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseStart)

	for i := 0; i < 3; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("acquire %d error = %v", i+1, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for acquire %d to finish", i+1)
		}
	}
}

func TestSessionManager_ReturnsCapacityErrorWhenBudgetExpires(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 2)
	releaseStart := make(chan struct{})
	manager := newSessionManager(1, func(ctx context.Context) (*SessionState, error) {
		started <- struct{}{}
		<-releaseStart
		return &SessionState{ConversationID: "conv-capacity", Connected: true}, nil
	})

	firstErrCh := make(chan error, 1)
	go func() {
		lease, err := manager.acquire(context.Background())
		if err == nil {
			defer lease.Release()
			<-time.After(150 * time.Millisecond)
		}
		firstErrCh <- err
	}()

	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	secondErrCh := make(chan error, 1)
	go func() {
		_, err := manager.acquire(ctx)
		secondErrCh <- err
	}()

	select {
	case err := <-secondErrCh:
		if !errors.Is(err, ErrCapacity) {
			t.Fatalf("acquire() error = %v, want ErrCapacity", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for capacity-limited acquire")
	}

	close(releaseStart)

	select {
	case err := <-firstErrCh:
		if err != nil {
			t.Fatalf("first acquire error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first acquire to finish")
	}
}

func TestSessionManager_ReleasesCapacityOnStartFailure(t *testing.T) {
	t.Parallel()

	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFailure := make(chan struct{})
	callCount := 0
	var callCountMu sync.Mutex

	manager := newSessionManager(1, func(ctx context.Context) (*SessionState, error) {
		callCountMu.Lock()
		callCount++
		currentCall := callCount
		callCountMu.Unlock()

		switch currentCall {
		case 1:
			close(firstStarted)
			<-releaseFailure
			return nil, errors.New("boom")
		case 2:
			close(secondStarted)
			return &SessionState{ConversationID: "conv-retry", Connected: true}, nil
		default:
			return &SessionState{ConversationID: "conv-extra", Connected: true}, nil
		}
	})

	firstErrCh := make(chan error, 1)
	go func() {
		_, err := manager.acquire(context.Background())
		firstErrCh <- err
	}()

	<-firstStarted

	secondErrCh := make(chan error, 1)
	go func() {
		lease, err := manager.acquire(context.Background())
		if err == nil {
			lease.Release()
		}
		secondErrCh <- err
	}()

	select {
	case <-secondStarted:
		t.Fatal("second start began before the failed warmup released capacity")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFailure)

	select {
	case err := <-firstErrCh:
		if err == nil {
			t.Fatal("first acquire error = nil, want start failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first acquire failure")
	}

	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second start did not begin after failed warmup released capacity")
	}

	select {
	case err := <-secondErrCh:
		if err != nil {
			t.Fatalf("second acquire error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second acquire success")
	}
}

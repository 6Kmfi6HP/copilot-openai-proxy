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
		session := &SessionState{ConversationID: "conv-lock"}
		session.setConnected(true)
		return session, nil
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
		session := &SessionState{ConversationID: "conv-lease"}
		session.setConnected(true)
		return session, nil
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
		session := &SessionState{ConversationID: fmt.Sprintf("conv-%d", nextID)}
		session.setConnected(true)
		return session, nil
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
		session := &SessionState{ConversationID: id}
		session.setConnected(true)
		return session, nil
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
		session := &SessionState{ConversationID: id}
		session.setConnected(true)
		return session, nil
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

func TestJanitor_EvictsExpiredIdleSessionsOnly(t *testing.T) {
	t.Parallel()

	started := make(chan string, 4)
	nextID := 0
	created := make(map[string]*SessionState)
	manager := newSessionManagerWithWarmPool(2, 1, func(ctx context.Context) (*SessionState, error) {
		nextID++
		id := fmt.Sprintf("conv-%d", nextID)
		session := &SessionState{
			ConversationID: id,
			CreatedAt:      time.Now(),
			LastUsedAt:     time.Now(),
		}
		session.setConnected(true)
		created[id] = session
		started <- id
		return session, nil
	})

	waitForWarmStart(t, started, "conv-1")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 1, idle: 1})

	lease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}

	waitForWarmStart(t, started, "conv-2")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 1, idle: 1})

	now := time.Now()
	backdateManagedSession(t, manager, "conv-1", now.Add(-2*time.Minute))
	backdateManagedSession(t, manager, "conv-2", now.Add(-2*time.Minute))

	manager.evictExpiredIdle(now, time.Minute)

	waitForWarmStart(t, started, "conv-3")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 1, idle: 1})

	if !created["conv-1"].IsConnected() {
		t.Fatal("leased session was closed by janitor, want preserved until release")
	}
	if created["conv-2"].IsConnected() {
		t.Fatal("expired idle session remained connected after janitor eviction")
	}

	lease.Release()
}

func TestClientClose_ClosesIdleAndWaitsForLeases(t *testing.T) {
	t.Parallel()

	started := make(chan string, 4)
	nextID := 0
	created := make(map[string]*SessionState)
	manager := newSessionManagerWithWarmPool(2, 1, func(ctx context.Context) (*SessionState, error) {
		nextID++
		id := fmt.Sprintf("conv-%d", nextID)
		session := &SessionState{
			ConversationID: id,
			CreatedAt:      time.Now(),
			LastUsedAt:     time.Now(),
		}
		session.setConnected(true)
		created[id] = session
		started <- id
		return session, nil
	})
	client := &Client{sessionMgr: manager}

	waitForWarmStart(t, started, "conv-1")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 1, idle: 1})

	lease, err := manager.acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire() error = %v", err)
	}

	waitForWarmStart(t, started, "conv-2")
	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 2, leased: 1, idle: 1})

	closeErrCh := make(chan error, 1)
	go func() {
		closeErrCh <- client.Close(context.Background())
	}()

	waitForSessionSnapshot(t, manager, sessionSnapshot{total: 1, leased: 1})
	if created["conv-2"].IsConnected() {
		t.Fatal("idle session remained connected after Client.Close()")
	}

	select {
	case err := <-closeErrCh:
		t.Fatalf("Client.Close() returned early with %v, want wait for leased session release", err)
	case <-time.After(100 * time.Millisecond):
	}

	lease.Release()

	select {
	case err := <-closeErrCh:
		if err != nil {
			t.Fatalf("Client.Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Client.Close() to finish after release")
	}

	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("second Client.Close() error = %v, want nil", err)
	}

	waitForSessionSnapshot(t, manager, sessionSnapshot{})
	if created["conv-1"].IsConnected() {
		t.Fatal("leased session remained connected after release during shutdown")
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

func backdateManagedSession(t *testing.T, manager *sessionManager, conversationID string, ts time.Time) {
	t.Helper()

	manager.mu.Lock()
	defer manager.mu.Unlock()

	entry, ok := manager.sessions[conversationID]
	if !ok {
		t.Fatalf("session %q not found in manager", conversationID)
	}

	entry.updatedAt = ts
	if entry.session != nil {
		entry.session.LastUsedAt = ts
	}
}

func TestSessionManager_AcquireParallelizesWarmups(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	releaseStart := make(chan struct{})
	manager := newSessionManager(2, func(ctx context.Context) (*SessionState, error) {
		started <- "started"
		<-releaseStart
		session := &SessionState{ConversationID: time.Now().String()}
		session.setConnected(true)
		return session, nil
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
		session := &SessionState{ConversationID: "conv-capacity"}
		session.setConnected(true)
		return session, nil
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
			session := &SessionState{ConversationID: "conv-retry"}
			session.setConnected(true)
			return session, nil
		default:
			session := &SessionState{ConversationID: "conv-extra"}
			session.setConnected(true)
			return session, nil
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

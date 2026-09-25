package heartbeat

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// ─── Mock Redis Client ───

// mockRedisClient is shared by publisher goroutines, so it locks like the
// real client would be safe to use concurrently.
type mockRedisClient struct {
	mu    sync.Mutex
	store map[string]mockEntry
}

// entry reads one key under the lock.
func (m *mockRedisClient) entry(key string) (mockEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.store[key]
	return e, ok
}

type mockEntry struct {
	value string
	ttl   time.Duration
}

func newMockRedis() *mockRedisClient {
	return &mockRedisClient{store: make(map[string]mockEntry)}
}

func (m *mockRedisClient) Set(_ context.Context, key string, value interface{}, expiration time.Duration) *goredis.StatusCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[key] = mockEntry{value: fmt.Sprintf("%v", value), ttl: expiration}
	cmd := goredis.NewStatusCmd(context.Background())
	cmd.SetVal("OK")
	return cmd
}

func (m *mockRedisClient) Get(_ context.Context, key string) *goredis.StringCmd {
	cmd := goredis.NewStringCmd(context.Background())
	entry, ok := m.entry(key)
	if !ok {
		cmd.SetErr(goredis.Nil)
		return cmd
	}
	cmd.SetVal(entry.value)
	return cmd
}

func (m *mockRedisClient) Keys(_ context.Context, _ string) *goredis.StringSliceCmd {
	cmd := goredis.NewStringSliceCmd(context.Background())
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.store {
		keys = append(keys, k)
	}
	cmd.SetVal(keys)
	return cmd
}

// ─── Publisher Tests ───

func TestPublisher_SetsKey(t *testing.T) {
	mock := newMockRedis()
	pub := NewPublisher("mdengine", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run in background, let it publish one beat
	go pub.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Verify the key was set
	entry, ok := mock.entry(KeyPrefix+"mdengine")
	if !ok {
		t.Fatal("expected heartbeat key to be set")
	}
	if entry.ttl != TTL {
		t.Errorf("expected TTL %v, got %v", TTL, entry.ttl)
	}
	// Value should be a unix timestamp in milliseconds
	var ts int64
	fmt.Sscanf(entry.value, "%d", &ts)
	if ts <= 0 {
		t.Errorf("expected positive timestamp, got %d", ts)
	}
}

func TestPublisher_MultipleBeatsDifferentServices(t *testing.T) {
	mock := newMockRedis()
	pub1 := NewPublisher("mdengine", mock)
	pub2 := NewPublisher("indengine", mock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pub1.Run(ctx)
	go pub2.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()

	if _, ok := mock.entry(KeyPrefix+"mdengine"); !ok {
		t.Error("expected mdengine heartbeat key")
	}
	if _, ok := mock.entry(KeyPrefix+"indengine"); !ok {
		t.Error("expected indengine heartbeat key")
	}
}

// ─── Aggregator Tests ───

func TestAggregator_AllUp(t *testing.T) {
	mock := newMockRedis()
	now := time.Now().UnixMilli()

	// Simulate all services having recent heartbeats
	services := []string{"mdengine", "indengine", "stratengine"}
	for _, svc := range services {
		mock.Set(context.Background(), KeyPrefix+svc, fmt.Sprintf("%d", now), TTL)
	}

	agg := NewAggregator(mock, services)
	statuses := agg.Status(context.Background())

	if len(statuses) != 3 {
		t.Fatalf("expected 3 statuses, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s.Status != "up" {
			t.Errorf("expected %s to be up, got %s", s.Name, s.Status)
		}
		if s.LastBeat == 0 {
			t.Errorf("expected LastBeat for %s to be set", s.Name)
		}
	}

	if !agg.AllHealthy(context.Background()) {
		t.Error("expected AllHealthy to return true")
	}
}

func TestAggregator_ServiceDown(t *testing.T) {
	mock := newMockRedis()
	now := time.Now().UnixMilli()

	// mdengine is up, indengine is missing
	mock.Set(context.Background(), KeyPrefix+"mdengine", fmt.Sprintf("%d", now), TTL)

	agg := NewAggregator(mock, []string{"mdengine", "indengine"})
	statuses := agg.Status(context.Background())

	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	// Check mdengine is up
	if statuses[0].Name != "mdengine" || statuses[0].Status != "up" {
		t.Errorf("expected mdengine up, got %s %s", statuses[0].Name, statuses[0].Status)
	}

	// Check indengine is down (key missing)
	if statuses[1].Name != "indengine" || statuses[1].Status != "down" {
		t.Errorf("expected indengine down, got %s %s", statuses[1].Name, statuses[1].Status)
	}

	if agg.AllHealthy(context.Background()) {
		t.Error("expected AllHealthy to return false")
	}
}

func TestAggregator_StaleHeartbeat(t *testing.T) {
	mock := newMockRedis()
	// Set a heartbeat from 20 seconds ago (beyond the 15s TTL threshold)
	staleTs := time.Now().Add(-20 * time.Second).UnixMilli()
	mock.Set(context.Background(), KeyPrefix+"mdengine", fmt.Sprintf("%d", staleTs), TTL)

	agg := NewAggregator(mock, []string{"mdengine"})
	statuses := agg.Status(context.Background())

	if statuses[0].Status != "down" {
		t.Errorf("expected stale heartbeat to show as down, got %s", statuses[0].Status)
	}
	if statuses[0].AgeMs < 19000 { // at least 19s old
		t.Errorf("expected age > 19000ms, got %d", statuses[0].AgeMs)
	}
}

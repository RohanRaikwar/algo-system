package heartbeat

import (
	"context"
	"fmt"
	"log"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

const (
	// KeyPrefix is the Redis key prefix for heartbeat entries.
	KeyPrefix = "heartbeat:"
	// PublishInterval is how often each service publishes its heartbeat.
	PublishInterval = 5 * time.Second
	// TTL is the expiry on each heartbeat key. If a service misses 3 beats, it's considered down.
	TTL = 15 * time.Second
)

// KnownServices lists all service names that should be tracked.
var KnownServices = []string{
	"mdengine",
	"indengine",
	"stratengine",
	"analyst",
	"api_gateway",
}

// ─── Publisher ───

// Publisher publishes periodic heartbeats to Redis for a single service.
type Publisher struct {
	serviceName string
	rdb         RedisClient
}

// RedisClient is the minimal interface needed for heartbeat operations.
// This enables unit testing with a mock.
type RedisClient interface {
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *goredis.StatusCmd
	Get(ctx context.Context, key string) *goredis.StringCmd
	Keys(ctx context.Context, pattern string) *goredis.StringSliceCmd
}

// NewPublisher creates a heartbeat publisher for the given service.
func NewPublisher(serviceName string, rdb RedisClient) *Publisher {
	return &Publisher{
		serviceName: serviceName,
		rdb:         rdb,
	}
}

// Run publishes heartbeats every PublishInterval until ctx is cancelled.
// Should be called as a goroutine: go publisher.Run(ctx)
func (p *Publisher) Run(ctx context.Context) {
	key := KeyPrefix + p.serviceName
	ticker := time.NewTicker(PublishInterval)
	defer ticker.Stop()

	// Publish immediately on start
	p.publish(ctx, key)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.publish(ctx, key)
		}
	}
}

func (p *Publisher) publish(ctx context.Context, key string) {
	val := fmt.Sprintf("%d", time.Now().UnixMilli())
	if err := p.rdb.Set(ctx, key, val, TTL).Err(); err != nil {
		log.Printf("[heartbeat] %s: publish failed: %v", p.serviceName, err)
	}
}

// ─── Aggregator ───

// ServiceHealth represents the health of a single service.
type ServiceHealth struct {
	Name     string `json:"name"`
	Status   string `json:"status"`    // "up" or "down"
	LastBeat int64  `json:"last_beat"` // unix milliseconds (0 if never seen)
	AgeMs    int64  `json:"age_ms"`    // milliseconds since last heartbeat
}

// Aggregator checks heartbeat keys to determine service health.
type Aggregator struct {
	rdb      RedisClient
	services []string
}

// NewAggregator creates a heartbeat aggregator for the given list of services.
func NewAggregator(rdb RedisClient, services []string) *Aggregator {
	return &Aggregator{
		rdb:      rdb,
		services: services,
	}
}

// Status returns the health status of all tracked services.
func (a *Aggregator) Status(ctx context.Context) []ServiceHealth {
	now := time.Now().UnixMilli()
	results := make([]ServiceHealth, 0, len(a.services))

	for _, svc := range a.services {
		key := KeyPrefix + svc
		val, err := a.rdb.Get(ctx, key).Result()
		if err != nil {
			// Key missing or expired = service down
			results = append(results, ServiceHealth{
				Name:   svc,
				Status: "down",
			})
			continue
		}

		var lastBeat int64
		fmt.Sscanf(val, "%d", &lastBeat)
		age := now - lastBeat

		status := "up"
		if age > TTL.Milliseconds() {
			status = "down"
		}

		results = append(results, ServiceHealth{
			Name:     svc,
			Status:   status,
			LastBeat: lastBeat,
			AgeMs:    age,
		})
	}

	return results
}

// AllHealthy returns true if all tracked services have an "up" status.
func (a *Aggregator) AllHealthy(ctx context.Context) bool {
	for _, h := range a.Status(ctx) {
		if h.Status != "up" {
			return false
		}
	}
	return true
}

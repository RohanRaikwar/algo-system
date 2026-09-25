package analyst

import (
	"context"
	"encoding/json"
	"log"
	"time"

	redisstore "trading-systemv1/internal/store/redis"
)

// Publisher handles publishing analyst events to Redis PubSub and key-value storage.
type Publisher struct {
	writer *redisstore.Writer
}

// NewPublisher creates a new publisher backed by the given Redis writer.
func NewPublisher(writer *redisstore.Writer) *Publisher {
	return &Publisher{writer: writer}
}

// PublishState publishes a market state event to PubSub and stores the latest value.
func (p *Publisher) PublishState(ctx context.Context, event MarketStateEvent) {
	payload := event.JSON()

	// PubSub for real-time subscribers
	if err := p.writer.Client().Publish(ctx, "pub:analyst:state", string(payload)).Err(); err != nil {
		log.Printf("[analyst] state publish error: %v", err)
	}

	// Store latest for polling / API access
	if err := p.writer.Client().Set(ctx, "analyst:state:latest", payload, 24*time.Hour).Err(); err != nil {
		log.Printf("[analyst] state store error: %v", err)
	}
}

// PublishLevels publishes S/R level updates to PubSub and stores the latest value.
func (p *Publisher) PublishLevels(ctx context.Context, event LevelUpdateEvent) {
	payload := event.JSON()

	if err := p.writer.Client().Publish(ctx, "pub:analyst:levels", string(payload)).Err(); err != nil {
		log.Printf("[analyst] levels publish error: %v", err)
	}

	if err := p.writer.Client().Set(ctx, "analyst:levels:latest", payload, 24*time.Hour).Err(); err != nil {
		log.Printf("[analyst] levels store error: %v", err)
	}
}

// PublishBreakout publishes a breakout event to PubSub and stores the latest value.
func (p *Publisher) PublishBreakout(ctx context.Context, event BreakoutEvent) {
	payload := event.JSON()

	if err := p.writer.Client().Publish(ctx, "pub:analyst:breakout", string(payload)).Err(); err != nil {
		log.Printf("[analyst] breakout publish error: %v", err)
	}

	if err := p.writer.Client().Set(ctx, "analyst:breakout:latest", payload, 24*time.Hour).Err(); err != nil {
		log.Printf("[analyst] breakout store error: %v", err)
	}
}

// PublishSummary publishes a combined summary of the current analysis state.
func (p *Publisher) PublishSummary(ctx context.Context, state MarketStateEvent, levels LevelUpdateEvent, breakout *BreakoutEvent) {
	summary := map[string]interface{}{
		"state":  state,
		"levels": levels,
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if breakout != nil {
		summary["breakout"] = breakout
	}

	payload, err := json.Marshal(summary)
	if err != nil {
		log.Printf("[analyst] summary marshal error: %v", err)
		return
	}

	if err := p.writer.Client().Set(ctx, "analyst:summary:latest", payload, 24*time.Hour).Err(); err != nil {
		log.Printf("[analyst] summary store error: %v", err)
	}
}

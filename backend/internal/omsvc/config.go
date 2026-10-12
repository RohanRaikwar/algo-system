// Package omsvc is the standalone order-management process (cmd/oms). It
// consumes strategy intents from Redis, runs them through an order
// executor, journals them, and publishes order events.
//
// Phase 3 (shadow): the executor is hard-wired to paper mode. stratengine
// still places every real order; this process runs alongside so its
// results can be compared before it takes over order placement.
package omsvc

import (
	"time"

	"trading-systemv1/config"
)

// Config for the OMS process.
type Config struct {
	RedisAddr     string
	RedisPassword string
	Group         string // consumer group on the intent stream
	Consumer      string // consumer name within the group
	JournalPath   string // SQLite order journal + claimed intents
	SnapshotKey   string // Redis key for executor position state
	PositionsKey  string // Redis key the dashboard can read
	LeaderKey     string // Redis lock: one OMS instance at a time
	LeaderTTL     time.Duration
	EntryTTL      time.Duration // entry intents older than this are skipped

	Qty                   int64
	FNOExchange           string
	PaperSlippageBps      int64
	PaperSlippageMinPaise int64
}

// LoadConfig reads OMS_* variables; executor sizing shares the STRAT_*
// variables so paper results match stratengine's.
func LoadConfig() Config {
	return Config{
		RedisAddr:     config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: config.GetEnv("REDIS_PASSWORD", ""),
		Group:         config.GetEnv("OMS_CONSUMER_GROUP", "oms"),
		Consumer:      config.GetEnv("OMS_CONSUMER_NAME", "oms-1"),
		JournalPath:   config.GetEnv("OMS_JOURNAL_PATH", "data/oms_orders.db"),
		SnapshotKey:   config.GetEnv("OMS_SNAPSHOT_KEY", "snapshot:oms"),
		PositionsKey:  config.GetEnv("OMS_POSITIONS_KEY", "oms:positions"),
		LeaderKey:     config.GetEnv("OMS_LEADER_KEY", "oms:leader"),
		LeaderTTL:     15 * time.Second,
		EntryTTL:      time.Duration(config.GetEnvInt("OMS_ENTRY_TTL_S", 30)) * time.Second,

		Qty:                   config.GetEnvInt64("STRAT_QTY", 1),
		FNOExchange:           config.GetEnv("STRAT_FNO_EXCHANGE", "NFO"),
		PaperSlippageBps:      config.GetEnvInt64("STRAT_PAPER_SLIPPAGE_BPS", 50),
		PaperSlippageMinPaise: config.GetEnvInt64("STRAT_PAPER_SLIPPAGE_MIN_PAISE", 50),
	}
}

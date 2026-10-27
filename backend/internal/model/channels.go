package model

// Redis channel and key names shared between processes.
//
// The five services talk only through Redis, so these names are the wire
// contract. Every "pub:", "cmd:" and "config:" name lives here; a test
// (channels_literal_test.go) fails on such a literal anywhere else under
// internal/. A channel the browser sees must also be in the gateway's
// dynamicPubSubPatterns (internal/gateway/pubsub.go).
//
// Full-state channels come with a snapshot key holding the same payload: the
// key serves REST and SNAPSHOT after a reconnect, the channel pushes changes.

// Market data (store/redis). Dynamic suffixes are added by the publisher.
const (
	TickChannelPrefix   = "pub:tick:"    // + exchange:token
	CandleChannelPrefix = "pub:candle:"  // + <tf>s:exchange:token (internal only)
	IndChannelPrefix    = "pub:ind:"     // + name:<tf>s:exchange:token
	AnalystChannelPref  = "pub:analyst:" // + state | levels | breakout | reversal
	TickChannelPattern  = TickChannelPrefix + "*"
	MarketStateChannel  = "pub:market:state" // internal only: "open" / "closed"
	MarketStateKey      = "market:state"
	CandleChannelAll    = CandleChannelPrefix + "*"
	Candle1sChannelPref = CandleChannelPrefix + "1s:"
)

// stratengine dashboard state, forwarded to the browser.
const (
	SignalChannel = "pub:signal" // no key; the gateway keeps a replay ring

	OrdersChannel = "pub:orders"
	OrdersKey     = "orders:live"

	PnLChannel = "pub:pnl"
	PnLKey     = "pnl:summary"

	StrikeChannel = "pub:strike"
	StrikeKey     = "strike:info"

	SRStateChannel = "pub:sr"
	SRStateKey     = "sr:state"

	// Extra underlyings' SR views (SENSEX_SR, …), one map by index key.
	SRXStateChannel = "pub:srx"
	SRXStateKey     = "srx:state"

	RefusedChannel = "pub:refused"
	RefusedKey     = "refused:state"

	StrikeSelChannel = "pub:strikesel"
	StrikeSelKey     = "strikesel:state"

	// Extra underlyings' strike selection views, one map by index key.
	StrikeSelXChannel = "pub:strikeselx"
	StrikeSelXKey     = "strikeselx:state"
)

// stratengine → exitwatch, internal only.
const (
	PosContextChannel = "pub:poscontext"
	PosContextKey     = "poscontext:latest"
)

// analyst (and exitwatch reversal), forwarded under pub:analyst:*.
const (
	AnalystStateChannel    = AnalystChannelPref + "state"
	AnalystStateKey        = "analyst:state:latest"
	AnalystLevelsChannel   = AnalystChannelPref + "levels"
	AnalystLevelsKey       = "analyst:levels:latest"
	AnalystBreakoutChannel = AnalystChannelPref + "breakout"
	AnalystBreakoutKey     = "analyst:breakout:latest"
	AnalystSummaryKey      = "analyst:summary:latest" // key only, never published
	ReversalChannel        = AnalystChannelPref + "reversal"
	ReversalKey            = "analyst:reversal:latest"
)

// Commands between services, internal only.
const (
	// ExitRequestChannel carries exitwatch's requests to close a position
	// (stratengine acts on them for the strategies it allows).
	ExitRequestChannel = "cmd:exitwatch:exit"

	// PositionCommandChannel carries the dashboard's hand edits of an open
	// paper position: move its stop/target or exit it (gateway →
	// stratengine).
	PositionCommandChannel = "cmd:position"

	// TGSignalChannel carries option calls parsed from Telegram group
	// messages (tgsignal → stratengine), payload TGSignal.
	TGSignalChannel = "cmd:tgsignal"
	// TGFollowUpChannel carries the caller's replies to a call ("exit",
	// "SL hit", "SL cost") read by the LLM (tgsignal → stratengine),
	// payload TGFollowUp.
	TGFollowUpChannel = "cmd:tgfollowup"
	// TGOutcomeChannel reports what stratengine did with each call
	// (armed, entered, exited, refused) back to tgsignal's review log,
	// payload TGOutcome.
	TGOutcomeChannel = "cmd:tgsignal:outcome"
	// TGStatusKey holds tgsignal's health (model.TGStatus), refreshed every
	// 15s with a 60s expiry: a missing key means tgsignal is not running.
	// TGMessagesKey is a list of the latest messages read (model.TGMessageView,
	// newest first); TGOutcomesKey a hash of call key → outcome lines;
	// TGTimelineKey a hash of call key → JSON []model.TGStage. The gateway
	// serves them at /api/telegram. Keys only.
	TGStatusKey   = "tgsignal:status"
	TGMessagesKey = "tgsignal:messages"
	TGOutcomesKey = "tgsignal:outcomes"
	TGTimelineKey = "tgsignal:timeline"
	// TGPositionKey holds TELEGRAM_SIGNAL's open trade (strategy.TGPosition)
	// so a stratengine restart keeps the call's stop. Key only. A channel
	// strategy (TG_VIKASH, …) uses TGPositionKeyFor.
	TGPositionKey = "tgsignal:position"

	// TradingConfigChannel announces a dashboard config change stored in
	// TradingConfigKey (gateway → stratengine).
	TradingConfigChannel = "cmd:config_update"
	TradingConfigKey     = "trading:config"

	// ExitPolicyReloadChannel reloads the exit policy. No service publishes
	// it; an operator does (redis-cli PUBLISH).
	ExitPolicyReloadChannel = "cmd:exitpolicy:reload"

	// SubscribeTokenChannel asks mdengine to subscribe extra tokens
	// (stratengine → mdengine).
	SubscribeTokenChannel = "cmd:subscribe_token"

	// IndicatorConfigChannel pushes indicator config (gateway → indengine).
	IndicatorConfigChannel = "config:indicators"
)

// TGPositionKeyFor is the saved-trade key of a Telegram strategy:
// TGPositionKey for TELEGRAM_SIGNAL, "tgsignal:position:<NAME>" for a
// channel strategy.
func TGPositionKeyFor(name string) string {
	if name == "" || name == "TELEGRAM_SIGNAL" {
		return TGPositionKey
	}
	return TGPositionKey + ":" + name
}

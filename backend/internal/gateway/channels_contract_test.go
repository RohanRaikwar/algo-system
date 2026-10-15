package gateway

import (
	"path"
	"testing"

	"trading-systemv1/internal/model"
)

// forwarded reports whether ch reaches the browser through a
// dynamicPubSubPatterns entry (exact name or PSubscribe glob).
func forwarded(ch string) bool {
	for _, p := range dynamicPubSubPatterns {
		if ok, _ := path.Match(p, ch); ok {
			return true
		}
	}
	return false
}

// TestChannelListsAgree keeps the gateway's per-channel lists consistent:
// every channel given special delivery treatment must actually be forwarded.
func TestChannelListsAgree(t *testing.T) {
	for _, lc := range liveStateChannels {
		if !forwarded(lc.channel) {
			t.Errorf("liveStateChannels %s not in dynamicPubSubPatterns", lc.channel)
		}
		if !isStickyChannel(lc.channel) {
			t.Errorf("liveStateChannels %s must be sticky", lc.channel)
		}
		if !isLatestWins(lc.channel) {
			t.Errorf("liveStateChannels %s must be latest-wins", lc.channel)
		}
	}
	for ch := range latestWinsChannels {
		if !forwarded(ch) {
			t.Errorf("latestWinsChannels %s not in dynamicPubSubPatterns", ch)
		}
		if !isStickyChannel(ch) {
			t.Errorf("latestWinsChannels %s must be sticky", ch)
		}
	}
	for ch := range losslessChannels {
		if !forwarded(ch) {
			t.Errorf("losslessChannels %s not in dynamicPubSubPatterns", ch)
		}
	}
}

// TestEveryChannelClassified makes adding a channel a deliberate choice:
// each one in model/channels.go is either forwarded to the browser or listed
// here as internal only.
func TestEveryChannelClassified(t *testing.T) {
	browser := []string{
		model.SignalChannel, model.OrdersChannel, model.PnLChannel, model.StrikeChannel,
		model.SRStateChannel, model.SRXStateChannel, model.RefusedChannel,
		model.StrikeSelChannel, model.StrikeSelXChannel,
		model.AnalystStateChannel, model.AnalystLevelsChannel,
		model.AnalystBreakoutChannel, model.ReversalChannel,
		model.TickChannelPrefix + "NSE:99926000",
		model.IndChannelPrefix + "EMA_9:60s:NSE:99926000",
	}
	internal := []string{
		model.MarketStateChannel, model.PosContextChannel, model.ExitRequestChannel,
		model.TradingConfigChannel, model.ExitPolicyReloadChannel,
		model.SubscribeTokenChannel, model.IndicatorConfigChannel,
		// Candles reach the browser via the hub's explicit per-token list
		// (buildChannels), not via a pattern.
		model.CandleChannelPrefix + "60s:NSE:99926000",
	}
	for _, ch := range browser {
		if !forwarded(ch) {
			t.Errorf("%s should reach the browser but is not in dynamicPubSubPatterns", ch)
		}
	}
	for _, ch := range internal {
		if forwarded(ch) {
			t.Errorf("%s is internal only but matches dynamicPubSubPatterns", ch)
		}
	}
}

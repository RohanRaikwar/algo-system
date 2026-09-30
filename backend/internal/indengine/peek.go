package indengine

import (
	"context"
	"log"
)

// peekLoop subscribes to 1s candle PubSub for live indicator previews of
// the SUBSCRIBE_TOKENS instruments (the ones with closed-candle indicators).
func (svc *Service) peekLoop(ctx context.Context) {
	if err := svc.redisReader.Subscribe1sForPeek(ctx, svc.cfg.EnabledTFs, svc.cfg.SubscribeTokenKeys, svc.tfCandleCh); err != nil {
		log.Printf("[indengine] 1s peek subscription error: %v", err)
	}
}

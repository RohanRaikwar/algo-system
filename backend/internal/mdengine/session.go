package mdengine

import (
	"context"
	"log"
	"time"

	"github.com/pquerna/otp/totp"

	"trading-systemv1/internal/marketdata/closedetector"
	"trading-systemv1/internal/marketdata/ws"
	"trading-systemv1/internal/marketdata/wssim"
	"trading-systemv1/internal/markethours"
	smartconnect "trading-systemv1/pkg/smartconnect"
)

// feedSubscribeMode is the Angel One WebSocket mode for every subscription
// (base and dynamic). Quote (2) carries last traded quantity and the
// cumulative day volume that candle volume is built from; LTP (1) carries
// price only, which left every candle's volume at 0.
const feedSubscribeMode = smartconnect.ModeQuote

// Pause before re-login after the feed was lost mid-session; doubles while
// sessions keep failing fast, capped at feedRetryMaxDelay.
const (
	feedRetryDelay    = 5 * time.Second
	feedRetryMaxDelay = 60 * time.Second
)

// runStagingSession connects to the tickserver via wssim.
func (s *Service) runStagingSession(ctx context.Context) {
	log.Printf("[mdengine] staging tick source: %s", s.cfg.SimWSURL)

	ingest, err := s.newStagingIngest()
	if err != nil {
		log.Fatalf("[mdengine] wssim init failed: %v", err)
	}

	go func() {
		if err := ingest.Start(ctx, s.tickCh); err != nil {
			log.Printf("[mdengine] wssim error: %v", err)
			s.health.SetWSConnected(false)
		}
	}()

	log.Println("[mdengine] ╔════════════════════════════════════════════════════════════════╗")
	log.Println("[mdengine] ║  Market Data Engine (MS1) — STAGING MODE                      ║")
	log.Println("[mdengine] ║                                                               ║")
	log.Println("[mdengine] ║  [TickServer WS] → [1s Agg] → [TF Builder] → [Redis/SQLite]   ║")
	log.Printf("[mdengine] ║  TFs: %-56v ║", s.cfg.EnabledTFs)
	log.Printf("[mdengine] ║  Source: %-52s ║", s.cfg.SimWSURL)
	log.Println("[mdengine] ║  No Angel One credentials required                             ║")
	log.Println("[mdengine] ╚════════════════════════════════════════════════════════════════╝")
}

// newStagingIngest builds the sim ingest with the same metric hooks as production
// (TicksTotal, E2E latency, ws_tick drops). The sim feed has no sequence numbers,
// so there is no seq-gap hook.
func (s *Service) newStagingIngest() (*wssim.Ingest, error) {
	ingest, err := wssim.New(wssim.Config{
		URL:               s.cfg.SimWSURL,
		ReconnectDelay:    2 * time.Second,
		MaxReconnectDelay: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	ingest.OnReconnect = func() {
		s.prom.WSReconnects.Inc()
	}
	ingest.OnIngested = s.recordIngest
	ingest.OnDrop = s.ctr.dropWSTick.Inc
	ingest.OnClockSkew = s.ctr.clockSkew.Inc
	ingest.OnConnState = s.health.SetWSConnected
	return ingest, nil
}

// feedLostMidSession reports whether the WS session ended because the feed
// was lost (connect failed or the socket gave up) on a trading day before the
// close — incl. the 9:14 pre-open window — so the session loop must back off,
// re-login and reconnect instead of running the market-close path.
// wsCtxErr non-nil means close detection or the hard deadline ended it.
func feedLostMidSession(startErr, wsCtxErr error, now time.Time) bool {
	return startErr != nil && wsCtxErr == nil &&
		markethours.IsTradingDay(now) && now.Before(markethours.TodayClose(now))
}

// rememberDynamicTokens records dynamically subscribed tokens so a re-login
// (new socket) subscribes them again.
func (s *Service) rememberDynamicTokens(tokens []smartconnect.TokenListEntry) {
	s.dynMu.Lock()
	s.dynTokens = append(s.dynTokens, tokens...)
	s.dynMu.Unlock()
}

// sessionTokenList is the configured token list plus remembered dynamic tokens.
func (s *Service) sessionTokenList() []smartconnect.TokenListEntry {
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	out := make([]smartconnect.TokenListEntry, 0, len(s.cfg.TokenList)+len(s.dynTokens))
	out = append(out, s.cfg.TokenList...)
	return append(out, s.dynTokens...)
}

// runProductionSession manages the Angel One WS lifecycle with market hours gating.
func (s *Service) runProductionSession(ctx context.Context) {
	go func() {
		loginBackoff := 30 * time.Second
		feedBackoff := feedRetryDelay

		for {
			// Wait for pre-market warm-up time (9:10 AM)
			now := time.Now()

			// Mid-session startup: if market is currently open, connect immediately
			midSession := markethours.IsMarketOpen(now)
			if midSession {
				log.Printf("[mdengine] 🟢 market is OPEN — connecting immediately (mid-session startup)")
			} else {
				nextPreOpen := markethours.NextPreOpen(now)
				if now.Before(nextPreOpen) {
					wait := nextPreOpen.Sub(now)
					log.Printf("[mdengine] ⏸ market closed. %s", markethours.StatusString(now))
					log.Printf("[mdengine] sleeping %v until pre-open %s",
						wait.Truncate(time.Second), nextPreOpen.In(markethours.IST).Format("Mon 15:04"))
					s.health.SetWSConnected(false)
					s.prom.MarketState.Set(0)

					select {
					case <-ctx.Done():
						return
					case <-time.After(wait):
					}
				}
			}

			// Determine next open time
			var nextOpen time.Time
			if midSession {
				ist := now.In(markethours.IST)
				nextOpen = time.Date(ist.Year(), ist.Month(), ist.Day(), 9, 15, 0, 0, markethours.IST)
			} else {
				nextOpen = markethours.NextOpen(now)
			}

			// Fresh login at ~9:10 AM (pre-market)
			log.Println("[mdengine] 🔑 pre-market warm-up — generating fresh session...")
			s.prom.SessionTransitions.WithLabelValues("open").Inc()

			totpCode, err := totp.GenerateCode(s.cfg.AngelTOTPSecret, time.Now())
			if err != nil {
				log.Printf("[mdengine] TOTP generation failed: %v, retrying in %v", err, loginBackoff)
				time.Sleep(loginBackoff)
				loginBackoff = minDur(loginBackoff*2, 5*time.Minute)
				continue
			}

			sc := smartconnect.NewSmartConnect(smartconnect.Config{
				APIKey: s.cfg.AngelAPIKey,
				Debug:  false,
			})
			userResp, err := sc.GenerateSession(s.cfg.AngelClientCode, s.cfg.AngelPassword, totpCode)
			if err != nil {
				log.Printf("[mdengine] login failed: %v, retrying in %v", err, loginBackoff)
				time.Sleep(loginBackoff)
				loginBackoff = minDur(loginBackoff*2, 5*time.Minute)
				continue
			}

			feedToken := sc.GetFeedToken()
			authToken := ""
			if data, ok := userResp["data"].(map[string]interface{}); ok {
				if jwt, ok := data["jwtToken"].(string); ok {
					authToken = jwt
				}
			}
			if feedToken == "" || authToken == "" {
				log.Printf("[mdengine] empty tokens from session, retrying in %v", loginBackoff)
				time.Sleep(loginBackoff)
				loginBackoff = minDur(loginBackoff*2, 5*time.Minute)
				continue
			}
			loginBackoff = 30 * time.Second // reset on success
			log.Printf("[mdengine] ✅ session ready, feedToken=%s...", feedToken[:minInt(10, len(feedToken))])

			// Wait until WS connect time (9:14 AM) — skip if mid-session
			if !midSession {
				wsTime := markethours.WSConnectTime(nextOpen)
				if wait := time.Until(wsTime); wait > 0 {
					log.Printf("[mdengine] ⏳ waiting %v to connect WS at %s",
						wait.Truncate(time.Second), wsTime.In(markethours.IST).Format("15:04"))
					select {
					case <-ctx.Done():
						return
					case <-time.After(wait):
					}
				}
			}

			// Connect WS with close detection
			closeTime := markethours.TodayClose(time.Now())
			detector := closedetector.New(closeTime)
			wsDeadline := closeTime.Add(detector.MaxGrace)
			wsCtx, wsCancel := context.WithDeadline(ctx, wsDeadline)

			ingest, err := ws.New(ws.IngestConfig{
				AuthToken:     authToken,
				APIKey:        s.cfg.AngelAPIKey,
				ClientCode:    s.cfg.AngelClientCode,
				FeedToken:     feedToken,
				SubscribeMode: feedSubscribeMode,
				TokenList:     s.sessionTokenList(),
			})
			if err != nil {
				log.Printf("[mdengine] ws init failed: %v, retrying in 30s", err)
				wsCancel()
				time.Sleep(30 * time.Second)
				continue
			}

			ingest.OnReconnect = func() {
				s.prom.WSReconnects.Inc()
			}
			ingest.OnIngested = s.recordIngest
			ingest.OnDrop = s.ctr.dropWSTick.Inc
			ingest.OnSeqGap = func(token string, missed int64) {
				s.prom.FeedSeqGaps.Inc()
				s.prom.FeedSeqMissed.Add(float64(missed))
			}
			ingest.OnClockSkew = s.ctr.clockSkew.Inc
			ingest.OnConnState = s.health.SetWSConnected // follows the socket, incl. reconnects

			// Close detection follows one instrument: interleaving every
			// token's LTP (index and options) never looks stable, so the
			// close would always wait for the hard deadline.
			refToken := s.cfg.CloseRefToken
			ingest.OnTick = func(token string, price int64) {
				if token != refToken {
					return
				}
				if detector.Observe(price, time.Now()) {
					wsCancel()
				}
			}

			s.prom.MarketState.Set(1)
			if s.redisWriter != nil {
				s.redisWriter.PublishMarketState("open")
			}
			log.Printf("[mdengine] 📡 WS connected — smart close after %s (hard max %s)",
				closeTime.In(markethours.IST).Format("15:04:05"),
				wsDeadline.In(markethours.IST).Format("15:04:05"))

			// Dynamic FNO token subscription: drain dynamicSubCh and subscribe on live WS
			go func() {
				for {
					select {
					case <-wsCtx.Done():
						return
					case tokens, ok := <-s.dynamicSubCh:
						if !ok {
							return
						}
						s.rememberDynamicTokens(tokens) // wanted for this session even if the send failed
						if err := ingest.SubscribeTokens(feedSubscribeMode, tokens); err != nil {
							log.Printf("[mdengine] ⚠️  dynamic subscribe failed: %v", err)
						} else {
							log.Printf("[mdengine] ✅ dynamically subscribed FNO tokens: %+v", tokens)
						}
					}
				}
			}()

			// Block until close detection triggers, hard deadline, or the feed is lost
			// (incl. the stale-feed watchdog forcing a reconnect/re-login)
			wsStarted := time.Now()
			s.setFeedReconnect(ingest.ForceReconnect)
			s.setFeedLastFrame(ingest.LastFrameTime)
			startErr := ingest.Start(wsCtx, s.tickCh)
			s.setFeedReconnect(nil)
			s.setFeedLastFrame(nil)
			if startErr != nil {
				log.Printf("[mdengine] ws session ended: %v", startErr)
			}
			lost := feedLostMidSession(startErr, wsCtx.Err(), time.Now())
			wsCancel()

			if lost {
				// Socket gave up mid-session (auth rejected / reconnects
				// exhausted): keep forming candles, re-login and reconnect.
				s.health.SetWSConnected(false)
				s.prom.SessionTransitions.WithLabelValues("ws_disconnect").Inc()
				if time.Since(wsStarted) > 10*time.Minute {
					feedBackoff = feedRetryDelay // the session had been healthy
				}
				log.Printf("[mdengine] ⚠️  feed lost mid-session — re-login in %v", feedBackoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(feedBackoff):
				}
				feedBackoff = minDur(feedBackoff*2, feedRetryMaxDelay)
				continue
			}

			// Market close: flush + cleanup
			s.health.SetWSConnected(false)
			s.prom.MarketState.Set(0)
			s.prom.SessionTransitions.WithLabelValues("close").Inc()
			if s.redisWriter != nil {
				s.redisWriter.PublishMarketState("closed")
			}

			// Finalize all in-progress candles (ADR-006 Contract #3)
			s.flushSession(ctx)
			s.dynMu.Lock()
			s.dynTokens = nil // dynamic FNO tokens are per session
			s.dynMu.Unlock()
			s.resetStaleFeed() // forced re-login cap is per session day

			log.Printf("[mdengine] 🔌 WS disconnected — closing price: %d", detector.ClosingPrice())

			if ctx.Err() != nil {
				return
			}
			// Loop back to wait for next pre-open
		}
	}()

	log.Println("[mdengine] ╔═══════════════════════════════════════════════════════════════╗")
	log.Println("[mdengine] ║  Market Data Engine (MS1) — Production Mode                  ║")
	log.Println("[mdengine] ║                                                              ║")
	log.Println("[mdengine] ║  Pipeline (24/7): [Agg] → [TF Builder] → [Redis/SQLite]      ║")
	log.Println("[mdengine] ║  Pre-open: 9:10 login → 9:14 WS connect → 9:15 first tick    ║")
	log.Println("[mdengine] ║  Smart close: price stabilization after 15:30 (max +5min)    ║")
	log.Printf("[mdengine] ║  TFs: %v                              ║", s.cfg.EnabledTFs)
	log.Println("[mdengine] ╚═══════════════════════════════════════════════════════════════╝")
	log.Printf("[mdengine] %s", markethours.StatusString(time.Now()))
}

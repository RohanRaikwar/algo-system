package stratengine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trading-systemv1/internal/model"
	"trading-systemv1/internal/strategy"
)

// positionCommander is the strategy side of a dashboard command.
type positionCommander interface {
	exitRequester
	SetLevels(side strategy.PositionSide, fnoToken string, e strategy.LevelEdit) error
}

// positionCommand applies a dashboard command (move stop/target, exit) to a
// paper position. It returns the exit signal to send, if any. Real-order
// strategies are never touched from the dashboard.
func (svc *Service) positionCommand(payload string, now time.Time) (*strategy.Signal, error) {
	var cmd model.PositionCommand
	if err := json.Unmarshal([]byte(payload), &cmd); err != nil {
		return nil, fmt.Errorf("bad payload: %w", err)
	}
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	switch {
	case svc.isRealStrategy(cmd.Strategy):
		return nil, fmt.Errorf("%s places real orders; dashboard edits are paper only", cmd.Strategy)
	case cmd.TS.IsZero() || now.Sub(cmd.TS) > exitRequestMaxAge:
		return nil, fmt.Errorf("stale command from %s", cmd.TS.Format(time.RFC3339))
	}
	sr := svc.srByName(cmd.Strategy)
	if sr == nil {
		return nil, fmt.Errorf("%s not running", cmd.Strategy)
	}
	return svc.positionCommandVia(sr, cmd)
}

func (svc *Service) positionCommandVia(c positionCommander, cmd model.PositionCommand) (*strategy.Signal, error) {
	side, tok := strategy.PositionSide(cmd.Side), svc.unqualifyToken(cmd.FNOToken)
	if cmd.Action == model.PositionExit {
		sig := c.ExitRequested(side, tok, "MANUAL EXIT")
		if sig == nil {
			return nil, fmt.Errorf("no open %s %s position", cmd.Strategy, cmd.Side)
		}
		return sig, nil
	}
	e := strategy.LevelEdit{IndexStop: cmd.IndexStop, IndexTarget: cmd.IndexTarget, FNOSL: cmd.FNOSL, FNOTarget: cmd.FNOTarget}
	return nil, c.SetLevels(side, tok, e)
}

// positionCommandLoop applies dashboard commands, resubscribing if the
// subscription drops.
func (svc *Service) positionCommandLoop(ctx context.Context) {
	client := svc.redisWriter.Client()
	backoff := time.Second
	for ctx.Err() == nil {
		sub := client.Subscribe(ctx, model.PositionCommandChannel)
		for msg := range sub.Channel() {
			backoff = time.Second
			sig, err := svc.positionCommand(msg.Payload, time.Now())
			if err != nil {
				log.Printf("[stratengine] position command refused: %v", err)
				continue
			}
			if sig != nil {
				log.Printf("[stratengine] 🚪 manual exit %s %s", sig.StrategyName, sig.Side)
				svc.tfEngine.SendSignal(*sig)
				continue
			}
			svc.publishLiveOrders(ctx)
		}
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("[stratengine] ⚠️  %s subscription closed — resubscribing in %s", model.PositionCommandChannel, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

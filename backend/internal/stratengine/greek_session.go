package stratengine

import (
	"fmt"
	"sync"
	"time"

	"trading-systemv1/internal/instruments"
	"trading-systemv1/pkg/smartconnect"

	"github.com/pquerna/otp/totp"
)

// greekSessionRefreshGap is the least time between two logins for one
// client after OptionGreek rejects its token. Angel limits logins, and
// every picker sharing the client sees the same rejection.
const greekSessionRefreshGap = time.Minute

// greekSessionState rate-limits greek-triggered logins per client.
type greekSessionState struct {
	mu   sync.Mutex
	last map[*smartconnect.SmartConnect]greekRefreshResult
}

type greekRefreshResult struct {
	at  time.Time
	err error
}

// attachGreekRefresh lets sp log its own client in again when OptionGreek
// answers "Invalid Token". A picker with no client is left alone.
func (svc *Service) attachGreekRefresh(sp *instruments.StrikePicker) {
	if sp == nil || sp.SmartConnect() == nil {
		return
	}
	sc := sp.SmartConnect()
	sp.SetSessionRefresher(func() error { return svc.refreshGreekSession(sc) })
}

// refreshGreekSession renews sc's session in place: through the session
// manager when sc is its client, else by a fresh login on sc itself (the
// standalone paper session). Within greekSessionRefreshGap of the last
// attempt for sc it returns that attempt's result without logging in.
func (svc *Service) refreshGreekSession(sc *smartconnect.SmartConnect) error {
	st := &svc.greekSession
	st.mu.Lock()
	defer st.mu.Unlock()
	if r, ok := st.last[sc]; ok && time.Since(r.at) < greekSessionRefreshGap {
		return r.err
	}
	err := svc.loginInPlace(sc)
	if st.last == nil {
		st.last = make(map[*smartconnect.SmartConnect]greekRefreshResult)
	}
	st.last[sc] = greekRefreshResult{at: time.Now(), err: err}
	return err
}

func (svc *Service) loginInPlace(sc *smartconnect.SmartConnect) error {
	if svc.greekLogin != nil {
		return svc.greekLogin(sc)
	}
	if sm := svc.sessionManager; sm != nil && sm.GetClient() == sc {
		return sm.RefreshSession()
	}
	if svc.cfg.AngelClientID == "" || svc.cfg.AngelPassword == "" || svc.cfg.AngelTOTP == "" {
		return fmt.Errorf("no Angel credentials for a session refresh")
	}
	code, err := totp.GenerateCode(svc.cfg.AngelTOTP, time.Now())
	if err != nil {
		return fmt.Errorf("TOTP: %w", err)
	}
	if _, err := sc.GenerateSession(svc.cfg.AngelClientID, svc.cfg.AngelPassword, code); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	return nil
}

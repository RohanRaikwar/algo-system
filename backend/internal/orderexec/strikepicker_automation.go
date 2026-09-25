package orderexec

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type AutomationSignalType string

const (
	SignalBuyCall AutomationSignalType = "BUY_CALL"
	SignalBuyPut  AutomationSignalType = "BUY_PUT"
)

type AutomationMarketState string

const (
	MarketStateTrending AutomationMarketState = "trending"
	MarketStateSideways AutomationMarketState = "sideways"
	MarketStateChoppy   AutomationMarketState = "choppy"
)

type AutomationStrength string

const (
	StrengthLow    AutomationStrength = "low"
	StrengthMedium AutomationStrength = "medium"
	StrengthHigh   AutomationStrength = "high"
)

type AutomationHoldType string

const (
	HoldTypeIntraday AutomationHoldType = "intraday"
	HoldTypeCarry    AutomationHoldType = "carry"
)

type AutomationIVMove string

const (
	IVMoveRise    AutomationIVMove = "rise"
	IVMoveNeutral AutomationIVMove = "neutral"
	IVMoveFall    AutomationIVMove = "fall"
)

type AutomationSession string

const (
	SessionEarly AutomationSession = "early_session"
	SessionMid   AutomationSession = "mid_session"
	SessionLate  AutomationSession = "late_session"
)

type OptionType string

const (
	OptionTypeCall OptionType = "CE"
	OptionTypePut  OptionType = "PE"
)

type OptionContract struct {
	Token          string
	Symbol         string
	Strike         int64
	Expiry         time.Time
	OptionType     OptionType
	Delta          float64
	Theta          float64
	Vega           float64
	IV             float64
	Premium        float64
	LiquidityScore float64
}

type AutomationInput struct {
	SignalType       AutomationSignalType
	IsExpiryDay      bool
	EntryTime        time.Time
	MarketState      AutomationMarketState
	TrendStrength    AutomationStrength
	MomentumStrength AutomationStrength
	HoldType         AutomationHoldType
	OptionChain      []OptionContract
	ExpectedIVMove   AutomationIVMove
	SpotPrice        int64
	
	// Premium range filter (in paise) for short/sell strategies
	// Example: MinPremium=20000, MaxPremium=25000 for ₹200-250 range
	MinPremium       float64
	MaxPremium       float64
	
	// Strike selection mode: "ATM" (default), "OTM1", "OTM2", "OTM3"
	// OTM1 = 1 strike OTM, OTM2 = 2 strikes OTM, etc.
	StrikeMode       string
}

type ExitPolicy struct {
	FastExit            bool
	TightTrailOnSideway bool
	ReduceOnIVDrop      bool
}

type AutomationPick struct {
	StrikeInfo
	Contract       OptionContract
	SelectedExpiry time.Time
	Session        AutomationSession
	ExitPolicy     ExitPolicy
	UsedFallback   bool
	Reason         string
}

type expiryChoice string

const (
	expiryChoiceToday   expiryChoice = "today_expiry"
	expiryChoiceCurrent expiryChoice = "current_week_expiry"
	expiryChoiceNext    expiryChoice = "next_expiry"
)

type expirySet struct {
	today   time.Time
	current time.Time
	next    time.Time
}

type candidateScore struct {
	strikeRank    int
	deltaDistance float64
	thetaRisk     float64
	vegaPenalty   float64
	ivPenalty     float64
	liqPenalty    float64
}

func (sp *StrikePicker) SelectContract(input AutomationInput) (AutomationPick, error) {
	if len(input.OptionChain) == 0 {
		return AutomationPick{}, fmt.Errorf("option chain is empty")
	}
	step := sp.strikeStep
	if step <= 0 {
		step = inferStrikeStep(input.OptionChain)
	}
	if step <= 0 {
		step = 50
	}
	return selectContract(input, step)
}

func (sp *StrikePicker) ResolveByAutomation(input AutomationInput) (AutomationPick, error) {
	pick, err := sp.SelectContract(input)
	if err != nil {
		return AutomationPick{}, err
	}

	contract := pick.Contract
	symbol := contract.Symbol
	if symbol == "" {
		symbol = buildOptionSymbol(contract.Expiry, contract.Strike, contract.OptionType)
	}

	token := contract.Token
	var lotSize int64
	if token == "" {
		if sp.sc == nil {
			return AutomationPick{}, fmt.Errorf("cannot resolve token for %s without SmartConnect session", symbol)
		}
		token, lotSize, err = sp.searchToken(symbol)
		if err != nil {
			return AutomationPick{}, err
		}
	}

	pick.StrikeInfo = StrikeInfo{
		Token:   token,
		Symbol:  symbol,
		LotSize: lotSize,
		Strike: contract.Strike,
	}
	return pick, nil
}

func (sp *StrikePicker) ResolveForEntry(input AutomationInput) (AutomationPick, error) {
	if len(input.OptionChain) == 0 {
		chain, err := sp.LoadOptionChain(input.EntryTime)
		if err != nil {
			return AutomationPick{}, err
		}
		input.OptionChain = chain
	}
	return sp.ResolveByAutomation(input)
}

func (sp *StrikePicker) LoadOptionChain(entryTime time.Time) ([]OptionContract, error) {
	if sp.sc == nil {
		return nil, fmt.Errorf("option chain fetch requires SmartConnect session")
	}

	expiries := sp.automationExpiries(entryTime)
	var chain []OptionContract
	seen := make(map[string]struct{})
	for _, expiry := range expiries {
		if expiry.IsZero() {
			continue
		}
		key := normalizeExpiryDay(expiry).Format("2006-01-02")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		contracts, err := sp.loadOptionGreekExpiry(expiry)
		if err != nil {
			return nil, err
		}
		chain = append(chain, contracts...)
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("no option chain data available")
	}
	return chain, nil
}

func (sp *StrikePicker) ApplyPick(pick AutomationPick) {
	info := pick.StrikeInfo
	if info.Token == "" {
		info.Token = pick.Contract.Token
	}
	if info.Symbol == "" {
		info.Symbol = pick.Contract.Symbol
	}
	if info.Strike == 0 {
		info.Strike = pick.Contract.Strike
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()

	switch pick.Contract.OptionType {
	case OptionTypeCall:
		sp.currentCE = info
	case OptionTypePut:
		sp.currentPE = info
	}
	sp.resolved = true
	sp.resolveTS = time.Now()
}

func selectContract(input AutomationInput, step int64) (AutomationPick, error) {
	session := classifySession(input.EntryTime)
	expiries := deriveExpirySet(input)
	if expiries.current.IsZero() {
		return AutomationPick{}, fmt.Errorf("no usable expiry found in option chain")
	}

	usedFallback := false
	reasons := []string{fmt.Sprintf("session=%s", session)}

	targetChoice := chooseExpiry(input, session, expiries)
	targetExpiry := expiryForChoice(expiries, targetChoice)
	if targetExpiry.IsZero() {
		targetExpiry = fallbackExpiry(expiries)
		usedFallback = true
		reasons = append(reasons, "fallback_expiry")
	}

	optType := optionTypeForSignal(input.SignalType)
	if optType == "" {
		return AutomationPick{}, fmt.Errorf("unsupported signal type %q", input.SignalType)
	}

	candidates := filterByTypeAndExpiry(input.OptionChain, optType, targetExpiry)
	if len(candidates) == 0 {
		targetExpiry = fallbackExpiry(expiries)
		candidates = filterByTypeAndExpiry(input.OptionChain, optType, targetExpiry)
		usedFallback = true
		reasons = append(reasons, "fallback_type_expiry")
	}
	if len(candidates) == 0 {
		return AutomationPick{}, fmt.Errorf("no contracts found for %s", optType)
	}

	if input.IsExpiryDay && session == SessionLate && sameExpiryDay(targetExpiry, expiries.today) &&
		hasHighThetaRisk(candidates) && !expiries.next.IsZero() {
		nextCandidates := filterByTypeAndExpiry(input.OptionChain, optType, expiries.next)
		if len(nextCandidates) > 0 {
			targetExpiry = expiries.next
			candidates = nextCandidates
			usedFallback = true
			reasons = append(reasons, "late_high_theta_next_expiry")
		}
	}

	deltaCandidates := filterByDelta(candidates, optType)
	if len(deltaCandidates) == 0 {
		usedFallback = true
		reasons = append(reasons, "delta_fallback")
		deltaCandidates = candidates
	}

	// Use strike mode if specified, otherwise use default priority
	var priorityCandidates []OptionContract
	if input.StrikeMode != "" {
		priorityCandidates = filterStrikePriorityWithMode(deltaCandidates, optType, input.SpotPrice, step, input.StrikeMode)
		reasons = append(reasons, fmt.Sprintf("strike_mode=%s", input.StrikeMode))
	} else {
		priorityCandidates = filterStrikePriority(deltaCandidates, optType, input.SpotPrice, step)
	}
	
	if len(priorityCandidates) == 0 {
		usedFallback = true
		reasons = append(reasons, "strike_fallback")
		priorityCandidates = deltaCandidates
	}

	thetaCandidates := filterTheta(priorityCandidates, input)
	if len(thetaCandidates) == 0 {
		usedFallback = true
		reasons = append(reasons, "theta_fallback")
		thetaCandidates = priorityCandidates
	}

	vegaCandidates := filterVegaIV(thetaCandidates, input)
	if len(vegaCandidates) == 0 {
		usedFallback = true
		reasons = append(reasons, "vega_fallback")
		vegaCandidates = thetaCandidates
	}

	// ── Premium range filter (for short/sell strategies) ──
	premiumCandidates := vegaCandidates
	if input.MinPremium > 0 || input.MaxPremium > 0 {
		premiumCandidates = filterPremiumRange(vegaCandidates, input.MinPremium, input.MaxPremium)
		if len(premiumCandidates) == 0 {
			usedFallback = true
			reasons = append(reasons, "premium_fallback")
			premiumCandidates = vegaCandidates
		} else {
			reasons = append(reasons, fmt.Sprintf("premium_filter_%.0f-%.0f", input.MinPremium/100, input.MaxPremium/100))
		}
	}

	best := bestCandidate(premiumCandidates, input, optType, step)
	pick := AutomationPick{
		Contract:       best,
		SelectedExpiry: targetExpiry,
		Session:        session,
		ExitPolicy:     buildExitPolicy(input, targetExpiry, expiries),
		UsedFallback:   usedFallback,
		Reason:         strings.Join(reasons, ","),
	}
	return pick, nil
}

func classifySession(entryTime time.Time) AutomationSession {
	entryTime = entryTime.In(istZone)
	minutes := entryTime.Hour()*60 + entryTime.Minute()
	switch {
	case minutes <= 11*60:
		return SessionEarly
	case minutes <= 13*60:
		return SessionMid
	default:
		return SessionLate
	}
}

func deriveExpirySet(input AutomationInput) expirySet {
	entryDay := input.EntryTime.In(istZone)
	var expiries []time.Time
	seen := make(map[string]struct{})
	for _, c := range input.OptionChain {
		expiry := normalizeExpiryDay(c.Expiry)
		if expiry.IsZero() {
			continue
		}
		key := expiry.Format("2006-01-02")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		expiries = append(expiries, expiry)
	}
	sort.Slice(expiries, func(i, j int) bool { return expiries[i].Before(expiries[j]) })

	var set expirySet
	for _, expiry := range expiries {
		if sameExpiryDay(expiry, entryDay) {
			set.today = expiry
		}
		if !expiry.Before(normalizeExpiryDay(entryDay)) {
			if set.current.IsZero() {
				set.current = expiry
				continue
			}
			if set.next.IsZero() && expiry.After(set.current) {
				set.next = expiry
				break
			}
		}
	}
	if set.current.IsZero() && len(expiries) > 0 {
		set.current = expiries[0]
	}
	if set.next.IsZero() {
		for _, expiry := range expiries {
			if expiry.After(set.current) {
				set.next = expiry
				break
			}
		}
	}
	return set
}

func chooseExpiry(input AutomationInput, session AutomationSession, expiries expirySet) expiryChoice {
	switch input.HoldType {
	case HoldTypeCarry:
		return expiryChoiceNext
	default:
	}

	if input.IsExpiryDay {
		if input.MarketState == MarketStateSideways || input.MarketState == MarketStateChoppy {
			return expiryChoiceNext
		}
		if session == SessionEarly && input.TrendStrength == StrengthHigh && input.MomentumStrength == StrengthHigh {
			return expiryChoiceToday
		}
		return expiryChoiceNext
	}
	return expiryChoiceCurrent
}

func expiryForChoice(expiries expirySet, choice expiryChoice) time.Time {
	switch choice {
	case expiryChoiceToday:
		if !expiries.today.IsZero() {
			return expiries.today
		}
	case expiryChoiceNext:
		if !expiries.next.IsZero() {
			return expiries.next
		}
	}
	return expiries.current
}

func fallbackExpiry(expiries expirySet) time.Time {
	if !expiries.next.IsZero() {
		return expiries.next
	}
	if !expiries.current.IsZero() {
		return expiries.current
	}
	return expiries.today
}

func optionTypeForSignal(signalType AutomationSignalType) OptionType {
	switch signalType {
	case SignalBuyCall:
		return OptionTypeCall
	case SignalBuyPut:
		return OptionTypePut
	default:
		return ""
	}
}

func filterByTypeAndExpiry(chain []OptionContract, optionType OptionType, expiry time.Time) []OptionContract {
	var out []OptionContract
	for _, c := range chain {
		if c.OptionType != optionType {
			continue
		}
		if !sameExpiryDay(c.Expiry, expiry) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func filterByDelta(candidates []OptionContract, optionType OptionType) []OptionContract {
	var out []OptionContract
	for _, c := range candidates {
		switch optionType {
		case OptionTypeCall:
			// Widen delta range to support OTM strikes (0.20-0.65)
			// ATM: 0.45-0.55, OTM1: 0.35-0.45, OTM2: 0.25-0.35, OTM3: 0.15-0.25
			if c.Delta >= 0.20 && c.Delta <= 0.65 {
				out = append(out, c)
			}
		case OptionTypePut:
			// Widen delta range to support OTM strikes (-0.20 to -0.65)
			if c.Delta <= -0.20 && c.Delta >= -0.65 {
				out = append(out, c)
			}
		}
	}
	return out
}

func filterStrikePriority(candidates []OptionContract, optionType OptionType, spotPrice, step int64) []OptionContract {
	if len(candidates) == 0 || spotPrice <= 0 || step <= 0 {
		return candidates
	}
	atm := spotStrike(spotPrice, step)
	bestRank := math.MaxInt
	for _, c := range candidates {
		rank := strikeRank(c, optionType, atm, step)
		if rank < bestRank {
			bestRank = rank
		}
	}

	var out []OptionContract
	for _, c := range candidates {
		if strikeRank(c, optionType, atm, step) == bestRank {
			out = append(out, c)
		}
	}
	return out
}

// filterStrikePriorityWithMode filters strikes based on the specified mode.
// mode: "ATM" (default), "OTM1", "OTM2", "OTM3" for short/sell strategies.
func filterStrikePriorityWithMode(candidates []OptionContract, optionType OptionType, spotPrice, step int64, mode string) []OptionContract {
	if len(candidates) == 0 || spotPrice <= 0 || step <= 0 {
		return candidates
	}
	
	atm := spotStrike(spotPrice, step)
	targetRank := 0 // Default: ATM
	
	switch mode {
	case "OTM1":
		targetRank = 2 // 1 strike OTM
	case "OTM2":
		targetRank = 3 // 2 strikes OTM
	case "OTM3":
		targetRank = 4 // 3 strikes OTM
	default:
		// ATM or empty: use best available rank
		bestRank := math.MaxInt
		for _, c := range candidates {
			rank := strikeRank(c, optionType, atm, step)
			if rank < bestRank {
				bestRank = rank
			}
		}
		targetRank = bestRank
	}

	var out []OptionContract
	for _, c := range candidates {
		if strikeRank(c, optionType, atm, step) == targetRank {
			out = append(out, c)
		}
	}
	
	// Fallback: if no strikes at target rank, use best available
	if len(out) == 0 {
		bestRank := math.MaxInt
		for _, c := range candidates {
			rank := strikeRank(c, optionType, atm, step)
			if rank < bestRank {
				bestRank = rank
			}
		}
		for _, c := range candidates {
			if strikeRank(c, optionType, atm, step) == bestRank {
				out = append(out, c)
			}
		}
	}
	
	return out
}

func filterTheta(candidates []OptionContract, input AutomationInput) []OptionContract {
	if len(candidates) == 0 {
		return nil
	}
	minRisk := math.MaxFloat64
	for _, c := range candidates {
		risk := thetaRisk(c)
		if risk < minRisk {
			minRisk = risk
		}
	}

	limit := math.Max(0.12, minRisk*1.5)
	if input.IsExpiryDay {
		limit = math.Max(0.10, minRisk*1.35)
	}

	var out []OptionContract
	for _, c := range candidates {
		if thetaRisk(c) <= limit {
			out = append(out, c)
		}
	}
	return out
}

func filterVegaIV(candidates []OptionContract, input AutomationInput) []OptionContract {
	if len(candidates) == 0 || input.ExpectedIVMove == IVMoveRise {
		return candidates
	}

	medianVega := medianFloat(contractMetricSlice(candidates, func(c OptionContract) float64 { return math.Abs(c.Vega) }))
	medianIV := medianFloat(contractMetricSlice(candidates, func(c OptionContract) float64 { return c.IV }))
	if medianVega == 0 && medianIV == 0 {
		return candidates
	}

	var out []OptionContract
	for _, c := range candidates {
		vegaOK := medianVega == 0 || math.Abs(c.Vega) <= medianVega*1.15
		ivOK := medianIV == 0 || c.IV <= medianIV*1.10
		if vegaOK && ivOK {
			out = append(out, c)
		}
	}
	return out
}

// filterPremiumRange filters contracts by premium range (in paise).
// For short/sell strategies, you want strikes with premiums in the 200-250 range.
// minPremium and maxPremium are in paise (e.g., 20000 = ₹200, 25000 = ₹250).
func filterPremiumRange(candidates []OptionContract, minPremium, maxPremium float64) []OptionContract {
	if len(candidates) == 0 || (minPremium <= 0 && maxPremium <= 0) {
		return candidates
	}

	var out []OptionContract
	for _, c := range candidates {
		premiumPaise := c.Premium * 100 // Convert rupees to paise
		if minPremium > 0 && premiumPaise < minPremium {
			continue
		}
		if maxPremium > 0 && premiumPaise > maxPremium {
			continue
		}
		out = append(out, c)
	}
	return out
}

func bestCandidate(candidates []OptionContract, input AutomationInput, optionType OptionType, step int64) OptionContract {
	atm := spotStrike(input.SpotPrice, step)
	sort.SliceStable(candidates, func(i, j int) bool {
		left := scoreCandidate(candidates[i], input, optionType, atm, step)
		right := scoreCandidate(candidates[j], input, optionType, atm, step)
		switch {
		case left.strikeRank != right.strikeRank:
			return left.strikeRank < right.strikeRank
		case left.deltaDistance != right.deltaDistance:
			return left.deltaDistance < right.deltaDistance
		case left.thetaRisk != right.thetaRisk:
			return left.thetaRisk < right.thetaRisk
		case left.vegaPenalty != right.vegaPenalty:
			return left.vegaPenalty < right.vegaPenalty
		case left.ivPenalty != right.ivPenalty:
			return left.ivPenalty < right.ivPenalty
		default:
			return left.liqPenalty < right.liqPenalty
		}
	})
	return candidates[0]
}

func scoreCandidate(contract OptionContract, input AutomationInput, optionType OptionType, atm, step int64) candidateScore {
	vegaPenalty := math.Abs(contract.Vega)
	if input.ExpectedIVMove == IVMoveRise {
		vegaPenalty = -math.Abs(contract.Vega)
	}

	ivPenalty := contract.IV
	if input.ExpectedIVMove == IVMoveRise {
		ivPenalty = 0
	}

	liqPenalty := 1.0 / math.Max(contract.LiquidityScore, 0.01)
	return candidateScore{
		strikeRank:    strikeRank(contract, optionType, atm, step),
		deltaDistance: math.Abs(math.Abs(contract.Delta) - 0.55),
		thetaRisk:     thetaRisk(contract),
		vegaPenalty:   vegaPenalty,
		ivPenalty:     ivPenalty,
		liqPenalty:    liqPenalty,
	}
}

func strikeRank(contract OptionContract, optionType OptionType, atm, step int64) int {
	if atm == 0 || step <= 0 {
		return 2
	}
	diff := contract.Strike - atm
	switch optionType {
	case OptionTypeCall:
		switch {
		case diff == 0:
			return 0 // ATM
		case diff == -step:
			return 1 // 1 ITM
		case diff == step:
			return 2 // 1 OTM (for short/sell strategies)
		case diff == 2*step:
			return 3 // 2 OTM
		case diff == 3*step:
			return 4 // 3 OTM
		case diff < -step:
			return 6 // Multiple ITM (less desirable)
		case diff > 3*step:
			return 5 // Far OTM
		}
	case OptionTypePut:
		switch {
		case diff == 0:
			return 0 // ATM
		case diff == step:
			return 1 // 1 ITM
		case diff == -step:
			return 2 // 1 OTM (for short/sell strategies)
		case diff == -2*step:
			return 3 // 2 OTM
		case diff == -3*step:
			return 4 // 3 OTM
		case diff > step:
			return 6 // Multiple ITM (less desirable)
		case diff < -3*step:
			return 5 // Far OTM
		}
	}
	return 2
}

func buildExitPolicy(input AutomationInput, targetExpiry time.Time, expiries expirySet) ExitPolicy {
	policy := ExitPolicy{
		TightTrailOnSideway: true,
		ReduceOnIVDrop:      true,
	}
	if sameExpiryDay(targetExpiry, expiries.today) {
		policy.FastExit = true
	}
	return policy
}

func hasHighThetaRisk(candidates []OptionContract) bool {
	for _, c := range candidates {
		if thetaRisk(c) >= 0.08 {
			return true
		}
	}
	return false
}

func thetaRisk(contract OptionContract) float64 {
	if contract.Premium <= 0 {
		return math.Abs(contract.Theta)
	}
	return math.Abs(contract.Theta) / contract.Premium
}

func spotStrike(spotPrice, step int64) int64 {
	if spotPrice <= 0 || step <= 0 {
		return 0
	}
	spotRupees := float64(spotPrice) / 100.0
	return int64(math.Round(spotRupees/float64(step))) * step
}

func inferStrikeStep(chain []OptionContract) int64 {
	if len(chain) < 2 {
		return 0
	}
	var strikes []int64
	seen := make(map[int64]struct{})
	for _, c := range chain {
		if c.Strike <= 0 {
			continue
		}
		if _, ok := seen[c.Strike]; ok {
			continue
		}
		seen[c.Strike] = struct{}{}
		strikes = append(strikes, c.Strike)
	}
	sort.Slice(strikes, func(i, j int) bool { return strikes[i] < strikes[j] })
	if len(strikes) < 2 {
		return 0
	}
	var minDiff int64
	for i := 1; i < len(strikes); i++ {
		diff := strikes[i] - strikes[i-1]
		if diff <= 0 {
			continue
		}
		if minDiff == 0 || diff < minDiff {
			minDiff = diff
		}
	}
	return minDiff
}

func normalizeExpiryDay(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	t = t.In(istZone)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, istZone)
}

func sameExpiryDay(left, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return false
	}
	l := normalizeExpiryDay(left)
	r := normalizeExpiryDay(right)
	return l.Equal(r)
}

func buildOptionSymbol(expiry time.Time, strike int64, optionType OptionType) string {
	return fmt.Sprintf("NIFTY%s%d%s", strings.ToUpper(expiry.In(istZone).Format("02Jan06")), strike, optionType)
}

func contractMetricSlice(contracts []OptionContract, fn func(OptionContract) float64) []float64 {
	out := make([]float64, 0, len(contracts))
	for _, c := range contracts {
		out = append(out, fn(c))
	}
	return out
}

func medianFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func (sp *StrikePicker) automationExpiries(entryTime time.Time) []time.Time {
	current := sp.nextWeeklyExpiryDay(entryTime)
	next := current.AddDate(0, 0, 7)
	if sameExpiryDay(current, next) {
		return []time.Time{current}
	}
	return []time.Time{current, next}
}

func (sp *StrikePicker) nextWeeklyExpiryDay(now time.Time) time.Time {
	now = now.In(istZone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, istZone)
	daysUntilTues := (int(time.Tuesday) - int(today.Weekday()) + 7) % 7
	if daysUntilTues == 0 {
		if now.Hour() > 15 || (now.Hour() == 15 && now.Minute() >= 30) {
			daysUntilTues = 7
		}
	}
	return today.AddDate(0, 0, daysUntilTues)
}

func (sp *StrikePicker) loadOptionGreekExpiry(expiry time.Time) ([]OptionContract, error) {
	res, err := sp.sc.OptionGreek(map[string]any{
		"name":       "NIFTY",
		"expirydate": strings.ToUpper(expiry.In(istZone).Format("02Jan2006")),
	})
	if err != nil {
		return nil, fmt.Errorf("OptionGreek(%s): %w", expiry.Format("02Jan2006"), err)
	}

	rawData, ok := res["data"]
	if !ok || rawData == nil {
		return nil, nil
	}

	items, ok := rawData.([]interface{})
	if !ok {
		return nil, fmt.Errorf("OptionGreek(%s): unexpected data format", expiry.Format("02Jan2006"))
	}

	contracts := make([]OptionContract, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		optionType := OptionType(strings.ToUpper(readString(row, "optionType", "optiontype")))
		if optionType != OptionTypeCall && optionType != OptionTypePut {
			continue
		}

		strike := int64(math.Round(readFloat(row, "strikePrice", "strikeprice", "strike")))
		if strike <= 0 {
			continue
		}

		symbol := readString(row, "tradingSymbol", "tradingsymbol", "symbol")
		contracts = append(contracts, OptionContract{
			Token:          readString(row, "symbolToken", "symboltoken", "token"),
			Symbol:         symbol,
			Strike:         strike,
			Expiry:         expiry,
			OptionType:     optionType,
			Delta:          readFloat(row, "delta"),
			Theta:          readFloat(row, "theta"),
			Vega:           readFloat(row, "vega"),
			IV:             readFloat(row, "impliedVolatility", "iv"),
			Premium:        readFloat(row, "ltp", "premium", "lastPrice", "close"),
			LiquidityScore: math.Max(readFloat(row, "tradeVolume", "volume"), readFloat(row, "openInterest", "openinterest", "oi")),
		})
	}
	return contracts, nil
}

func readString(row map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := row[key]; ok {
			switch typed := val.(type) {
			case string:
				if typed != "" {
					return typed
				}
			case fmt.Stringer:
				return typed.String()
			}
		}
	}
	return ""
}

func readFloat(row map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		val, ok := row[key]
		if !ok || val == nil {
			continue
		}
		switch typed := val.(type) {
		case float64:
			return typed
		case float32:
			return float64(typed)
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return f
			}
		}
	}
	return 0
}

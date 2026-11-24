package model

// OptChainLeg is one side (CE or PE) of a chain row.
type OptChainLeg struct {
	Token  string  `json:"token"`
	Symbol string  `json:"symbol"`
	LTP    int64   `json:"ltp,omitempty"` // paise; filled by the gateway
	IV     float64 `json:"iv,omitempty"`
	Delta  float64 `json:"delta,omitempty"`
}

// OptChainRow is one strike of the chain.
type OptChainRow struct {
	Strike int64        `json:"strike"` // index points
	CE     *OptChainLeg `json:"ce,omitempty"`
	PE     *OptChainLeg `json:"pe,omitempty"`
}

// OptChainView is one index's chain: contracts from the instrument master
// around the ATM strike of the nearest listed expiry.
type OptChainView struct {
	Index     string        `json:"index"`      // "NSE:99926009"
	Name      string        `json:"name"`       // "BANKNIFTY"
	Exchange  string        `json:"exchange"`   // options segment "NFO", "BFO"
	Spot      int64         `json:"spot"`       // paise
	ATM       int64         `json:"atm"`        // index points
	Step      int64         `json:"step"`       // index points
	Expiry    string        `json:"expiry"`     // "2026-12-29"
	UpdatedAt string        `json:"updated_at"` // RFC3339
	PricedAt  string        `json:"priced_at,omitempty"`
	PriceNote string        `json:"price_note,omitempty"`
	Rows      []OptChainRow `json:"rows"`
}

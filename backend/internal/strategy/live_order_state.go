package strategy

// LiveFNOPosition exposes the active FnO premium state for a strategy position.
// It is used by stratengine to seed live order UI state after snapshot restore.
type LiveFNOPosition struct {
	Side       PositionSide
	Token      string
	EntryPrice int64
	BestPrice  int64
}

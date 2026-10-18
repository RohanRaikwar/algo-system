package strategy

// LiveFNOPosition exposes the active FnO premium state for a strategy position.
// It is used by stratengine to seed live order UI state after snapshot restore.
type LiveFNOPosition struct {
	Side       PositionSide
	Token      string
	EntryPrice int64
	BestPrice  int64

	ManualSL    int64 // premium stop set by hand, 0 = automatic
	Target      int64 // premium target, 0 = none
	IndexStop   int64
	IndexTarget int64
	Manual      bool // index levels moved by hand
}

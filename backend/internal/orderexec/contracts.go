package orderexec

import (
	"errors"
	"time"
)

// istZone is Indian Standard Time, for IST day boundaries.
var istZone = time.FixedZone("IST", 5*3600+30*60)

// ContractSource gives the ATM CE/PE contracts the executor buys when a
// signal names none. Satisfied by *instruments.StrikePicker; orderexec
// never imports the instruments package.
type ContractSource interface {
	Resolved() bool
	CallContract() (token, symbol string)
	PutContract() (token, symbol string)
}

// contractRef boxes a ContractSource for atomic swapping.
type contractRef struct{ src ContractSource }

var errNoLotSizer = errors.New("lot size unknown: no instrument master attached (Config.LotSizeOf)")

// noLotSize is lotSizeOf without a Config.LotSizeOf: real entries refuse.
func noLotSize(string) (int64, error) { return 0, errNoLotSizer }

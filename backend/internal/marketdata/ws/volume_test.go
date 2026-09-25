package ws

import (
	"testing"
	"time"
)

func TestParseTick_DayVolumeFromQuotePacket(t *testing.T) {
	msg := map[string]interface{}{
		"token": "43210", "exchange_type": 2, "last_traded_price": int64(12345),
		"last_traded_quantity": int64(75), "volume_trade_for_the_day": int64(987_650),
		"exchange_timestamp": time.Now().UnixMilli(),
	}
	tk, err := parseTick(msg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if tk.DayVolume != 987_650 || tk.Qty != 75 {
		t.Fatalf("DayVolume=%d Qty=%d, want 987650/75", tk.DayVolume, tk.Qty)
	}
}

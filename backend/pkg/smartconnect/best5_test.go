package smartconnect

import (
	"encoding/binary"
	"testing"
)

// best5Packet is one 20-byte SnapQuote depth level: flag, qty, price, orders.
func best5Packet(flag uint16, qty, price int64, orders uint16) []byte {
	p := make([]byte, 20)
	binary.LittleEndian.PutUint16(p[0:2], flag)
	binary.LittleEndian.PutUint64(p[2:10], uint64(qty))
	binary.LittleEndian.PutUint64(p[10:18], uint64(price))
	binary.LittleEndian.PutUint16(p[18:20], orders)
	return p
}

// Angel's flag is 1 for buy, 0 for sell. Live ticks on 2026-10-06 parsed
// with the sides swapped had ask < bid on 121 of 122 option ticks (e.g. LTP
// 60.75 read as bid 60.95 / ask 60.30), so every quote looked crossed and
// the option picker and paper fills rejected them.
func TestParseBest5BuySellFlagSides(t *testing.T) {
	var b []byte
	b = append(b, best5Packet(1, 650, 6030, 3)...)  // best bid
	b = append(b, best5Packet(1, 1300, 6025, 5)...) // next bid
	b = append(b, best5Packet(0, 325, 6095, 2)...)  // best ask
	b = append(b, best5Packet(0, 975, 6100, 4)...)  // next ask
	for len(b) < 200 {
		b = append(b, best5Packet(0, 0, 0, 0)...)
	}
	got := parseBest5BuySell(b)
	buy := got["best_5_buy_data"].([]map[string]interface{})
	sell := got["best_5_sell_data"].([]map[string]interface{})
	if len(buy) != 2 || buy[0]["price"] != int64(6030) || buy[1]["price"] != int64(6025) {
		t.Fatalf("buy side %v", buy)
	}
	if len(sell) < 2 || sell[0]["price"] != int64(6095) || sell[1]["price"] != int64(6100) {
		t.Fatalf("sell side %v", sell)
	}
}

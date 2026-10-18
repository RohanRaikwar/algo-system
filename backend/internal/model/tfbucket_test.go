package model

import (
	"testing"
	"time"
)

func TestTFBucket(t *testing.T) {
	ist := time.FixedZone("IST", 19800)
	at := func(h, m int) int64 { return time.Date(2026, 10, 19, h, m, 0, 0, ist).Unix() }
	cases := []struct {
		ts   int64
		tf   int64
		want int64
	}{
		{at(9, 15), 1800, at(9, 15)},
		{at(9, 44), 1800, at(9, 15)},
		{at(9, 45), 1800, at(9, 45)},
		{at(15, 29), 1800, at(15, 15)},
		{at(9, 15), 600, at(9, 15)},
		{at(9, 24), 600, at(9, 15)},
		{at(9, 25), 600, at(9, 25)},
		// Clock-aligned TFs are unchanged.
		{at(9, 17), 300, at(9, 15)},
		{at(9, 29), 900, at(9, 15)},
		{at(9, 20), 3600, at(9, 15)},
		{at(10, 14), 3600, at(9, 15)},
		{at(15, 15), 3600, at(15, 15)},
		{at(9, 15), 120, at(9, 14)},
	}
	for _, c := range cases {
		if got := TFBucket(c.ts, c.tf); got != c.want {
			t.Errorf("TFBucket(%s, %d) = %s, want %s", time.Unix(c.ts, 0).In(ist).Format("15:04"), c.tf,
				time.Unix(got, 0).In(ist).Format("15:04"), time.Unix(c.want, 0).In(ist).Format("15:04"))
		}
	}
	if TFOffset(1800) != 900 || TFOffset(600) != 300 || TFOffset(3600) != 2700 || TFOffset(300) != 0 {
		t.Error("offsets")
	}
}

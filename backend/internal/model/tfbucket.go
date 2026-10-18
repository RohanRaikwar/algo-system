package model

// sessionOpenOfDay is 09:15 IST in seconds after 00:00 UTC (03:45 UTC).
const sessionOpenOfDay = 3*3600 + 45*60

// sessionAlignedTFs start their bars at the 09:15 open, like broker charts
// (09:15, 09:45, … for 30m; 09:15, 10:15, … for 1h). Every other TF keeps
// clock alignment (ts - ts%tf); for 1m/3m/5m/15m the two are the same.
var sessionAlignedTFs = map[int64]bool{600: true, 1800: true, 3600: true}

// TFBucket is the start (Unix seconds) of the tf-second bar holding ts.
// Every builder of TF candles (live, backfill, replay, dashboard) must use
// it so their bars line up.
func TFBucket(ts, tf int64) int64 {
	if tf <= 0 {
		return ts
	}
	if sessionAlignedTFs[tf] {
		off := sessionOpenOfDay % tf
		return ts - ((ts-off)%tf+tf)%tf
	}
	return ts - ts%tf
}

// TFOffset is the offset of tf's bar starts from clock alignment: a bar
// starts at ts with (ts - TFOffset(tf)) % tf == 0.
func TFOffset(tf int64) int64 {
	if tf > 0 && sessionAlignedTFs[tf] {
		return sessionOpenOfDay % tf
	}
	return 0
}

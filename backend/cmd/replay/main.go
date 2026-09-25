// Command replay feeds recorded ticks through the real candle construction
// path (internal/marketdata/agg → internal/marketdata/tfbuilder), in process
// and without Redis, and prints the resulting 1s and TF candles as JSONL.
//
// Purpose: regression-check candle construction across code changes.
//
//	# 1. Record ticks during a session (the system does not persist raw ticks;
//	#    mdengine publishes every tick as model.Tick JSON on pub:tick:*).
//	go run ./cmd/replay -record -redis localhost:6379 -out ticks.jsonl
//
//	# 2. Replay them before and after a change and diff.
//	go run ./cmd/replay -in ticks.jsonl -tfs 60,300 > before.jsonl
//	go run ./cmd/replay -in ticks.jsonl -tfs 60,300 > after.jsonl
//	diff before.jsonl after.jsonl
//
// Input: one model.Tick JSON object per line (the pub:tick:* payload format,
// also the wssim/tickserver wire format). Output lines are
// {"kind":"1s"|"tf"|"tf_forming","candle":{...}}; prices are int64 paise.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

func main() {
	var (
		in             = flag.String("in", "-", "input JSONL of model.Tick (- = stdin)")
		outPath        = flag.String("out", "-", "output file (- = stdout)")
		tfsFlag        = flag.String("tfs", "60,120,180,300,3600", "comma-separated TF seconds (mdengine ENABLED_TFS default)")
		speed          = flag.Float64("speed", 0, "playback speed: 0 = as fast as possible, 1 = real time, 10 = 10x")
		maxGap         = flag.Duration("max-gap", 5*time.Second, "cap on a single paced sleep before scaling (speed>0 only)")
		closeGate      = flag.Bool("market-close-gate", false, "enable the aggregator's 15:30 IST close gate (true in mdengine production)")
		forming        = flag.Bool("forming", false, "also emit forming TF snapshots")
		sortOut        = flag.Bool("sort", true, "buffer and emit in canonical order (deterministic); false streams in emission order")
		record         = flag.Bool("record", false, "record mode: PSUBSCRIBE to ticks on Redis and write JSONL to -out")
		redisAddr      = flag.String("redis", envOr("REDIS_ADDR", "localhost:6379"), "Redis address (record mode)")
		redisPass      = flag.String("redis-password", os.Getenv("REDIS_PASSWORD"), "Redis password (record mode)")
		recordPattern  = flag.String("pattern", "pub:tick:*", "PubSub pattern to record (record mode)")
		recordDuration = flag.Duration("duration", 0, "stop recording after this long (0 = until Ctrl-C)")
	)
	flag.Parse()
	log.SetOutput(os.Stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	w, closeOut, err := openOut(*outPath)
	if err != nil {
		log.Fatalf("[replay] %v", err)
	}
	defer closeOut()

	if *record {
		if *recordDuration > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, *recordDuration)
			defer cancel()
		}
		n, err := RecordTicks(ctx, *redisAddr, *redisPass, *recordPattern, w)
		log.Printf("[replay] recorded %d ticks", n)
		if err != nil && ctx.Err() == nil {
			log.Fatalf("[replay] record: %v", err)
		}
		return
	}

	tfs, err := parseTFs(*tfsFlag)
	if err != nil {
		log.Fatalf("[replay] -tfs: %v", err)
	}

	r := io.Reader(os.Stdin)
	if *in != "-" {
		f, err := os.Open(*in)
		if err != nil {
			log.Fatalf("[replay] %v", err)
		}
		defer f.Close()
		r = f
	}
	ticks, err := ReadTicks(r)
	if err != nil {
		log.Fatalf("[replay] read ticks: %v", err)
	}

	start := time.Now()
	st, err := Replay(ctx, ticks, w, Options{
		TFs:             tfs,
		Speed:           *speed,
		MaxGap:          *maxGap,
		MarketCloseGate: *closeGate,
		IncludeForming:  *forming,
		Sort:            *sortOut,
	})
	log.Printf("[replay] ticks=%d candles_1s=%d tf=%d forming=%d late=%d stale=%d dropped=%d in %s",
		st.Ticks, st.Candles1s, st.TFCandles, st.Forming, st.LateTicks, st.StaleRejects, st.DroppedAgg, time.Since(start).Round(time.Millisecond))
	if err != nil {
		log.Fatalf("[replay] %v", err)
	}
	if st.DroppedAgg > 0 {
		log.Fatalf("[replay] aggregator dropped %d candles — output incomplete", st.DroppedAgg)
	}
}

func parseTFs(s string) ([]int, error) {
	var tfs []int
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("bad TF %q", p)
		}
		tfs = append(tfs, n)
	}
	if len(tfs) == 0 {
		return nil, fmt.Errorf("no timeframes")
	}
	return tfs, nil
}

func openOut(p string) (io.Writer, func(), error) {
	if p == "-" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(p)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

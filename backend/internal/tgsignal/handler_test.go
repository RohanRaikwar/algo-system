package tgsignal

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trading-systemv1/internal/model"
)

type fakeReader struct {
	res   LLMResult
	err   error
	calls int
	reply string
}

func (f *fakeReader) Read(_ context.Context, _, replyText string) (LLMResult, error) {
	f.calls++
	f.reply = replyText
	return f.res, f.err
}

const realCall = "🟢 BUY NIFTY PE 22750\n\n💰 Entry: Near Above 191.00\n🎯 Target:1st: ₹202.00 , 2ND:221 \n🛑 Stop Loss: ₹182.00"

func llmCall(strike int64, opt, trig, sl string) LLMResult {
	return LLMResult{Kind: "new_call", Underlying: "NIFTY", Strike: strike, OptionType: opt, Trigger: trig, SL: sl,
		Targets: []string{"202", "221"}, Action: "none", Confidence: "high", Reason: "call"}
}

type sink struct {
	calls   []model.TGSignal
	follows []model.TGFollowUp
}

func newTestHandler(t *testing.T, r CallReader) (*handler, *sink, *Journal) {
	j, err := OpenJournal(filepath.Join(t.TempDir(), "tg.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	s := &sink{}
	h := newHandler(Config{ChatIDs: []int64{42}, MaxAge: time.Minute}, r, j,
		func(c model.TGSignal) error { s.calls = append(s.calls, c); return nil },
		func(f model.TGFollowUp) error { s.follows = append(s.follows, f); return nil })
	return h, s, j
}

var tNow = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func msg(id int, text string) inMsg { return inMsg{ChatID: 42, MsgID: id, Text: text, Sent: tNow} }

func verdictOf(t *testing.T, j *Journal, id int) (string, string) {
	t.Helper()
	var v, reason string
	if err := j.db.QueryRow(`SELECT verdict, reason FROM messages WHERE msg_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&v, &reason); err != nil {
		t.Fatalf("no journal row for %d: %v", id, err)
	}
	return v, reason
}

func TestHandlerRegexOnly(t *testing.T) {
	h, s, j := newTestHandler(t, nil)
	ctx := context.Background()
	h.onMessage(ctx, inMsg{ChatID: 7, MsgID: 1, Text: realCall, Sent: tNow}, tNow) // other chat
	old := msg(2, realCall)
	old.Sent = tNow.Add(-2 * time.Minute)
	h.onMessage(ctx, old, tNow)
	h.onMessage(ctx, msg(3, "good morning"), tNow)
	h.onMessage(ctx, msg(4, realCall), tNow)
	h.onMessage(ctx, msg(4, realCall), tNow) // repeat delivery
	if len(s.calls) != 1 || s.calls[0].MsgID != 4 || s.calls[0].Source != "REGEX" || s.calls[0].TriggerPaise != 19100 {
		t.Fatalf("calls %+v", s.calls)
	}
	if v, _ := verdictOf(t, j, 2); v != VerdictOld {
		t.Errorf("old verdict %s", v)
	}
}

func TestHandlerAgreeAndDisagree(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191.00", "₹182")}
	h, s, j := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	if len(s.calls) != 1 || s.calls[0].Source != "AGREE" {
		t.Fatalf("agree: %+v", s.calls)
	}

	r.res = llmCall(22750, "PE", "191", "172") // LLM reads a different stop
	h.onMessage(context.Background(), msg(2, realCall), tNow)
	if len(s.calls) != 1 {
		t.Fatal("traded on disagreement")
	}
	if v, reason := verdictOf(t, j, 2); v != VerdictDisagree || !strings.Contains(reason, "sl") {
		t.Fatalf("verdict %s %s", v, reason)
	}
}

func TestHandlerLLMOnly(t *testing.T) {
	text := "Nifty 22800 call le lo 150 ke upar, stoploss 140" // regex finds no BUY
	r := &fakeReader{res: LLMResult{Kind: "new_call", Underlying: "NIFTY", Strike: 22800, OptionType: "CE",
		Trigger: "150", SL: "140", Action: "none", Confidence: "high"}}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, text), tNow)
	if len(s.calls) != 1 || s.calls[0].Source != "LLM_ONLY" || s.calls[0].Strike != 22800 || s.calls[0].SLPaise != 14000 {
		t.Fatalf("llm-only: %+v", s.calls)
	}

	r.res.Confidence = "medium"
	h.onMessage(context.Background(), msg(2, text), tNow)
	if len(s.calls) != 1 {
		t.Fatal("traded a medium-confidence LLM-only call")
	}
}

func TestHandlerFollowUpGetsReplyText(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	h, s, _ := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(10, realCall), tNow)

	r.res = LLMResult{Kind: "follow_up", Action: "move_sl", NewSL: "191", Confidence: "high"}
	reply := msg(11, "Sl ctc kar lena")
	reply.ReplyTo = 10
	h.onMessage(context.Background(), reply, tNow)
	if r.reply != realCall {
		t.Fatalf("LLM got reply text %q", r.reply)
	}
	if len(s.follows) != 1 || s.follows[0].CallKey() != "42:10" || s.follows[0].Action != model.TGMoveSL || s.follows[0].NewSLPaise != 19100 {
		t.Fatalf("follow-ups %+v", s.follows)
	}
}

func TestHandlerSkipsTicksAndLLMDown(t *testing.T) {
	r := &fakeReader{err: errors.New("HTTP 503")}
	h, s, j := newTestHandler(t, r)
	h.onMessage(context.Background(), msg(1, "193🔥🔥🔥"), tNow)
	if r.calls != 0 {
		t.Fatal("LLM called for a price tick")
	}
	if v, _ := verdictOf(t, j, 1); v != VerdictSkipped {
		t.Fatalf("tick verdict %s", v)
	}
	// LLM down: the regex still trades a call it reads.
	h.onMessage(context.Background(), msg(2, realCall), tNow)
	if len(s.calls) != 1 || s.calls[0].Source != "REGEX" {
		t.Fatalf("llm down: %+v", s.calls)
	}
}

func TestHandlerEdits(t *testing.T) {
	h, s, _ := newTestHandler(t, nil)
	h.onMessage(context.Background(), msg(1, realCall), tNow)
	ed := msg(1, strings.Replace(realCall, "182.00", "185.00", 1))
	ed.Edited = true
	h.onMessage(context.Background(), ed, tNow)
	h.onMessage(context.Background(), ed, tNow) // same edit again
	if len(s.calls) != 2 || !s.calls[1].Edited || s.calls[1].SLPaise != 18500 {
		t.Fatalf("edits %+v", s.calls)
	}
	// An edit made now to a call posted yesterday never trades.
	old := msg(2, strings.Replace(realCall, "182.00", "186.00", 1))
	old.Edited, old.Posted = true, tNow.Add(-24*time.Hour)
	h.onMessage(context.Background(), old, tNow)
	if len(s.calls) != 2 {
		t.Fatalf("edit of old call traded: %+v", s.calls[len(s.calls)-1])
	}
}

func TestNeedsLLM(t *testing.T) {
	for text, want := range map[string]bool{
		"193🔥🔥🔥": false, "Good morning trader’s": false, "First target done ✔️": true,
		realCall: true, "Ajao 3 pm plan krte hai youtube pe": false,
	} {
		if got := needsLLM(text, false); got != want {
			t.Errorf("needsLLM(%q) = %v", text, got)
		}
	}
	if needsLLM("Ajao bhai log", false) {
		t.Error("chat sent to the LLM")
	}
	if !needsLLM("jaldi niklo", true) {
		t.Error("reply to a call skipped")
	}
	for _, text := range []string{"Sl ctc kar lena", "Ctc exit", "Sl hit 😢", "Trailing sl hit", "aap sl 112 par le aao", "Avoid btst"} {
		if !needsLLM(text, false) {
			t.Errorf("follow-up %q skipped", text)
		}
	}
}

func TestJournalOutcome(t *testing.T) {
	h, _, j := newTestHandler(t, nil)
	h.onMessage(context.Background(), msg(5, realCall), tNow)
	if err := j.AddOutcome(42, 5, "ARMED"); err != nil {
		t.Fatal(err)
	}
	_ = j.AddOutcome(42, 5, "ENTERED")
	var out string
	_ = j.db.QueryRow(`SELECT outcome FROM messages WHERE msg_id = 5`).Scan(&out)
	if out != "ARMED | ENTERED" || j.TextOf(42, 5) != realCall {
		t.Fatalf("outcome %q", out)
	}
}

func TestNormalizeChatID(t *testing.T) {
	for in, want := range map[string]int64{"-1001234567890": 1234567890, "1234567890": 1234567890, "-4567": 4567, " 99 ": 99} {
		if got, ok := normalizeChatID(in); !ok || got != want {
			t.Errorf("normalizeChatID(%q) = %d, %v", in, got, ok)
		}
	}
	if _, ok := normalizeChatID("abc"); ok {
		t.Error("abc accepted")
	}
}

func TestHandlerStatusAndDashboardList(t *testing.T) {
	r := &fakeReader{res: llmCall(22750, "PE", "191", "182")}
	r.res.English, r.res.Usage = "Buy Nifty PE 22750", Usage{Total: 700}
	h, _, _ := newTestHandler(t, r)
	h.status = newStatusTracker(Config{ChatIDs: []int64{42}, LLM: true, LLMModel: "m", LLMBaseURL: "https://api.groq.com/openai/v1"})
	var shown []model.TGMessageView
	h.sink = func(v model.TGMessageView) { shown = append(shown, v) }

	h.onMessage(context.Background(), msg(1, realCall), tNow)
	h.onMessage(context.Background(), msg(2, "193🔥🔥🔥"), tNow)

	if len(shown) != 2 || shown[0].English != "Buy Nifty PE 22750" || shown[0].Verdict != VerdictSent ||
		shown[0].Summary != "BUY NIFTY 22750PE above 191.00 SL 182.00 (AGREE)" || shown[1].Verdict != VerdictSkipped {
		t.Fatalf("shown %+v", shown)
	}
	st := h.status.snapshot(time.Now())
	if st.LLM.Provider != "api.groq.com" || !st.LLM.OK || st.LLM.TokensToday != 700 || st.Verdicts[VerdictSent] != 1 || st.Verdicts[VerdictSkipped] != 1 {
		t.Fatalf("status %+v", st)
	}
}

// slowReader takes d to read, like a remote LLM.
type slowReader struct {
	fakeReader
	d time.Duration
}

func (s *slowReader) Read(ctx context.Context, text, reply string) (LLMResult, error) {
	time.Sleep(s.d)
	return s.fakeReader.Read(ctx, text, reply)
}

func TestHandlerLatencyStamps(t *testing.T) {
	r := &slowReader{fakeReader: fakeReader{res: llmCall(22750, "PE", "191", "182")}, d: 20 * time.Millisecond}
	h, s, j := newTestHandler(t, r)
	var shown []model.TGMessageView
	h.sink = func(v model.TGMessageView) { shown = append(shown, v) }
	recv := tNow.Add(700 * time.Millisecond)
	h.onMessage(context.Background(), msg(1, realCall), recv)

	if len(s.calls) != 1 {
		t.Fatalf("calls %+v", s.calls)
	}
	c := s.calls[0]
	if !c.SentAt.Equal(tNow) || !c.ReceivedAt.Equal(recv) || c.ReadMS < 20 || c.PublishedAt.IsZero() {
		t.Fatalf("stamps sent=%s recv=%s read=%d pub=%s", c.SentAt, c.ReceivedAt, c.ReadMS, c.PublishedAt)
	}
	if len(shown) != 1 || !shown[0].ReceivedAt.Equal(recv) || shown[0].ReadMS != c.ReadMS {
		t.Fatalf("shown %+v", shown)
	}
	var readMS int64
	var gotRecv time.Time
	if err := j.db.QueryRow(`SELECT read_ms, received_at FROM messages WHERE msg_id = 1`).Scan(&readMS, &gotRecv); err != nil {
		t.Fatal(err)
	}
	if readMS != c.ReadMS || !gotRecv.Equal(recv) {
		t.Fatalf("journal read_ms=%d received_at=%s", readMS, gotRecv)
	}
}

func TestJournalAddsColumnsToOldLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER NOT NULL, msg_id INTEGER NOT NULL,
		reply_to INTEGER NOT NULL DEFAULT 0, edited INTEGER NOT NULL DEFAULT 0, sent_at DATETIME NOT NULL, text TEXT NOT NULL,
		regex TEXT NOT NULL DEFAULT '', llm TEXT NOT NULL DEFAULT '', verdict TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL DEFAULT '', created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	err = j.Record(Entry{ChatID: 1, MsgID: 2, SentAt: tNow, Text: "x", Verdict: VerdictNoise, ReadMS: 5})
	j.Close()
	if err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path) // reopen: columns already there
	if err != nil {
		t.Fatal(err)
	}
	j.Close()
}

func TestOutcomeLine(t *testing.T) {
	ts := time.Date(2026, 10, 26, 10, 0, 3, 0, time.Local)
	if got := outcomeLine(model.TGOutcome{Event: "ENTERED", Detail: "premium=191", TS: ts, MS: 41250}); got != "10:00:03 ENTERED +41250ms: premium=191" {
		t.Errorf("got %q", got)
	}
	if got := outcomeLine(model.TGOutcome{Event: "DROPPED", TS: ts}); got != "10:00:03 DROPPED" {
		t.Errorf("got %q", got)
	}
}

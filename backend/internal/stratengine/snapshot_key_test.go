package stratengine

import (
	"encoding/json"
	"testing"
)

func TestUseExecutorKey(t *testing.T) {
	full := func(ts string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{"order_executor": json.RawMessage(`"full"`)}
		if ts != "" {
			m["ts"] = json.RawMessage(ts)
		}
		return m
	}
	key := []byte(`{"ts":200,"order_executor":"key"}`)
	for name, c := range map[string]struct {
		snap map[string]json.RawMessage
		want string
	}{
		"key newer":        {full("100"), `"key"`},
		"full newer":       {full("300"), `"full"`},
		"full unstamped":   {full(""), `"key"`},
		"same time (full)": {full("200"), `"full"`},
	} {
		useExecutorKey(c.snap, key)
		if got := string(c.snap["order_executor"]); got != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}
	s := full("100")
	if _, ok := useExecutorKey(s, []byte(`not json`)); ok || string(s["order_executor"]) != `"full"` {
		t.Error("bad key data used")
	}
}

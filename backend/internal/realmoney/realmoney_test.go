package realmoney

import (
	"io"
	"strings"
	"testing"
)

func TestConfirm(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		input string
		ok    bool
		rest  []string
	}{
		{"no flag", []string{"123"}, "REAL\n", false, nil},
		{"flag, wrong word", []string{Flag}, "yes\n", false, nil},
		{"flag, empty input", []string{Flag}, "", false, nil},
		{"flag, confirmed", []string{Flag}, "REAL\n", true, []string{}},
		{"flag anywhere, args kept", []string{"ORDER1", Flag, "x"}, " REAL \n", true, []string{"ORDER1", "x"}},
		{"confirmed without newline", []string{Flag}, "REAL", true, []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			rest, err := confirm(c.args, strings.NewReader(c.input), io.Discard, "places orders")
			if (err == nil) != c.ok {
				t.Fatalf("err = %v, want ok=%v", err, c.ok)
			}
			if c.ok && strings.Join(rest, ",") != strings.Join(c.rest, ",") {
				t.Fatalf("rest = %v, want %v", rest, c.rest)
			}
		})
	}
}

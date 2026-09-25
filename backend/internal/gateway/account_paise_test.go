package gateway

import "testing"

func TestRupeesToPaise(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"", 0}, {"0", 0}, {"0.29", 29}, {"1234.29", 123429}, {"0.1", 10}, {"5", 500},
		{"5.", 500}, {".5", 50}, {"-0.29", -29}, {"+12.00", 1200}, {"1.005", 101},
		{"1.004", 100}, {"  42.7 ", 4270}, {"1000000.99", 100000099},
	} {
		got, err := rupeesToPaise(c.in)
		if err != nil || got != c.want {
			t.Errorf("rupeesToPaise(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"abc", "1e3", "1.2.3", "-", "."} {
		if _, err := rupeesToPaise(bad); err == nil {
			t.Errorf("rupeesToPaise(%q) accepted, want error", bad)
		}
	}
}

package smartconnect

import (
	"os"
	"testing"
)

// The registered static IP from ANGEL_CLIENT_PUBLIC_IP is sent as
// X-ClientPublicIP, with no lookup.
func TestClientPublicIPFromEnv(t *testing.T) {
	t.Setenv("ANGEL_CLIENT_PUBLIC_IP", " 203.0.113.7 ")
	wd, _ := os.Getwd()
	t.Chdir(t.TempDir()) // the client writes logs/ under the working dir
	defer os.Chdir(wd)
	sc := NewSmartConnect(Config{APIKey: "k", ClientLocalIP: "10.0.0.1"})
	if got := sc.requestHeaders().Get("X-ClientPublicIP"); got != "203.0.113.7" {
		t.Fatalf("X-ClientPublicIP %q", got)
	}
	if got := sc.requestHeaders().Get("X-ClientLocalIP"); got != "10.0.0.1" {
		t.Fatalf("X-ClientLocalIP %q (configured one overwritten)", got)
	}
}

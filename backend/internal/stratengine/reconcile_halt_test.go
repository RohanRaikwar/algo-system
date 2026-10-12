package stratengine

import "testing"

// Unsafe reconcile drift blocks entries exactly like the kill switch.
func TestReconcileHaltActsAsKillSwitch(t *testing.T) {
	svc := &Service{}
	if svc.killSwitchActive() {
		t.Fatal("kill switch active by default")
	}
	svc.reconcileHalt.Store(true)
	if !svc.killSwitchActive() {
		t.Fatal("reconcile halt does not block entries")
	}
}

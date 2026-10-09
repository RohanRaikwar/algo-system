package stratengine

import "testing"

// recordOldChoice is the mechanism behind F6: it attaches what the old
// (non-picker) path chose to the picker's own decision, purely for the
// shadow-mode go/no-go review — it never changes what either path does.

func TestRecordOldChoiceSingleEntryAgreeAndDisagree(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.recordPickerDecision(pickerDecision{Strategy: "S", Mode: "shadow", Result: "picked", Symbol: "PICKED_SYM"})

	svc.recordOldChoice("S", "PICKED_SYM")
	if d := svc.lastPickerDecision("S"); d.OldSymbol != "PICKED_SYM" || !d.Agree {
		t.Fatalf("agree case: %+v", d)
	}

	svc.recordOldChoice("S", "OTHER_SYM")
	if d := svc.lastPickerDecision("S"); d.OldSymbol != "OTHER_SYM" || d.Agree {
		t.Fatalf("disagree case: %+v", d)
	}
}

func TestRecordOldChoiceNoOpWithoutAPickerDecision(t *testing.T) {
	svc := deltaSvc(&fakeGreeks{}, 2270000)
	svc.recordOldChoice("NEVER_DECIDED", "X") // PickerMode "off": pickEntry never ran
	if d := svc.lastPickerDecision("NEVER_DECIDED"); d.Strategy != "" {
		t.Fatalf("no-op created a decision: %+v", d)
	}
}

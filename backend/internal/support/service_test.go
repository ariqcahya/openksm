package support

import "testing"

func TestComplaintTransitions_SLAAndReopen(t *testing.T) {
	if !CanTransition("open", "assigned") {
		t.Fatal("expected open -> assigned to be valid")
	}
	if !CanTransition("assigned", "resolved") {
		t.Fatal("expected assigned -> resolved to be valid")
	}
	if !CanTransition("resolved", "closed") {
		t.Fatal("expected resolved -> closed to be valid")
	}
	if !CanTransition("closed", "reopened") {
		t.Fatal("expected closed -> reopened to be valid")
	}
	if CanTransition("open", "closed") {
		t.Fatal("expected open -> closed to be rejected")
	}
}

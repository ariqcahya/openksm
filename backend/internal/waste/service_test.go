package waste

import (
	"errors"
	"testing"
)

func TestWasteValidation(t *testing.T) {
	svc := NewService(nil)
	if _, err := svc.RecordIntake(t.Context(), "u1", "TPS3R A", "PET", "SCALE-1", "k1", 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for zero weight, got %v", err)
	}
	if _, err := svc.FulfillSale(t.Context(), "u1", "Buyer", "PET", "k1", -5, 1000); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for negative quantity, got %v", err)
	}
}

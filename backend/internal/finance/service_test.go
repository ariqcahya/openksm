package finance

import (
	"errors"
	"testing"
)

func TestFinanceValidation(t *testing.T) {
	svc := NewService(nil)
	if _, err := svc.CreateTariff(t.Context(), "", 10000, "2026-01-01"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty tariff label, got %v", err)
	}
	if _, err := svc.CreateTariff(t.Context(), "Tarif", -100, "2026-01-01"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for negative tariff amount, got %v", err)
	}
	if _, err := svc.RecordPayment(t.Context(), "u1", "inv-1", "k1", "cash", 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for zero payment amount, got %v", err)
	}
}

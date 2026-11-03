package domain

import "testing"

// TestOrderStatusNormalize pins the CONFIRMED/PREPARING merge: an older client that still sends
// CONFIRMED gets PREPARING saved, every other status is kept as sent.
func TestOrderStatusNormalize(t *testing.T) {
	if got := OrderStatus("CONFIRMED").Normalize(); got != OrderStatusPreparing {
		t.Fatalf("CONFIRMED must normalize to PREPARING, got %s", got)
	}
	for _, s := range []OrderStatus{
		OrderStatusPending, OrderStatusPreparing, OrderStatusAwaitingUp, OrderStatusPickedUp,
		OrderStatusOutForDelivery, OrderStatusDelivered, OrderStatusCanceled, OrderStatusFailed,
	} {
		if got := s.Normalize(); got != s {
			t.Errorf("%s must stay %s, got %s", s, s, got)
		}
	}
}

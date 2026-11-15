package discount

import "testing"

// The suite is deliberately incomplete: nothing covers the 100% clamp.

func TestFullPrice(t *testing.T) {
	if got := Price(1000, 0); got != 1000 {
		t.Fatalf("Price(1000, 0) = %d, want 1000", got)
	}
}

func TestHalfOff(t *testing.T) {
	if got := Price(1000, 50); got != 500 {
		t.Fatalf("Price(1000, 50) = %d, want 500", got)
	}
}

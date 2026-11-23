package application

import (
	"testing"
	"time"
)

func TestSystemClock(t *testing.T) {
	var c Clock = SystemClock{}
	if d := time.Since(c.Now()); d < 0 || d > time.Second {
		t.Fatalf("Now() lệch: %v", d)
	}
}

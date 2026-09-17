package nodeagent

import (
	"context"
	"testing"
	"time"
)

func TestBackoffDoublesUpToMax(t *testing.T) {
	b := newBackoff(time.Second, 10*time.Second)

	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		10 * time.Second, // would be 16s uncapped; clamped to max
		10 * time.Second,
	}
	for i, w := range want {
		if got := b.next(); got != w {
			t.Errorf("next() call %d = %s, want %s", i+1, got, w)
		}
	}
}

func TestBackoffResetReturnsToBase(t *testing.T) {
	b := newBackoff(time.Second, time.Minute)

	b.next()
	b.next()
	b.reset()

	if got := b.next(); got != time.Second {
		t.Errorf("next() after reset = %s, want base (1s)", got)
	}
}

func TestBackoffNeverExceedsMaxEvenAfterManyAttempts(t *testing.T) {
	b := newBackoff(time.Second, 30*time.Second)

	for i := 0; i < 100; i++ {
		if got := b.next(); got > 30*time.Second {
			t.Fatalf("next() call %d = %s, want <= 30s", i+1, got)
		}
	}
}

func TestSleepReturnsAfterDuration(t *testing.T) {
	start := time.Now()
	if err := sleep(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("sleep returned after %s, want >= 20ms", elapsed)
	}
}

func TestSleepReturnsEarlyOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := sleep(ctx, time.Minute)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("sleep succeeded on a cancelled context, want an error")
	}
	if elapsed > time.Second {
		t.Errorf("sleep took %s to return after cancellation, want near-instant", elapsed)
	}
}

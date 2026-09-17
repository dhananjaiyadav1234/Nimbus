package nodeagent

import (
	"context"
	"time"
)

// backoff produces a bounded, deterministic exponential retry delay
// sequence: base, 2*base, 4*base, ... capped at max. It has no jitter,
// deliberately — jitter would make the sequence non-deterministic and
// harder to unit test, and Nimbus has at most a handful of agents in any
// realistic deployment, so a thundering herd of retries is not a concern
// this phase needs to solve.
type backoff struct {
	base    time.Duration
	max     time.Duration
	attempt int
}

func newBackoff(base, max time.Duration) *backoff {
	return &backoff{base: base, max: max}
}

// next returns the delay for the next retry and advances the sequence.
func (b *backoff) next() time.Duration {
	d := b.base << b.attempt // base * 2^attempt
	if d <= 0 || d > b.max {
		d = b.max
	}
	b.attempt++
	return d
}

// reset returns the sequence to its first delay. Call it after a successful
// call, so the *next* failure starts backing off from base again rather than
// wherever a previous, unrelated failure streak left off.
func (b *backoff) reset() {
	b.attempt = 0
}

// sleep waits for d, or returns ctx.Err() early if ctx is cancelled first —
// so a shutdown signal interrupts a retry wait immediately instead of
// blocking it out.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

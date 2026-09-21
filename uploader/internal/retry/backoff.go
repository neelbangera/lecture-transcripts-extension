// Package retry computes the capped, jittered delay between durable upload
// attempts.  The attempt count lives in the queue row and therefore survives
// restarts; Backoff itself is stateless so a restarted process cannot lose its
// position in the schedule.
package retry

import (
	"math/rand/v2"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
)

// Backoff applies the shared uploader schedule to an attempt count.
//
// The schedule source is config.BackoffSchedule; this package intentionally
// does not define a second schedule.
type Backoff struct {
	schedule []time.Duration
	jitter   float64
	source   func() float64
}

// New returns a Backoff using the machine-local schedule and jitter fraction.
func New() Backoff {
	return Backoff{
		schedule: config.BackoffSchedule(),
		jitter:   config.BackoffJitterFraction,
		source:   rand.Float64,
	}
}

// NewWithSource returns a Backoff whose jitter source is injectable for
// deterministic tests.  A nil source falls back to the default.
func NewWithSource(source func() float64) Backoff {
	backoff := New()
	if source != nil {
		backoff.source = source
	}
	return backoff
}

// Schedule returns a copy of the configured base delays.
func (b Backoff) Schedule() []time.Duration {
	delays := make([]time.Duration, len(b.schedule))
	copy(delays, b.schedule)
	return delays
}

// BaseDelay returns the un-jittered delay for the attempt count.  The index is
// min(attemptCount, len(schedule)-1), so the delay stays capped at the last
// schedule entry for every later attempt.
func (b Backoff) BaseDelay(attemptCount int) time.Duration {
	if attemptCount < 0 {
		attemptCount = 0
	}
	if len(b.schedule) == 0 {
		return 0
	}
	index := attemptCount
	if index >= len(b.schedule) {
		index = len(b.schedule) - 1
	}
	return b.schedule[index]
}

// Delay returns the base delay with +/- jitter applied.  The jitter factor is
// uniform in [1-jitter, 1+jitter).  A successful upload resets the persisted
// attempt count to zero; calling Delay(0) is the reset behavior.
func (b Backoff) Delay(attemptCount int) time.Duration {
	base := b.BaseDelay(attemptCount)
	if base <= 0 || b.jitter <= 0 {
		return base
	}
	factor := 1 - b.jitter + 2*b.jitter*b.source()
	return time.Duration(float64(base) * factor)
}

// NextAttemptAt returns the wall-clock time of the next attempt.  Callers
// persist that time and the incremented attempt count together.
func (b Backoff) NextAttemptAt(attemptCount int, now time.Time) time.Time {
	return now.Add(b.Delay(attemptCount))
}

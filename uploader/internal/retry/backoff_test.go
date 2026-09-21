package retry

import (
	"testing"
	"time"

	"github.com/neelbangera/lecture-transcripts-extension/uploader/internal/config"
)

func neutralSource() func() float64 {
	return func() float64 { return 0.5 }
}

func TestScheduleMatchesConfig(t *testing.T) {
	got := New().Schedule()
	want := config.BackoffSchedule()
	if len(got) != len(want) {
		t.Fatalf("schedule length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("schedule[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestDelayFollowsSchedule(t *testing.T) {
	backoff := NewWithSource(neutralSource())
	want := []time.Duration{
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		time.Hour,
	}
	for attempt, expected := range want {
		if got := backoff.Delay(attempt); got != expected {
			t.Fatalf("Delay(%d) = %s, want %s", attempt, got, expected)
		}
		if got := backoff.BaseDelay(attempt); got != expected {
			t.Fatalf("BaseDelay(%d) = %s, want %s", attempt, got, expected)
		}
	}
}

func TestDelayStaysCappedAfterLastAttempt(t *testing.T) {
	backoff := NewWithSource(neutralSource())
	for _, attempt := range []int{5, 6, 25, 1000} {
		if got := backoff.Delay(attempt); got != time.Hour {
			t.Fatalf("Delay(%d) = %s, want %s", attempt, got, time.Hour)
		}
	}
}

func TestNegativeAttemptCountUsesFirstDelay(t *testing.T) {
	backoff := NewWithSource(neutralSource())
	if got := backoff.Delay(-3); got != 5*time.Second {
		t.Fatalf("Delay(-3) = %s, want 5s", got)
	}
}

func TestJitterBounds(t *testing.T) {
	sources := []float64{0, 0.25, 0.5, 0.75, 0.999999}
	for _, attempt := range []int{0, 2, 4} {
		base := New().BaseDelay(attempt)
		lower := time.Duration(float64(base) * (1 - config.BackoffJitterFraction))
		upper := time.Duration(float64(base) * (1 + config.BackoffJitterFraction))
		for _, value := range sources {
			source := value
			backoff := NewWithSource(func() float64 { return source })
			got := backoff.Delay(attempt)
			if got < lower || got > upper {
				t.Fatalf("Delay(%d) with source %v = %s, want within [%s, %s]", attempt, value, got, lower, upper)
			}
		}
	}
}

func TestJitterExtremes(t *testing.T) {
	lower := NewWithSource(func() float64 { return 0 })
	if got, want := lower.Delay(0), time.Duration(float64(5*time.Second)*(1-config.BackoffJitterFraction)); got != want {
		t.Fatalf("minimum jitter delay = %s, want %s", got, want)
	}
	almostOne := NewWithSource(func() float64 { return 0.999999 })
	got := almostOne.Delay(4)
	want := time.Duration(float64(time.Hour) * (1 + config.BackoffJitterFraction))
	if got >= want {
		t.Fatalf("maximum jitter delay = %s, want strictly below %s", got, want)
	}
}

func TestNextAttemptAtUsesProvidedClock(t *testing.T) {
	clock := time.Date(2026, 2, 12, 8, 30, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	backoff := NewWithSource(neutralSource())

	if got, want := backoff.NextAttemptAt(0, now()), clock.Add(5*time.Second); !got.Equal(want) {
		t.Fatalf("NextAttemptAt(0) = %s, want %s", got, want)
	}
	if got, want := backoff.NextAttemptAt(3, now()), clock.Add(10*time.Minute); !got.Equal(want) {
		t.Fatalf("NextAttemptAt(3) = %s, want %s", got, want)
	}
	if got, want := backoff.NextAttemptAt(9, now()), clock.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("NextAttemptAt(9) = %s, want %s", got, want)
	}
}

func TestResetBehaviorReturnsToFirstDelay(t *testing.T) {
	backoff := NewWithSource(neutralSource())
	for attempt := 0; attempt < 6; attempt++ {
		_ = backoff.Delay(attempt)
	}
	if got := backoff.Delay(0); got != 5*time.Second {
		t.Fatalf("delay after reset = %s, want 5s", got)
	}
}

// TestAttemptCountPersistenceIsStateless models a host restart: the queue row
// carries attempt_count across processes, so two independent Backoff values
// must agree for the same attempt count.
func TestAttemptCountPersistenceIsStateless(t *testing.T) {
	first := NewWithSource(neutralSource())
	second := NewWithSource(neutralSource())
	for attempt := 0; attempt <= 6; attempt++ {
		if a, b := first.BaseDelay(attempt), second.BaseDelay(attempt); a != b {
			t.Fatalf("attempt %d differs across instances: %s vs %s", attempt, a, b)
		}
	}
	if first.BaseDelay(4) != first.BaseDelay(7) {
		t.Fatal("capped attempt counts must not change the delay")
	}
}

func TestDelayWithCustomSourceIsUsed(t *testing.T) {
	calls := 0
	backoff := NewWithSource(func() float64 {
		calls++
		return 0.5
	})
	_ = backoff.Delay(1)
	if calls != 1 {
		t.Fatalf("jitter source calls = %d, want 1", calls)
	}
}

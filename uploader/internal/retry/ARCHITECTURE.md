# retry Architecture

## Purpose

`uploader/internal/retry` computes the delay before the next durable upload
attempt. It is stateless: the attempt count lives in the SQLite queue row, so a
restarted process cannot lose its position in the schedule. It defines no
schedule of its own — it reads `config.BackoffSchedule()` — and exposes an
injectable jitter source for deterministic tests.

## Boundaries and dependencies

- Imports `math/rand/v2`, `time`, and the uploader's `config` package (schedule
  and jitter fraction).
- Consumers: `processor` calls `NextAttemptAt(job.AttemptCount, now)` when it
  persists `retryable_error`; `processor.Config.Backoff` is a `retry.Backoff`
  and `processor.New` falls back to `retry.New()` when the supplied schedule is
  empty.
- The package has no clock dependency and no I/O. Callers pass `now`; this is
  the clock injection point. Tests use a fixed `time.Time`
  (`TestNextAttemptAtUsesProvidedClock`) or the processor's `testClock`.

## Contracts and invariants

### Schedule

The base schedule is exactly `config.BackoffSchedule()`:

| Index | Delay |
| --- | --- |
| 0 | `5 * time.Second` |
| 1 | `30 * time.Second` |
| 2 | `2 * time.Minute` |
| 3 | `10 * time.Minute` |
| 4 | `time.Hour` |

`Schedule()` returns a copy, so a caller cannot mutate the shared slice.
`config.BackoffJitterFraction` is `0.20`.

### Attempt-count semantics

`BaseDelay(attemptCount)`:

- clamps a negative count to 0;
- uses `index = min(attemptCount, len(schedule)-1)`, so the delay stays capped
  at 1 hour for every later attempt;
- returns 0 if the schedule is empty.

`processor.scheduleRetry` computes the delay from the **pre-increment**
`job.AttemptCount` and then `MarkRetryableError` increments the stored count.
That pairing makes the first failure use index 0 (5s) and the fifth failure use
index 4 (1h), matching the plan's `index=min(attempt_count,len-1)` rule.
`TestDelayFollowsSchedule`, `TestDelayStaysCappedAfterLastAttempt`, and
`TestNegativeAttemptCountUsesFirstDelay` pin these behaviors.

A successful upload resets the attempt count to zero in the queue row
(`MarkUploaded`/`MarkUnchanged` do not increment it); `Delay(0)` is therefore
the reset behavior and returns 5 seconds
(`TestResetBehaviorReturnsToFirstDelay`). `TestAttemptCountPersistenceIsStateless`
models a host restart with two independent `Backoff` values and proves they
agree for the same attempt count.

### Jitter

`Delay(attemptCount)` applies a uniform factor in `[1-jitter, 1+jitter)`:

```go
factor := 1 - b.jitter + 2*b.jitter*b.source()
return time.Duration(float64(base) * factor)
```

- `New()` uses `rand.Float64` and `config.BackoffJitterFraction`.
- `NewWithSource(source)` injects a deterministic source; a nil source falls
  back to the default.
- A non-positive base or jitter returns the base unchanged.
- `TestJitterBounds` checks sources `{0, 0.25, 0.5, 0.75, 0.999999}` against
  `[base*0.8, base*1.2]` for attempts 0/2/4; `TestJitterExtremes` checks the
  exact lower bound and that the upper bound is strictly below `base*1.2`;
  `TestDelayWithCustomSourceIsUsed` proves the source is called once per
  `Delay`.

### API

```go
type Backoff struct { /* schedule, jitter, source */ }

func New() Backoff
func NewWithSource(source func() float64) Backoff
func (b Backoff) Schedule() []time.Duration
func (b Backoff) BaseDelay(attemptCount int) time.Duration
func (b Backoff) Delay(attemptCount int) time.Duration
func (b Backoff) NextAttemptAt(attemptCount int, now time.Time) time.Time
```

`NextAttemptAt` is `now.Add(Delay(attemptCount))`; callers persist that
wall-clock value and the incremented attempt count together (the queue's
`MarkRetryableError`).

## Data flow

```
github failure (retryable)
  └─ processor.handlePublishError / scheduleRetry
       ├─ nextAttemptAt := p.backoff.NextAttemptAt(job.AttemptCount, now)
       └─ queue.MarkRetryableError(id, category, httpStatus, nextAttemptAt, now)
            └─ row: status=retryable_error, attempt_count+1, next_attempt_at

later
  └─ queue.PromoteDueRetries(now)  -> status=queued, next_attempt_at=NULL
       └─ processor claims and retries; a success resets the attempt count
```

Retries are indefinite: the delay is capped, not the number of attempts. Only
the uploader retries; the extension retains no full copy after acknowledgement.

## File responsibilities

| File | Role | Key exports |
| --- | --- | --- |
| `backoff.go` | Capped schedule, jitter, attempt-count mapping, clock-parameterized next-attempt time | `Backoff`, `New`, `NewWithSource`, `Backoff.Schedule`, `Backoff.BaseDelay`, `Backoff.Delay`, `Backoff.NextAttemptAt` |
| `backoff_test.go` | Schedule, cap, jitter bounds, reset, statelessness, clock, source-injection tests | `TestScheduleMatchesConfig`, `TestDelayFollowsSchedule`, `TestDelayStaysCappedAfterLastAttempt`, `TestNegativeAttemptCountUsesFirstDelay`, `TestJitterBounds`, `TestJitterExtremes`, `TestNextAttemptAtUsesProvidedClock`, `TestResetBehaviorReturnsToFirstDelay`, `TestAttemptCountPersistenceIsStateless`, `TestDelayWithCustomSourceIsUsed` |

## Testing and verification

Run `cd uploader && go test ./internal/retry/ -count=1`. The suite verifies the
schedule equals `config.BackoffSchedule()`, every index and the 1-hour cap,
negative-count handling, jitter bounds and extremes, `NextAttemptAt` against an
injected clock value, reset semantics, and that two independent `Backoff`
instances agree for the same attempt count (restart safety). No test sleeps;
all timing is arithmetic.

## Related plan sections

- `TECHNICAL_PLAN.md` → "Transcript job and state model" (per-job backoff
  `5s,30s,2m,10m,1h` with ±20% jitter, `index=min(attempt_count,len-1)`, reset
  on success, attempt count persists across restarts).
- `TECHNICAL_PLAN.md` → "Canonical status vocabulary" (`retryable_error` is
  durable, never auto-pruned).
- `TECHNICAL_PLAN.md` → "Stage 6" item 6 (only `retryable` auto-retries).
- `docs/TROUBLESHOOTING.md` → "Alarm-delayed retries".

## How to change this package safely

1. Change the schedule only in `config.BackoffSchedule()` and update
   `TECHNICAL_PLAN.md` first; `TestScheduleMatchesConfig` and the config
   contract test will fail otherwise.
2. Keep the cap rule `index = min(attemptCount, len(schedule)-1)`; the plan
   fixes it.
3. Keep the package stateless and clock-free. Do not add a `time.Now` call or
   a timer; callers own persistence and the processor owns sleeping.
4. Keep `NewWithSource` for deterministic tests; never make jitter optional in
   production.
5. Update `TestJitterExtremes` only if the jitter fraction in `config`
   changes. No open questions remain: the schedule, jitter math, cap, reset,
   and clock parameter are all directly verifiable in `backoff.go` and
   `backoff_test.go`.

# ADR 0005: Retries use exponential backoff with full jitter

## Status

Accepted

## Context

Two kinds of failure show up when several posters hit one Postgres: serialization
failures (`40001`) and deadlocks (`40P01`), plus connections that drop when the
database restarts. Retrying them immediately, on a fixed delay, lines the
retries up and repeats the collision.

## Decision

Only transient errors are retried: `40001`, `40P01`, unexpected EOF, and
network errors. Context cancellation, constraint violations, and business
rejections (unbalanced entries, illegal settlement transitions) return at once.

The delay after failure `n` (starting at 0) is

```
sleep = rand(0, min(cap, base * 2^n))
```

with the upper bound exclusive. The default budget is 5 attempts, a 20ms base,
and a 500ms cap, so a request does not sit in the handler for long. The settler
uses the same knobs via `LEDGER_RETRY_MAX`, `LEDGER_RETRY_BASE`, and
`LEDGER_RETRY_CAP`.

When the budget is spent, the settlement payload is inserted into `dead_letters`
and the HTTP API responds 503 with `dead_letter_id`. `POST /dead-letters/{id}/replay`
runs the apply again. A letter that is already replayed returns the stored result.

The posting transaction helper uses the same classifier, so a deadlock inside
`Post` retries the whole transaction. The idempotency key makes that retry safe
if the first commit actually landed and the client only saw a connection error.

## Consequences

- A thundering herd spreads out instead of retrying on the same millisecond.
- Permanent mistakes are not parked and retried until they look transient.
- Tests inject a fake clock and a scripted random source, so the bounds are
  asserted without sleeping.

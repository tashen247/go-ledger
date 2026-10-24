# ADR 0004: Settlement changes state only from pending

## Status

Accepted

## Context

A payment feed delivers events at least once, and not always in an order the
application likes. Applying "settled" twice must not create two rows, and a
later "failed" must not undo a settled payment.

## Decision

`settlements.external_id` is unique. Apply does, in one transaction:

```sql
INSERT INTO settlements (id, external_id, transaction_id, amount, currency)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (external_id) DO NOTHING;

UPDATE settlements
SET state = $6
WHERE external_id = $2 AND state = 'pending';
```

Zero rows updated means the event was already applied. The response is 200
with `"applied": false`. `settled` and `failed` are terminal. A second status
that differs is 409. A repeat of the same external id with a different
amount, currency, or transaction is also 409, so a mismatched payload is not
silently ignored.

A row trigger rejects any update that is not `pending -> settled|failed` or a
no-op. The `WHERE state = 'pending'` guard is what the application uses; the
trigger is what still holds if something talks to Postgres directly.

Deliveries of the same external id take an advisory lock so two workers do
not both observe `pending`. The currency on the event must match the
transaction. The amount is not required to equal the positive leg at apply
time. Reconciliation compares those integers and reports a mismatch. That
keeps the feed path idempotent and makes drift visible instead of hiding it.

## Consequences

- Replaying a dead letter is safe. A second apply of an event that already
  landed updates zero rows.
- Out-of-order `failed` after `settled` is a conflict the caller can see.
- An operator who updates `state` with SQL still cannot move a terminal row.

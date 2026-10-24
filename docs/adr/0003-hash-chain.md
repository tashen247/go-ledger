# ADR 0003: One HMAC chain, guarded by an advisory lock

## Status

Accepted

## Context

A ledger should show whether a row was rewritten after it was posted. An
append-only trigger stops `UPDATE`, `DELETE`, and `TRUNCATE` from the SQL
role that owns the table. It does not stop a superuser who disables the
trigger, and it does not tell an operator which row changed. A hash chain does.

## Decision

Each `ledger_entries` row stores

```
hash = HMAC-SHA256(key, prev_hash || canonical(entry))
```

`canonical` is the UTF-8 string

```
v1|{seq}|{transaction_id}|{account_id}|{amount}|{currency}|{created_at}
```

with `created_at` in UTC RFC3339Nano, truncated to microseconds before it is
hashed so it matches `timestamptz`. The first `prev_hash` is 32 zero bytes.
The HMAC key comes from `LEDGER_HMAC_KEY` and is not stored in the database.

Writers take `pg_advisory_xact_lock` on a single chain class before they read
the head. The transaction is **Read Committed**. A Serializable snapshot is
taken at the first statement, which would be the lock wait, and would then
miss the commit it waited for. Two writers would chain off the same parent.
Read Committed plus the advisory lock sees the head that the previous holder
just committed.

`make verify-chain` and `GET /reconciliation` recompute the chain and report
the first broken seq. The hash is calculated before `INSERT` because the
append-only trigger rejects the `UPDATE` that would otherwise fill it in.
Sequence values come from `nextval`, so a rollback can leave a gap. Gaps do
not break the chain; links are hashes, not `seq - 1`.

Corrections are reversing entries. The trigger is there so an ordinary
session cannot "fix" a row in place.

## Consequences

- Posting throughput is bounded by one lock. That is the trade-off called out
  in the project brief. Per-account chains would remove it and are not in
  this version.
- The key is a single secret. Rotation would need a key id on each row.
- `verify-chain` loads the chain into memory. That is fine while the table
  is small enough to read in one sitting.

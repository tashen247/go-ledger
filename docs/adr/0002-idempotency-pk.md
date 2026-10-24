# ADR 0002: Idempotency is a primary key

## Status

Accepted

## Context

Clients retry when they time out. If the first request committed and the
response was lost, a second insert creates a second transaction. Application
locks around "have I seen this key?" lose to two app instances.

## Decision

`Idempotency-Key` is the primary key of `idempotency_keys`. The row stores a
SHA-256 of the raw request body and the exact response JSON. The key row and
the ledger rows commit in the same database transaction, so a rollback cannot
leave a key that points at a transaction that does not exist, and a commit
cannot leave a transaction without its key.

A second request takes `pg_try_advisory_xact_lock` on the key:

- the lock is busy: the original request is in flight, respond 409
- a completed row with the same hash: replay the stored response with
  `Idempotent-Replayed: true` and status 201
- a completed row with a different hash: 422, and do not post
- no row: post, then insert the key as `completed`

The body hash is the raw bytes. Different whitespace is a different request.
The key is not written when validation fails, so a client can fix the body
and reuse the key.

## Consequences

- Uniqueness is enforced by Postgres, including across processes.
- The 409 path does not wait. A client that receives 409 retries the same body.
- Replays cannot drift, because the bytes written to the client are the bytes
  stored in the row.
- The advisory lock is 32 bits of `hashtext`. A collision would surface as a
  false 409. That is acceptable next to a primary key that still prevents a
  double post.

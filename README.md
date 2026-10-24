# go-ledger

A small, correctness-first ledger service in Go. It shows the patterns that matter when money moves: an append-only, HMAC-signed double-entry ledger; amounts in integer minor units; idempotent writes keyed on a database primary key; settlement that stays correct under at-least-once delivery; jittered retries with dead-letter recovery; and reconciliation that has to match to the cent.

It is deliberately small enough to read in one sitting. The design follows patterns used in production ledger work. The code and numbers here belong to this repo alone.

## Why this exists

Ledger bugs rarely come from the happy path. They come from a client retrying after a timeout, a payment feed delivering the same event twice, or a database restart at the wrong moment. Each of those failure modes has an explicit answer here, with a test or a load scenario that proves it.

## Design

```mermaid
flowchart LR
  C[Client] -->|Idempotency-Key| API[HTTP API]
  API --> PG[(Postgres)]
  F[Settlement feed<br/>at-least-once] --> S[Settler]
  S -->|retry w/ jitter| PG
  S -->|exhausted| DL[(dead_letters)]
  DL -->|replay| S
  R[GET /reconciliation] --> PG
```

- **Money is an integer.** Amounts are `int64` minor units with an ISO-4217 currency. Floats never touch a balance.
- **Double-entry, append-only.** Every transaction writes entries that sum to zero. A Postgres trigger blocks UPDATE, DELETE, and TRUNCATE on `ledger_entries`. Mistakes are corrected with reversing entries.
- **Tamper-evident.** Each entry is signed with `HMAC-SHA256(key, prev_hash || entry)`, forming one global hash chain. `make verify-chain` recomputes it and points at the first broken link.
- **Idempotency through the primary key.** The `Idempotency-Key` header is the primary key of `idempotency_keys`. A repeat with the same raw body replays the stored response, including status 201, and sets `Idempotent-Replayed: true`. A repeat with a different body is 422. A key whose request is still in flight is 409. The key row and the posting commit in the same transaction.
- **Settlement is idempotent by state.** Transitions only fire `WHERE state = 'pending'`, so a duplicate event updates zero rows. A trigger rejects any other transition. The same `external_id` with a different amount, currency, or transaction is 409.
- **Retries with full jitter.** Only transient errors are retried (serialization failures `40001`, deadlocks `40P01`, lost connections): `sleep = rand(0, min(cap, base * 2^n))`. Once attempts are exhausted, the settlement is parked in `dead_letters` and can be replayed through the API.
- **Exact reconciliation.** Cached balances are checked against the sum of entries, every transaction must net to zero, settled amounts must match the positive legs, and the chain must verify. There is no tolerance.

The posting transaction is Read Committed. A serializable snapshot would be taken before the chain lock is acquired and would miss the commit it waited for. That choice, and the single global chain lock, is written up in [`docs/adr`](docs/adr).

## Run it

```bash
cp .env.example .env          # set LEDGER_HMAC_KEY (at least 16 bytes)
make up                       # postgres + migrate + api + settler via docker compose
make test
make verify-chain
```

`make up` applies migrations before the API starts. `make migrate` applies them again from the host when `DATABASE_URL` points at Postgres.

Requires Docker and Go 1.22+.

Without Docker, point `DATABASE_URL` at Postgres 16, then:

```bash
export DATABASE_URL=postgres://ledger:ledger@127.0.0.1:5432/ledger?sslmode=disable
export LEDGER_HMAC_KEY=dev-only-change-me-please-32b
make migrate
go run ./cmd/api
```

The settler reads a JSONL feed. One JSON object per line, the same shape as `POST /settlements`. Run it once, or leave it tailing:

```bash
go run ./cmd/settler -file feed/events.jsonl
go run ./cmd/settler -watch -file feed/events.jsonl
```

A line on stdin works when `-file` is omitted. Applying the same file twice is safe: settlement is idempotent by `external_id`.

## API

| Method | Path | Notes |
|---|---|---|
| POST | `/accounts` | Create an account. Currency is fixed on the account. |
| POST | `/transactions` | Requires `Idempotency-Key`. Entries must net to zero. |
| GET | `/accounts/{id}/balance` | Balance in minor units, plus `as_of_seq`. |
| POST | `/settlements` | At-least-once feed input. Duplicates are no-ops. |
| GET | `/reconciliation` | Exact checks and chain verification. |
| GET | `/dead-letters?status=parked` | List parked or replayed messages. |
| POST | `/dead-letters/{id}/replay` | Re-run a parked settlement. |
| GET | `/healthz` | Database ping. |

Errors are `application/problem+json`. `type` is `https://github.com/tashen247/go-ledger/problems/` plus a slug. The slugs are listed in [`docs/problems.md`](docs/problems.md).

```bash
curl -s -X POST localhost:8080/accounts \
  -H 'Content-Type: application/json' \
  -d '{"name":"cash","currency":"ZAR","type":"asset"}'

curl -s -X POST localhost:8080/accounts \
  -H 'Content-Type: application/json' \
  -d '{"name":"alice-wallet","currency":"ZAR","type":"liability"}'

curl -s -X POST localhost:8080/transactions \
  -H 'Idempotency-Key: 7f9c2e1a-4b6d-4c1e-9a2f-1d3e5b7c9a01' \
  -H 'Content-Type: application/json' \
  -d '{"reference":"deposit-123","entries":[
        {"account_id":"acc_cash","amount":15000},
        {"account_id":"acc_alice","amount":-15000}]}'
```

Use the account ids returned by `POST /accounts`. Amounts are integer minor units: `15000` is R150.00. The idempotency hash is the raw body, so a retry has to send the same bytes.

`POST /settlements` returns `"applied": false` when that `external_id` was already moved to the requested state. A settled payment that later arrives as `failed` is 409.

## Proving it

| Failure mode | How it's tested |
|---|---|
| Client retries after timeout | 50 concurrent requests with one key create exactly one transaction; a later call replays the stored body |
| Duplicate settlement events | The same `external_id` posted again returns `applied: false`; a JSONL file with the line twice settles once |
| Someone edits history | UPDATE, DELETE, and TRUNCATE on `ledger_entries` fail; a hand-inserted hash breaks `GET /reconciliation` |
| Retries run out | Transient settlement errors are retried with full jitter, then parked; replay applies the event once |
| Drift between cache and ledger | `GET /reconciliation` returns `ok: false` if `accounts.balance` or a settled amount disagrees with the entries |

```bash
make test
make verify-chain
make loadtest-smoke   # short k6 run; make loadtest is the 5 minute ramp
```

Fault injection against compose: `docker compose restart db` during `make loadtest`. Retries cover the blip. Whatever still fails lands in `dead_letters`. `POST /dead-letters/{id}/replay` clears it. Afterwards `GET /reconciliation` should return `ok: true` and `make verify-chain` should print `ok`.

## Benchmarks

Run with k6 (`make loadtest`). The full ramp posts unique keys from 50 to 500 requests/s over five minutes. `SMOKE=1` (via `make loadtest-smoke`) is a 20 second slice of the same scripts. Fill the table from a run on the machine you care about. A global chain lock caps throughput at the rate of one signed post at a time, which is the trade-off in [ADR 0003](docs/adr/0003-hash-chain.md).

| Scenario | Rate | p50 | p95 | p99 | Errors |
|---|---|---|---|---|---|
| Steady posting | _not yet measured_ | | | | |
| Idempotency storm (same key) | _not yet measured_ | | | | |
| Duplicate settlement feed | _not yet measured_ | | | | |

Record the CPU, RAM, OS, Postgres version and config, and whether the API ran in Docker or on the host. After the run, `GET /reconciliation` must return `ok: true`, `make verify-chain` must report no break, and `count(transactions)` must equal the number of unique keys that returned 201.

## What I'd add for production

- Per-account hash chains, or batched signing, to remove the global posting lock.
- A transactional outbox for publishing ledger events.
- HMAC key rotation with a key id stored on each entry.
- OpenTelemetry tracing and RED metrics.
- Partitioning `ledger_entries` by time, and archival.

## License

MIT

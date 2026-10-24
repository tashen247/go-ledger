# Problem types

Error responses use `application/problem+json` (RFC 9457). The `type` URI is
`https://github.com/tashen247/go-ledger/problems/` plus one of the slugs below.

## idempotency-key-required

`POST /transactions` was sent without an `Idempotency-Key` header. Status 400.

## idempotency-key-reuse

The key was already stored for a different request body. The original
transaction is unchanged. Status 422.

## idempotency-in-progress

Another request with the same key is still inside its database transaction.
Retry the same body after that request finishes. Status 409.

## account-exists

`accounts.name` is unique. Status 409.

## account-not-found

No account has the id in the path. Status 404.

## unknown-account

A transaction leg names an account id that does not exist. Status 400.

## unbalanced-transaction

The signed entry amounts do not sum to zero. Status 400.

## mixed-currency

The accounts in one transaction do not share a currency. Status 400.

## too-few-entries

A transaction needs at least two entries. Status 400.

## zero-amount

An entry amount was zero. Status 400.

## amount-overflow

Adding the amounts, or applying them to a cached balance, would overflow int64. Status 400.

## invalid-request

The JSON body failed structural validation. Status 400.

## transaction-not-found

A settlement named a transaction that does not exist. Status 404.

## currency-mismatch

The settlement currency is not the transaction currency. Status 400.

## illegal-settlement-transition

The settlement is already `settled` or `failed` and the event asks for the other state. Status 409. The database trigger rejects the same change if it is attempted with SQL.

## settlement-event-mismatch

`external_id` was already stored with a different amount, currency, or transaction. Status 409.

## settlement-parked

Transient database errors exhausted the retry budget. The event is in `dead_letters` and the body includes `dead_letter_id`. Status 503.

## dead-letter-not-found

No dead letter has that id. Status 404.

## internal-error

An unexpected failure. The server log has the cause. Status 500.

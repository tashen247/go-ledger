# ADR 0001: Money is an int64 of minor units

## Status

Accepted

## Context

Balances that move through `float64` pick up binary rounding. A ledger that is
off by a fraction of a cent cannot be reconciled, and the error is hard to
explain after the fact.

## Decision

`money.Amount` is an `int64` count of minor units. A ZAR amount of R150.00 is
`15000`. Every account has one ISO-4217 alphabetic currency, stored as three
uppercase letters. JSON amounts are integers. Adding two amounts checks for
signed overflow and returns an error instead of wrapping. Floats are not
accepted at the HTTP boundary or inside the posting path.

## Consequences

- Reconciliation is exact integer comparison. There is no tolerance.
- Currencies with different exponents still fit, as long as the caller sends
  minor units. The service does not convert between currencies.
- Values outside int64 are rejected. That is the intended limit for this service.

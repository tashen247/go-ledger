CREATE TABLE accounts (
  id          text PRIMARY KEY,
  name        text NOT NULL UNIQUE,
  currency    char(3) NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
  type        text NOT NULL CHECK (type IN ('asset','liability','equity','revenue','expense')),
  balance     bigint NOT NULL DEFAULT 0,        -- cache, reconciled against ledger_entries
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE transactions (
  id          text PRIMARY KEY,
  reference   text NOT NULL,
  currency    char(3) NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
  seq             bigserial PRIMARY KEY,
  transaction_id  text NOT NULL REFERENCES transactions(id),
  account_id      text NOT NULL REFERENCES accounts(id),
  amount          bigint NOT NULL CHECK (amount <> 0),   -- minor units, signed
  currency        char(3) NOT NULL,
  prev_hash       bytea NOT NULL,
  hash            bytea NOT NULL UNIQUE,
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON ledger_entries (account_id);
CREATE INDEX ON ledger_entries (transaction_id);

-- Append-only enforcement
CREATE FUNCTION forbid_ledger_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'ledger_entries is append-only (% blocked)', TG_OP
    USING ERRCODE = 'insufficient_privilege';
END $$;

CREATE TRIGGER ledger_entries_no_update_delete
  BEFORE UPDATE OR DELETE ON ledger_entries
  FOR EACH ROW EXECUTE FUNCTION forbid_ledger_mutation();

CREATE TRIGGER ledger_entries_no_truncate
  BEFORE TRUNCATE ON ledger_entries
  FOR EACH STATEMENT EXECUTE FUNCTION forbid_ledger_mutation();

CREATE TABLE idempotency_keys (
  key             text PRIMARY KEY,                      -- the idempotency guarantee
  request_hash    bytea NOT NULL,
  status          text NOT NULL CHECK (status IN ('in_progress','completed')),
  response_code   int,
  response_body   jsonb,
  transaction_id  text REFERENCES transactions(id),
  created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TYPE settlement_state AS ENUM ('pending','settled','failed');

CREATE TABLE settlements (
  id              text PRIMARY KEY,
  external_id     text NOT NULL UNIQUE,                  -- dedup of feed events
  transaction_id  text NOT NULL REFERENCES transactions(id),
  amount          bigint NOT NULL CHECK (amount > 0),
  currency        char(3) NOT NULL,
  state           settlement_state NOT NULL DEFAULT 'pending',
  updated_at      timestamptz NOT NULL DEFAULT now(),
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- Only legal transitions: pending -> settled | failed
CREATE FUNCTION check_settlement_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.state = NEW.state THEN RETURN NEW; END IF;
  IF OLD.state = 'pending' AND NEW.state IN ('settled','failed') THEN
    NEW.updated_at := now(); RETURN NEW;
  END IF;
  RAISE EXCEPTION 'illegal settlement transition % -> %', OLD.state, NEW.state;
END $$;

CREATE TRIGGER settlements_state_machine
  BEFORE UPDATE ON settlements
  FOR EACH ROW EXECUTE FUNCTION check_settlement_transition();

CREATE TABLE dead_letters (
  id            text PRIMARY KEY,
  source        text NOT NULL,                 -- e.g. 'settlement_feed'
  payload       jsonb NOT NULL,
  last_error    text NOT NULL,
  attempts      int NOT NULL,
  status        text NOT NULL DEFAULT 'parked' CHECK (status IN ('parked','replayed')),
  replay_result jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  replayed_at   timestamptz
);

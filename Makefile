.PHONY: up down migrate test vet lint verify-chain loadtest loadtest-smoke

export DATABASE_URL ?= postgres://ledger:ledger@127.0.0.1:5432/ledger?sslmode=disable
export LEDGER_HMAC_KEY ?= dev-only-change-me-please-32b

up:
	docker compose up -d --build

down:
	docker compose down

migrate:
	go run ./cmd/migrate

test:
	go test ./...

vet:
	go vet ./...

lint:
	go run honnef.co/go/tools/cmd/staticcheck@v0.5.1 ./...

verify-chain:
	go run ./cmd/verify

loadtest:
	k6 run loadtest/transactions.js
	k6 run loadtest/idempotency_storm.js
	k6 run loadtest/settlements.js

loadtest-smoke:
	SMOKE=1 k6 run loadtest/transactions.js
	SMOKE=1 k6 run loadtest/idempotency_storm.js
	SMOKE=1 k6 run loadtest/settlements.js

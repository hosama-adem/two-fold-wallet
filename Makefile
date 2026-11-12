.PHONY: up down logs run build test test-concurrency vet lint tidy health ready migrate-up migrate-down migrate-new generate seed

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BACKEND := $(ROOT)/backend
DATABASE_URL ?= postgres://twofold:twofold@127.0.0.1:5434/twofold?sslmode=disable

ifneq (,$(wildcard $(ROOT)/.env))
include $(ROOT)/.env
export
endif

up: ## Start Postgres (twofold-db)
	docker compose up -d db
	@echo "waiting for postgres..."
	@until docker compose exec -T db pg_isready -U twofold -d twofold >/dev/null 2>&1; do sleep 1; done
	@echo "twofold-db is ready"

down: ## Stop containers (keeps twofold-pgdata volume)
	docker compose down

logs: ## Tail database logs
	docker compose logs -f db

tidy: ## Refresh Go module sums
	cd $(BACKEND) && go mod tidy

build: ## Compile the API
	cd $(BACKEND) && go build -o bin/api ./cmd/api

test: ## Run unit + integration tests
	cd $(BACKEND) && go test -p 1 ./...

test-concurrency: ## 1,000 parallel transfers, 20×, with the race detector
	cd $(BACKEND) && go test -race -count=20 -timeout 40m -p 1 ./internal/transfer/ -run '^TestConcurrency'

vet: ## Static checks
	cd $(BACKEND) && go vet ./...

lint: vet ## Lint gate (vet today; golangci-lint later)
	@echo "lint ok"

run: ## Run the API (requires make up + .env)
	@test -f $(ROOT)/.env || (echo "missing .env — copy .env.example to .env" && exit 1)
	cd $(BACKEND) && go run ./cmd/api

health: ## Probe liveness
	curl -sf http://localhost:8080/health | tee /dev/stderr

ready: ## Probe readiness (DB)
	curl -sf http://localhost:8080/ready | tee /dev/stderr

migrate-up: ## Apply goose migrations
	cd $(BACKEND) && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -command=up -dir=migrations

migrate-down: ## Roll back one goose migration
	cd $(BACKEND) && DATABASE_URL="$(DATABASE_URL)" go run ./cmd/migrate -command=down -dir=migrations

migrate-new: ## Create a new goose migration: make migrate-new name=add_foo
	@test -n "$(name)" || (echo "usage: make migrate-new name=add_foo" && exit 1)
	cd $(BACKEND) && go run ./cmd/migrate -command=new -dir=migrations -name=$(name)

generate: ## Regenerate sqlc code (requires sqlc CLI on PATH)
	cd $(BACKEND) && sqlc generate

seed: ## Dev-only: open the sample ETB wallet and fund it with a journal
	@test "$${APP_ENV}" != "production" || (echo "refusing to seed production" && exit 1)
	cd $(BACKEND) && go run ./cmd/seed

.PHONY: up down logs run build test vet lint tidy health ready

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
BACKEND := $(ROOT)/backend

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

test: ## Run unit tests
	cd $(BACKEND) && go test ./...

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

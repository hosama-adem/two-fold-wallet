#!/usr/bin/env bash
# Lightweight stand-in when GNU make is unavailable (common on Windows Git Bash).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BACKEND="$ROOT/backend"

export GOPATH="${GOPATH:-$HOME/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOCACHE="${GOCACHE:-$GOPATH/cache}"

if [[ -f "$ROOT/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "$ROOT/.env"
  set +a
fi

DATABASE_URL="${DATABASE_URL:-postgres://twofold:twofold@127.0.0.1:5434/twofold?sslmode=disable}"

cmd="${1:-}"
case "$cmd" in
  up)
    docker compose -f "$ROOT/docker-compose.yml" up -d db
    echo "waiting for postgres..."
    until docker compose -f "$ROOT/docker-compose.yml" exec -T db pg_isready -U twofold -d twofold >/dev/null 2>&1; do sleep 1; done
    echo "twofold-db is ready"
    ;;
  down)
    docker compose -f "$ROOT/docker-compose.yml" down
    ;;
  run)
    [[ -f "$ROOT/.env" ]] || { echo "missing .env — copy .env.example to .env"; exit 1; }
    cd "$BACKEND" && go run ./cmd/api
    ;;
  build)
    cd "$BACKEND" && go build -o bin/api ./cmd/api
    ;;
  test)
    cd "$BACKEND" && go test ./...
    ;;
  vet|lint)
    cd "$BACKEND" && go vet ./...
    echo "lint ok"
    ;;
  health)
    curl -sf http://localhost:8080/health
    echo
    ;;
  ready)
    curl -sf http://localhost:8080/ready
    echo
    ;;
  migrate-up)
    cd "$BACKEND" && DATABASE_URL="$DATABASE_URL" go run ./cmd/migrate -command=up -dir=migrations
    ;;
  migrate-down)
    cd "$BACKEND" && DATABASE_URL="$DATABASE_URL" go run ./cmd/migrate -command=down -dir=migrations
    ;;
  migrate-new)
    name="${2:-}"
    [[ -n "$name" ]] || { echo "usage: ./dev.sh migrate-new add_foo"; exit 1; }
    cd "$BACKEND" && go run ./cmd/migrate -command=new -dir=migrations -name="$name"
    ;;
  generate)
    cd "$BACKEND" && sqlc generate
    ;;
  *)
    echo "usage: ./dev.sh {up|down|run|build|test|vet|lint|health|ready|migrate-up|migrate-down|migrate-new|generate}"
    exit 1
    ;;
esac

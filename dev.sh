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
  *)
    echo "usage: ./dev.sh {up|down|run|build|test|vet|lint|health|ready}"
    exit 1
    ;;
esac

package outbox

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// MigrateRiver applies River's Postgres schema (job tables, leader election).
// Call from cmd/worker (and any test that uses InsertTx). Goose owns app
// tables; River owns its own version line via rivermigrate.
func MigrateRiver(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("outbox: MigrateRiver requires a pool")
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("outbox: river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("outbox: river migrate: %w", err)
	}
	return nil
}

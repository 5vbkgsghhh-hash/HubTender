package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Position-input writes used to be single pool.Exec calls without an actor.
// Keep actor assignment and the write in one transaction for PgBouncer safety.
func execWithPricingActor(ctx context.Context, pool *pgxpool.Pool, q string, args ...any) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := setAuditUser(ctx, tx, ""); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

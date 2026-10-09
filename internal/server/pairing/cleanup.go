package pairing

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartCleanup starts pairing cleanup work.
func StartCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	startCleanup(ctx, pool, logger, time.Minute)
}

func startCleanup(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := cleanupPass(ctx, pool); err != nil && ctx.Err() == nil {
					logger.Error("pairing cleanup failed", "error", err)
				}
			}
		}
	}()
}

func cleanupPass(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE pairing_requests SET status = 'expired'
		WHERE status IN ('waiting_for_approval', 'waiting_for_code') AND expires_at <= now()`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `WITH expired_machines AS (
			UPDATE machines SET status = 'expired'
			WHERE status = 'pending' AND expires_at <= now()
			RETURNING pairing_request_id
		)
		UPDATE pairing_requests AS pr
		SET status = 'failed', failure_reason = 'not_confirmed'
		FROM expired_machines AS em
		WHERE pr.id = em.pairing_request_id AND pr.status = 'finishing'`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

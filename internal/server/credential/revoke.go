package credential

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Revoke marks a machine's active credential revoked within the caller's
// transaction. It reports whether a credential changed.
func Revoke(ctx context.Context, tx pgx.Tx, machineID string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE credentials SET status = 'revoked', ended_at = now()
		WHERE machine_id = $1 AND status = 'active'`, machineID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() != 0, nil
}

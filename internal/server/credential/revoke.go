package credential

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const revokeSQLTemplate = `UPDATE credentials SET status = 'revoked', ended_at = now()
	WHERE %s = $1 AND status = 'active'`

// Revoke marks a machine's active credential revoked within the caller's
// transaction. It reports whether a credential changed.
func Revoke(ctx context.Context, tx pgx.Tx, machineID string) (bool, error) {
	return revoke(ctx, tx, "machine_id", machineID)
}

// RevokeCredential marks one active credential revoked within the caller's
// transaction. It reports whether the credential changed.
func RevokeCredential(ctx context.Context, tx pgx.Tx, credentialID string) (bool, error) {
	return revoke(ctx, tx, "credential_id", credentialID)
}

func revoke(ctx context.Context, tx pgx.Tx, column, id string) (bool, error) {
	tag, err := tx.Exec(ctx, revokeSQL(column), id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() != 0, nil
}

func revokeSQL(column string) string {
	return fmt.Sprintf(revokeSQLTemplate, column)
}

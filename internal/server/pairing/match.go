package pairing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type matchingQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const findMatchingMachineQuery = `SELECT m.machine_id::text
	FROM machines AS m
	JOIN LATERAL (
		SELECT c.created_at, c.credential_id
		FROM credentials AS c
		WHERE c.machine_id = m.machine_id AND c.status IN ('active', 'revoked')
		ORDER BY c.created_at DESC, c.credential_id DESC
		LIMIT 1
	) AS paired ON true
	WHERE m.identity_hash = $1
	ORDER BY paired.created_at DESC, paired.credential_id DESC
	LIMIT 1`

func findMatchingMachine(ctx context.Context, q matchingQuerier, identityHash []byte) (string, error) {
	var machineID string
	err := q.QueryRow(ctx, findMatchingMachineQuery, identityHash).Scan(&machineID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return machineID, err
}

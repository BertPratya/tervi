// Package credential reads and changes machine credentials.
package credential

import (
	"context"
	"errors"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/jackc/pgx/v5"
)

const (
	StatusPending = "pending"
	StatusActive  = "active"
	StatusFailed  = "failed"
	StatusExpired = "expired"
	StatusRevoked = "revoked"

	ErrorUnknownCredential = "unknown_credential"
	ErrorCredentialRevoked = "credential_revoked"
)

// HasActiveSQL returns the EXISTS predicate for a machine ID column that has
// an active credential.
func HasActiveSQL(machineIDColumn string) string {
	return "EXISTS (SELECT 1 FROM credentials WHERE machine_id = " + machineIDColumn + " AND status = 'active')"
}

// Row is a credential found by its hash.
type Row struct {
	CredentialID     string
	MachineID        string
	PairingRequestID string
	Status           string
}

// Querier can read a single row from PostgreSQL.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const findQuery = `SELECT credential_id::text, machine_id::text, pairing_request_id::text,
	CASE WHEN status = 'pending' AND expires_at <= now() THEN 'expired' ELSE status END
	FROM credentials WHERE credential_hash = $1`

// Find hashes proof and returns its credential row. A pending credential past
// its expiry is reported as expired. found is false when the hash is unknown.
func Find(ctx context.Context, q Querier, proof secret.Value) (row Row, found bool, err error) {
	err = q.QueryRow(ctx, findQuery, secret.Hash(proof)).Scan(
		&row.CredentialID, &row.MachineID, &row.PairingRequestID, &row.Status,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	return row, true, nil
}

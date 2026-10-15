package dbtest

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MachineCredential holds the values used to create a machine and its
// credential in database-backed tests.
type MachineCredential struct {
	MachineID   string
	DisplayName string
	Hostname    string
	OSName      string
	OSVersion   string
	Hash        []byte
	Status      string
}

const insertCredentialFixtureQuery = `INSERT INTO credentials
	(machine_id, pairing_request_id, credential_hash, status, expires_at, ended_at)
	VALUES ($1, $2, $3, $4, now() + interval '10 minutes',
		CASE WHEN $4 = 'revoked' THEN now() ELSE NULL END)`

// InsertMachineCredential creates a pairing request, machine, and credential
// for a database-backed test and returns the machine and request IDs.
func InsertMachineCredential(t testing.TB, pool *pgxpool.Pool, fixture MachineCredential) (machineID, pairingRequestID string) {
	t.Helper()
	if fixture.DisplayName == "" {
		fixture.DisplayName = "test machine"
	}
	if fixture.Status == "" {
		fixture.Status = "active"
	}
	key := fmt.Sprintf("%x", fixture.Hash)
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('paired', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("dbtest-polling-"+key), []byte("dbtest-approval-"+key)).Scan(&pairingRequestID); err != nil {
		t.Fatalf("insert test pairing request: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO machines
		(machine_id, hostname, os_name, os_version, display_name)
		VALUES (COALESCE(NULLIF($1, '')::uuid, gen_random_uuid()), $2, $3, $4, $5)
		RETURNING machine_id::text`, fixture.MachineID, fixture.Hostname, fixture.OSName, fixture.OSVersion, fixture.DisplayName).
		Scan(&machineID); err != nil {
		t.Fatalf("insert test machine: %v", err)
	}
	if _, err := pool.Exec(ctx, insertCredentialFixtureQuery,
		machineID, pairingRequestID, fixture.Hash, fixture.Status); err != nil {
		t.Fatalf("insert test credential: %v", err)
	}
	return machineID, pairingRequestID
}

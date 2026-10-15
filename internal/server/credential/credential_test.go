package credential_test

import (
	"context"
	"testing"
	"time"

	"github.com/bertpratya/tervi/internal/secret"
	"github.com/bertpratya/tervi/internal/server/credential"
	"github.com/bertpratya/tervi/internal/server/db/dbtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFindCredential(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	proofs := map[string]string{
		"active":  "credential-find-active",
		"revoked": "credential-find-revoked",
		"failed":  "credential-find-failed",
		"expired": "credential-find-expired",
		"pending": "credential-find-pending",
	}
	rows := make(map[string]credential.Row)
	for status, proof := range proofs {
		requestID, machineID := insertCredentialMachine(t, pool, proof, status)
		var row credential.Row
		if err := pool.QueryRow(ctx, `SELECT credential_id::text, machine_id::text,
			pairing_request_id::text, status FROM credentials WHERE machine_id = $1`, machineID).
			Scan(&row.CredentialID, &row.MachineID, &row.PairingRequestID, &row.Status); err != nil {
			t.Fatalf("read inserted credential: %v", err)
		}
		if row.PairingRequestID != requestID {
			t.Fatalf("credential request ID = %q, want %q", row.PairingRequestID, requestID)
		}
		rows[status] = row
	}
	if _, err := pool.Exec(ctx, `UPDATE credentials SET expires_at = now() - interval '1 second'
		WHERE credential_id = $1`, rows["pending"].CredentialID); err != nil {
		t.Fatalf("expire pending credential: %v", err)
	}

	for _, status := range []string{"active", "revoked", "failed", "expired", "pending"} {
		t.Run(status, func(t *testing.T) {
			row, found, err := credential.Find(ctx, pool, secret.FromString(proofs[status]))
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if !found {
				t.Fatal("Find() did not find credential")
			}
			wantStatus := status
			if status == "pending" {
				wantStatus = "expired"
			}
			want := rows[status]
			want.Status = wantStatus
			if row != want {
				t.Fatalf("Find() = %+v, want %+v", row, want)
			}
		})
	}
	if _, found, err := credential.Find(ctx, pool, secret.FromString("credential-find-unknown")); err != nil || found {
		t.Fatalf("Find(unknown) = found %t, error %v; want false, nil", found, err)
	}
}

func TestRevokeCredential(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	_, activeMachineID := insertCredentialMachine(t, pool, "credential-revoke-active", "active")
	_, inactiveMachineID := insertCredentialMachine(t, pool, "credential-revoke-inactive", "failed")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin revoke transaction: %v", err)
	}
	changed, err := credential.Revoke(ctx, tx, activeMachineID)
	if err != nil || !changed {
		t.Fatalf("Revoke(active) = (%t, %v), want (true, nil)", changed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit revoke transaction: %v", err)
	}
	var status string
	var endedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, ended_at FROM credentials WHERE machine_id = $1`, activeMachineID).
		Scan(&status, &endedAt); err != nil {
		t.Fatalf("read revoked credential: %v", err)
	}
	if status != credential.StatusRevoked || endedAt == nil {
		t.Fatalf("revoked credential = (%q, %v), want revoked with ended_at", status, endedAt)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin no-op revoke transaction: %v", err)
	}
	changed, err = credential.Revoke(ctx, tx, inactiveMachineID)
	if err != nil || changed {
		t.Fatalf("Revoke(inactive) = (%t, %v), want (false, nil)", changed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit no-op revoke transaction: %v", err)
	}
}

func TestRevokeCredentialDoesNotRevokeReplacement(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	_, machineID := insertCredentialMachine(t, pool, "credential-revoke-stale-proof", credential.StatusActive)
	var oldCredentialID string
	if err := pool.QueryRow(ctx, `SELECT credential_id::text FROM credentials WHERE machine_id = $1`, machineID).
		Scan(&oldCredentialID); err != nil {
		t.Fatalf("read old credential ID: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin replacement credential setup: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE credentials SET status = 'revoked', ended_at = now() WHERE credential_id = $1`, oldCredentialID); err != nil {
		t.Fatalf("revoke old credential: %v", err)
	}
	var requestID string
	if err := tx.QueryRow(ctx, `INSERT INTO pairing_requests
		(status, polling_key_hash, approval_key_hash, expires_at)
		VALUES ('paired', $1, $2, now() + interval '10 minutes') RETURNING id::text`,
		[]byte("replacement-polling"), []byte("replacement-approval")).Scan(&requestID); err != nil {
		t.Fatalf("insert replacement request: %v", err)
	}
	var newCredentialID string
	if err := tx.QueryRow(ctx, `INSERT INTO credentials
		(machine_id, pairing_request_id, credential_hash, status, expires_at)
		VALUES ($1, $2, $3, 'active', now() + interval '10 minutes') RETURNING credential_id::text`,
		machineID, requestID, secret.Hash(secret.FromString("credential-replacement-proof"))).Scan(&newCredentialID); err != nil {
		t.Fatalf("insert replacement credential: %v", err)
	}

	changed, err := credential.RevokeCredential(ctx, tx, oldCredentialID)
	if err != nil || changed {
		t.Fatalf("RevokeCredential(old) = (%t, %v), want (false, nil)", changed, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit replacement credential setup: %v", err)
	}
	var newStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM credentials WHERE credential_id = $1`, newCredentialID).Scan(&newStatus); err != nil {
		t.Fatalf("read replacement status: %v", err)
	}
	if newStatus != credential.StatusActive {
		t.Fatalf("replacement credential status = %q, want active", newStatus)
	}
}

func TestHasActiveSQL(t *testing.T) {
	got := credential.HasActiveSQL("m.machine_id")
	want := "EXISTS (SELECT 1 FROM credentials WHERE machine_id = m.machine_id AND status = 'active')"
	if got != want {
		t.Fatalf("HasActiveSQL() = %q, want %q", got, want)
	}
}

func insertCredentialMachine(t *testing.T, pool *pgxpool.Pool, proof, status string) (string, string) {
	t.Helper()
	machineID, requestID := dbtest.InsertMachineCredential(t, pool, dbtest.MachineCredential{
		DisplayName: "credential test machine",
		Hash:        secret.Hash(secret.FromString(proof)),
		Status:      status,
	})
	return requestID, machineID
}

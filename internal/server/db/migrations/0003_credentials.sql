-- +goose Up
CREATE TABLE credentials (
    credential_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    machine_id uuid NOT NULL REFERENCES machines(machine_id),
    pairing_request_id uuid NOT NULL UNIQUE REFERENCES pairing_requests(id),
    credential_hash bytea NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('pending', 'active', 'failed', 'expired', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    CHECK ((status = 'revoked') = (ended_at IS NOT NULL))
);
CREATE UNIQUE INDEX credentials_one_active_per_machine
    ON credentials (machine_id) WHERE status = 'active';
CREATE INDEX credentials_machine ON credentials (machine_id);

INSERT INTO credentials (machine_id, pairing_request_id, credential_hash, status, created_at, expires_at)
    SELECT machine_id, pairing_request_id, credential_hash, status, created_at, expires_at FROM machines;

ALTER TABLE machines
    DROP COLUMN pairing_request_id,
    DROP COLUMN credential_hash,
    DROP COLUMN status,
    DROP COLUMN expires_at,
    ADD COLUMN identity_hash bytea CHECK (identity_hash IS NULL OR length(identity_hash) = 32);
CREATE INDEX machines_identity_hash ON machines (identity_hash);

ALTER TABLE pairing_requests
    ADD COLUMN identity_hash bytea CHECK (identity_hash IS NULL OR length(identity_hash) = 32),
    ADD COLUMN matched_machine_id uuid REFERENCES machines(machine_id);

-- +goose Down
ALTER TABLE pairing_requests
    DROP COLUMN matched_machine_id,
    DROP COLUMN identity_hash;
DROP INDEX machines_identity_hash;
ALTER TABLE machines DROP COLUMN identity_hash;

ALTER TABLE machines
    ADD COLUMN pairing_request_id uuid,
    ADD COLUMN credential_hash bytea,
    ADD COLUMN status text,
    ADD COLUMN expires_at timestamptz;

WITH newest_credentials AS (
    SELECT DISTINCT ON (machine_id)
        machine_id,
        pairing_request_id,
        credential_hash,
        CASE WHEN status = 'revoked' THEN 'expired' ELSE status END AS status,
        expires_at
    FROM credentials
    ORDER BY machine_id, created_at DESC, credential_id DESC
)
UPDATE machines AS m
SET pairing_request_id = c.pairing_request_id,
    credential_hash = c.credential_hash,
    status = c.status,
    expires_at = c.expires_at
FROM newest_credentials AS c
WHERE c.machine_id = m.machine_id;

DELETE FROM machines WHERE pairing_request_id IS NULL;

ALTER TABLE machines
    ALTER COLUMN pairing_request_id SET NOT NULL,
    ALTER COLUMN credential_hash SET NOT NULL,
    ALTER COLUMN status SET NOT NULL,
    ALTER COLUMN expires_at SET NOT NULL,
    ADD CONSTRAINT machines_pairing_request_id_key UNIQUE (pairing_request_id),
    ADD CONSTRAINT machines_pairing_request_id_fkey FOREIGN KEY (pairing_request_id) REFERENCES pairing_requests(id),
    ADD CONSTRAINT machines_credential_hash_key UNIQUE (credential_hash),
    ADD CONSTRAINT machines_status_check CHECK (status IN ('pending', 'active', 'failed', 'expired'));

DROP TABLE credentials;

-- +goose Up
CREATE TABLE pairing_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    hostname text NOT NULL DEFAULT '',
    os_name text NOT NULL DEFAULT '',
    os_version text NOT NULL DEFAULT '',
    status text NOT NULL CHECK (status IN (
        'waiting_for_approval', 'waiting_for_code', 'finishing', 'paired',
        'rejected', 'failed', 'expired'
    )),
    polling_key_hash bytea NOT NULL UNIQUE,
    approval_key_hash bytea NOT NULL UNIQUE,
    pairing_code text,
    tries_left integer NOT NULL DEFAULT 5,
    failure_reason text CHECK (failure_reason IS NULL OR failure_reason IN (
        'wrong_codes', 'not_saved', 'not_confirmed'
    )),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE TABLE machines (
    machine_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pairing_request_id uuid NOT NULL UNIQUE REFERENCES pairing_requests(id),
    hostname text NOT NULL DEFAULT '',
    os_name text NOT NULL DEFAULT '',
    os_version text NOT NULL DEFAULT '',
    display_name text NOT NULL,
    credential_hash bytea NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('pending', 'active', 'failed', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE machines;
DROP TABLE pairing_requests;

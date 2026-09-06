-- +goose Up
CREATE TABLE devices (
    id uuid PRIMARY KEY,
    role smallint NOT NULL CHECK (role IN (1, 2)),
    display_name text NOT NULL DEFAULT '',
    ed25519_public_key bytea NOT NULL CHECK (octet_length(ed25519_public_key) = 32),
    x25519_public_key bytea NOT NULL CHECK (octet_length(x25519_public_key) = 32),
    capability_hash bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'revoked')),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);

CREATE TABLE enrollment_tokens (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    remaining_uses integer NOT NULL CHECK (remaining_uses >= 0),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pairing_sessions (
    id uuid PRIMARY KEY,
    daemon_device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 5),
    status text NOT NULL CHECK (status IN ('pending', 'approved', 'cancelled', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pairings (
    id uuid PRIMARY KEY,
    daemon_device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    mobile_device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    scopes text[] NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'revoked')),
    grant_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    UNIQUE (daemon_device_id, mobile_device_id)
);

CREATE INDEX pairings_mobile_active_idx ON pairings (mobile_device_id) WHERE status = 'active';
CREATE INDEX pairings_daemon_active_idx ON pairings (daemon_device_id) WHERE status = 'active';

CREATE TABLE push_endpoints (
    id uuid PRIMARY KEY,
    mobile_device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    platform text NOT NULL CHECK (platform IN ('apns', 'fcm')),
    app_id text NOT NULL,
    environment text NOT NULL,
    locale text NOT NULL DEFAULT '',
    token_ciphertext bytea NOT NULL,
    key_version integer NOT NULL DEFAULT 1,
    generation bigint NOT NULL,
    status text NOT NULL CHECK (status IN ('active', 'invalid', 'deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (mobile_device_id, platform, app_id, environment)
);

CREATE TABLE audit_events (
    id bigserial PRIMARY KEY,
    event_type text NOT NULL,
    actor_device_id uuid,
    target_device_id uuid,
    result_code text NOT NULL,
    network_hash bytea,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE audit_events;
DROP TABLE push_endpoints;
DROP TABLE pairings;
DROP TABLE pairing_sessions;
DROP TABLE enrollment_tokens;
DROP TABLE devices;

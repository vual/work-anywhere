-- +goose Up
LOCK TABLE devices IN SHARE ROW EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
DECLARE conflicting_ids text;
BEGIN
    SELECT string_agg(id::text, ', ' ORDER BY id::text) INTO conflicting_ids
    FROM devices
    WHERE role = 1 AND ed25519_public_key IN (
        SELECT ed25519_public_key FROM devices WHERE role = 1
        GROUP BY ed25519_public_key HAVING count(*) > 1
    );
    IF conflicting_ids IS NOT NULL THEN
        RAISE EXCEPTION 'Duplicate daemon identities require manual resolution: %', conflicting_ids;
    END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX devices_daemon_ed25519_unique ON devices (ed25519_public_key) WHERE role = 1;

-- +goose Down
DROP INDEX devices_daemon_ed25519_unique;

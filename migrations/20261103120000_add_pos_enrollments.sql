-- +goose Up
-- One-time codes an admin creates in the dashboard to enroll a handheld at runtime, so the same
-- Play build serves every restaurant. The handheld generates its own secret and sends it with the
-- code to POST /pos/devices/enroll, which creates (or re-keys) its pos_devices row. Only the
-- SHA-256 of the code is kept; a code is valid for a few minutes and only once.
CREATE TABLE pos_enrollments (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash   TEXT        NOT NULL UNIQUE,
    label       TEXT        NOT NULL,
    created_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    device_id   UUID        REFERENCES pos_devices(id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE IF EXISTS pos_enrollments;

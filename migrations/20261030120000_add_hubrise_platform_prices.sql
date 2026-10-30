-- +goose Up
-- HubRise integration: the price lists of the delivery platforms. Each entry becomes a HubRise variant
-- carrying the published price plus a markup; a platform's bridge reads its variant. Additive only, the
-- previous release keeps working on this schema.

-- ALTER TABLE takes ACCESS EXCLUSIVE: lock_timeout makes this migration fail fast behind a long
-- transaction instead of queueing every reader of the table behind itself.
SET LOCAL lock_timeout = '5s';

ALTER TABLE hubrise_connection
    ADD COLUMN platform_prices JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE hubrise_connection
    DROP COLUMN platform_prices;

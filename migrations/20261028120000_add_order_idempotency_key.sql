-- +goose Up
-- A client-generated key per checkout attempt. When the connection drops after the order was
-- saved but before the reply arrived, the client retries with the same key and gets the order it
-- already placed instead of a second one. Unique per customer; NULL for clients that send none.
ALTER TABLE orders
    ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX orders_user_idempotency_key
    ON orders (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS orders_user_idempotency_key;

ALTER TABLE orders
    DROP COLUMN idempotency_key;

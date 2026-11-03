-- +goose Up
-- CONFIRMED and PREPARING are merged: confirming a PENDING order now starts its preparation.
UPDATE orders
SET order_status = 'PREPARING'
WHERE order_status = 'CONFIRMED';

-- The history keeps the confirmation moment (it is when preparation started now) and drops the
-- PREPARING row that followed it, so a timeline does not show the same step twice.
UPDATE order_status_history
SET status = 'PREPARING'
WHERE status = 'CONFIRMED';

DELETE FROM order_status_history
WHERE id IN (
    SELECT id
    FROM (
        SELECT id, status,
               lag(status) OVER (PARTITION BY order_id ORDER BY changed_at, id) AS previous_status
        FROM order_status_history
    ) h
    WHERE h.status = 'PREPARING' AND h.previous_status = 'PREPARING'
);

ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (
    order_status = ANY (
        ARRAY[
            'PENDING', 'PREPARING', 'AWAITING_PICK_UP',
            'PICKED_UP', 'OUT_FOR_DELIVERY', 'DELIVERED', 'CANCELLED', 'FAILED'
        ]
    )
);

-- +goose Down
-- Only the constraint comes back: merged orders stay PREPARING.
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (
    order_status = ANY (
        ARRAY[
            'PENDING', 'CONFIRMED', 'PREPARING', 'AWAITING_PICK_UP',
            'PICKED_UP', 'OUT_FOR_DELIVERY', 'DELIVERED', 'CANCELLED', 'FAILED'
        ]
    )
);

-- +goose Up
-- The handheld moves from FCM registration tokens to Firebase Installation IDs (FCM direct
-- send, firebase-messaging 25.1+). A device holds one or the other: storing one clears the
-- other, so it is never pushed twice.
ALTER TABLE pos_devices
    ADD COLUMN fcm_fid TEXT,
    ADD COLUMN fcm_fid_updated_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE pos_devices
    DROP COLUMN fcm_fid,
    DROP COLUMN fcm_fid_updated_at;

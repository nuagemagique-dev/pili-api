-- +goose Up
-- When the product image was last stored. A new upload replaces the file under the same key, so
-- clients append this to the image URL to get past browser and CDN caches.
ALTER TABLE products ADD COLUMN image_updated_at TIMESTAMPTZ;

-- Give every existing product a version once: the images stored so far have no Cache-Control,
-- so browsers keep them heuristically, and a new URL is the only way to get past that.
UPDATE products SET image_updated_at = now();

-- +goose Down
ALTER TABLE products DROP COLUMN image_updated_at;

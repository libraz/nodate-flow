-- name: InsertStorageLocationIfAbsent :exec
-- Record the bucket this database's blobs live in, unless a drainer has
-- already recorded one. The unique key on setting_key makes the first
-- writer win; a later writer's value is not applied.
INSERT INTO instance_settings (public_id, setting_key, setting_value)
VALUES (sqlc.arg(public_id), 'storage.location', sqlc.arg(location))
ON DUPLICATE KEY UPDATE setting_key = setting_key;

-- name: GetStorageLocation :one
-- The bucket recorded by the first drainer to start against this
-- database, read regardless of enabled so a disabled row still blocks a
-- drainer pointed elsewhere.
SELECT setting_value
FROM instance_settings
WHERE setting_key = 'storage.location'
LIMIT 1;

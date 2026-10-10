-- name: NodeLeaseClaim :execrows
INSERT INTO snowflake_node_leases (node_id, holder, acquired_at, expires_at)
VALUES (sqlc.arg('node_id'), sqlc.arg('holder'), now(), now() + make_interval(secs => sqlc.arg('ttl_seconds')::int))
ON CONFLICT (node_id) DO UPDATE
SET holder = EXCLUDED.holder, acquired_at = now(), expires_at = EXCLUDED.expires_at
WHERE snowflake_node_leases.expires_at < now();

-- name: NodeLeaseRenew :execrows
UPDATE snowflake_node_leases
SET expires_at = now() + make_interval(secs => sqlc.arg('ttl_seconds')::int)
WHERE node_id = sqlc.arg('node_id') AND holder = sqlc.arg('holder');

-- name: NodeLeaseRelease :exec
DELETE FROM snowflake_node_leases
WHERE node_id = sqlc.arg('node_id') AND holder = sqlc.arg('holder');

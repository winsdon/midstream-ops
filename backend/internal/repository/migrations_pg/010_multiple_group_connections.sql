-- One group can provision separate protocol accounts or backup accounts.
-- Idempotency remains enforced by idx_upstream_conn_operation.
DROP INDEX IF EXISTS idx_upstream_conn_active;
CREATE INDEX idx_upstream_conn_active
  ON upstream_connections(provider_id, upstream_group) WHERE status = 'active';

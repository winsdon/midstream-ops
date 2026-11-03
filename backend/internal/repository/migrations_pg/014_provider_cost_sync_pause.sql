-- Allow pausing automatic cost queries without disabling balance collection.
ALTER TABLE providers
  ADD COLUMN IF NOT EXISTS cost_sync_paused BOOLEAN NOT NULL DEFAULT FALSE;

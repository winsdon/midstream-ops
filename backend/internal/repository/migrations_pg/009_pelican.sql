-- Independent publication history; never trimmed by model IQ retention.
CREATE TABLE pelican_batches (
  id BIGSERIAL PRIMARY KEY,
  request_id TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  request JSONB NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE pelican_results (
  id BIGSERIAL PRIMARY KEY,
  batch_id BIGINT NOT NULL REFERENCES pelican_batches(id),
  group_id BIGINT NOT NULL,
  model TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  published_at TIMESTAMPTZ,
  tested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL,
  output JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX pelican_results_history ON pelican_results(tested_at DESC, id DESC);
CREATE INDEX pelican_results_latest ON pelican_results(group_id, model, tested_at DESC) WHERE published_at IS NOT NULL;
CREATE INDEX pelican_results_batch ON pelican_results(batch_id);

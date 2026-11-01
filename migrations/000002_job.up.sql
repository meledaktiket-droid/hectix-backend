CREATE TABLE job (
  id           bigserial PRIMARY KEY,
  kind         text        NOT NULL,
  payload      jsonb       NOT NULL,
  status       text        NOT NULL DEFAULT 'pending',
  attempts     int         NOT NULL DEFAULT 0,
  max_attempts int         NOT NULL DEFAULT 8,
  run_at       timestamp   NOT NULL DEFAULT now(),
  last_error   text,
  created_at   timestamp   NOT NULL DEFAULT now(),
  updated_at   timestamp   NOT NULL DEFAULT now()
);
CREATE INDEX job_ready_idx ON job (run_at) WHERE status = 'pending';

DROP INDEX voucher_event_code_idx;
CREATE UNIQUE INDEX voucher_event_code_idx ON voucher (event_id, code) WHERE deleted_at IS NULL;

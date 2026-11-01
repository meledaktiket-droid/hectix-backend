DROP INDEX voucher_event_code_idx;
CREATE UNIQUE INDEX voucher_event_code_idx ON voucher (event_id, code);

DROP TABLE job;

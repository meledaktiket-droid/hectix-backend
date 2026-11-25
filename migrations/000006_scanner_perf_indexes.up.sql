CREATE INDEX IF NOT EXISTS scan_ledger_target_action_idx ON public.scan_ledger (target_id, action, created_at DESC);
CREATE INDEX IF NOT EXISTS physical_ticket_event_created_idx ON public.physical_ticket (event_id, created_at DESC, id ASC);
CREATE INDEX IF NOT EXISTS physical_ticket_order_idx ON public.physical_ticket (order_id) WHERE order_id IS NOT NULL;

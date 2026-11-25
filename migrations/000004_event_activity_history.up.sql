CREATE TABLE IF NOT EXISTS public.event_activity_history (
    id          text PRIMARY KEY,
    event_id    text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    category    text NOT NULL,
    action      text NOT NULL,
    target_id   text,
    from_status text,
    to_status   text,
    note        text,
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    actor_id    text REFERENCES public."user"(id) ON DELETE SET NULL,
    actor_type  text NOT NULL DEFAULT 'admin',
    created_at  timestamp without time zone NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS event_activity_history_event_idx ON public.event_activity_history (event_id, category, created_at DESC);

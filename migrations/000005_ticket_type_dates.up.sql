CREATE TABLE public.ticket_type_dates (
    id              text PRIMARY KEY,
    ticket_type_id  text NOT NULL REFERENCES public.ticket_type(id) ON DELETE CASCADE,
    event_id        text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    valid_date      date NOT NULL,
    session_id      text REFERENCES public.event_session(id) ON DELETE SET NULL,
    created_at      timestamp without time zone NOT NULL DEFAULT now(),
    UNIQUE (ticket_type_id, valid_date)
);
CREATE INDEX ticket_type_dates_event_idx ON public.ticket_type_dates (event_id, valid_date);
CREATE INDEX ticket_type_dates_ticket_idx ON public.ticket_type_dates (ticket_type_id);

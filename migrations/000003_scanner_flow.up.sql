CREATE TABLE public.order_item_redemption (
    order_item_id   text PRIMARY KEY REFERENCES public.order_item(id) ON DELETE CASCADE,
    order_id        text NOT NULL REFERENCES public."order"(id) ON DELETE CASCADE,
    redeemed_qty    integer NOT NULL DEFAULT 0 CHECK (redeemed_qty >= 0),
    voided_qty      integer NOT NULL DEFAULT 0 CHECK (voided_qty >= 0),
    updated_at      timestamp without time zone NOT NULL DEFAULT now()
);
CREATE INDEX order_item_redemption_order_idx ON public.order_item_redemption (order_id);

CREATE TABLE public.ticket_type_access_map (
    ticket_type_id  text PRIMARY KEY REFERENCES public.ticket_type(id) ON DELETE CASCADE,
    event_id        text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    access_type     text NOT NULL,
    session_ids     jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at      timestamp without time zone NOT NULL DEFAULT now()
);
CREATE INDEX ticket_type_access_map_event_idx ON public.ticket_type_access_map (event_id);

CREATE TABLE public.event_session (
    id              text PRIMARY KEY,
    event_id        text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    name            text NOT NULL,
    starts_at       timestamp without time zone NOT NULL,
    ends_at         timestamp without time zone NOT NULL,
    status          text NOT NULL DEFAULT 'open',
    quota_limit     integer NOT NULL DEFAULT 0,
    allow_reentry   boolean NOT NULL DEFAULT false,
    unique_entered  integer NOT NULL DEFAULT 0,
    current_inside  integer NOT NULL DEFAULT 0,
    created_at      timestamp without time zone NOT NULL DEFAULT now(),
    CONSTRAINT event_session_occupancy_nonneg CHECK (unique_entered >= 0 AND current_inside >= 0)
);
CREATE INDEX event_session_event_idx ON public.event_session (event_id, starts_at);

CREATE TABLE public.physical_batch (
    id              text PRIMARY KEY,
    event_id        text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    request_id      text NOT NULL UNIQUE,
    payload_hash    text NOT NULL,
    name            text NOT NULL,
    access_type     text NOT NULL,
    ticket_type_id  text REFERENCES public.ticket_type(id) ON DELETE SET NULL,
    session_ids     jsonb NOT NULL DEFAULT '[]'::jsonb,
    purpose         text NOT NULL DEFAULT 'ALLOCATION',
    activation_mode text NOT NULL DEFAULT 'ON_HANDOVER',
    quantity        integer NOT NULL CHECK (quantity > 0 AND quantity <= 10000),
    merchant_ref    text,
    generated_by    text NOT NULL REFERENCES public."user"(id),
    generated_at    timestamp without time zone NOT NULL DEFAULT now(),
    exported_by     text REFERENCES public."user"(id),
    exported_at     timestamp without time zone,
    export_count    integer NOT NULL DEFAULT 0
);
CREATE INDEX physical_batch_event_idx ON public.physical_batch (event_id, generated_at DESC);

CREATE TABLE public.physical_ticket (
    id                  text PRIMARY KEY,
    batch_id            text NOT NULL REFERENCES public.physical_batch(id) ON DELETE CASCADE,
    event_id            text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    serial_number       text NOT NULL,
    code                text NOT NULL UNIQUE,
    access_type         text NOT NULL,
    session_ids         jsonb NOT NULL DEFAULT '[]'::jsonb,
    validity_status     text NOT NULL DEFAULT 'STOCK',
    assignment_status   text NOT NULL DEFAULT 'UNASSIGNED',
    order_id            text REFERENCES public."order"(id) ON DELETE SET NULL,
    order_item_id       text REFERENCES public.order_item(id) ON DELETE SET NULL,
    ots_reference       text,
    replaced_by_id      text REFERENCES public.physical_ticket(id) ON DELETE SET NULL,
    activated_by        text REFERENCES public."user"(id),
    activated_at        timestamp without time zone,
    created_at          timestamp without time zone NOT NULL DEFAULT now(),
    UNIQUE (batch_id, serial_number)
);
CREATE INDEX physical_ticket_event_idx ON public.physical_ticket (event_id, validity_status, assignment_status);

CREATE TABLE public.ticket_session_usage (
    physical_ticket_id  text NOT NULL REFERENCES public.physical_ticket(id) ON DELETE CASCADE,
    session_id          text NOT NULL REFERENCES public.event_session(id) ON DELETE CASCADE,
    event_id            text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    state               text NOT NULL DEFAULT 'NOT_ENTERED',
    entry_count         integer NOT NULL DEFAULT 0,
    first_in_at         timestamp without time zone,
    last_in_at          timestamp without time zone,
    last_out_at         timestamp without time zone,
    last_actor_id       text REFERENCES public."user"(id),
    last_gate_label     text,
    PRIMARY KEY (physical_ticket_id, session_id)
);
CREATE INDEX ticket_session_usage_event_idx ON public.ticket_session_usage (event_id, session_id, state);

CREATE TABLE public.event_crew (
    id              text PRIMARY KEY,
    event_id        text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    email           text NOT NULL,
    user_id         text REFERENCES public."user"(id) ON DELETE CASCADE,
    role            text NOT NULL DEFAULT 'verification_crew',
    token           text NOT NULL UNIQUE,
    status          text NOT NULL DEFAULT 'pending',
    invited_by      text NOT NULL REFERENCES public."user"(id),
    expires_at      timestamp without time zone NOT NULL,
    accepted_at     timestamp without time zone,
    revoked_at      timestamp without time zone,
    created_at      timestamp without time zone NOT NULL DEFAULT now(),
    UNIQUE (event_id, email)
);
CREATE INDEX event_crew_user_idx ON public.event_crew (user_id, status);

CREATE TABLE public.scan_ledger (
    id                  bigserial PRIMARY KEY,
    request_id          text NOT NULL UNIQUE,
    payload_hash        text NOT NULL,
    event_id            text NOT NULL REFERENCES public.event(id) ON DELETE CASCADE,
    session_id          text REFERENCES public.event_session(id) ON DELETE SET NULL,
    actor_id            text NOT NULL REFERENCES public."user"(id),
    action              text NOT NULL,
    target_id           text,
    result_code         text NOT NULL,
    reason              text,
    state_before        jsonb,
    state_after         jsonb,
    response_payload    jsonb NOT NULL,
    created_at          timestamp without time zone NOT NULL DEFAULT now()
);
CREATE INDEX scan_ledger_event_idx ON public.scan_ledger (event_id, created_at DESC);

CREATE TABLE public.account (
    id text NOT NULL,
    account_id text NOT NULL,
    provider_id text NOT NULL,
    user_id text NOT NULL,
    access_token text,
    refresh_token text,
    id_token text,
    access_token_expires_at timestamp without time zone,
    refresh_token_expires_at timestamp without time zone,
    scope text,
    password text,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);

CREATE TABLE public.admin_invitation (
    id text NOT NULL,
    email text NOT NULL,
    role text NOT NULL,
    token text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    invited_by text NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    accepted_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public.banner (
    id text NOT NULL,
    tag text NOT NULL,
    description text NOT NULL,
    image_url text NOT NULL,
    display_order integer DEFAULT 0 NOT NULL,
    is_active boolean DEFAULT true NOT NULL,
    deleted_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);

CREATE TABLE public.event (
    id text NOT NULL,
    organization_id text NOT NULL,
    name text NOT NULL,
    description text,
    venue_name text,
    address text,
    city text,
    banner_url text,
    starts_at timestamp without time zone NOT NULL,
    ends_at timestamp without time zone,
    status text DEFAULT 'draft'::text NOT NULL,
    visibility text DEFAULT 'public'::text NOT NULL,
    attendee_form boolean DEFAULT false NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    categories jsonb DEFAULT '[]'::jsonb NOT NULL,
    deleted_at timestamp without time zone
);

CREATE TABLE public.favorite (
    id text NOT NULL,
    user_id text NOT NULL,
    event_id text NOT NULL,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public.invitation (
    id text NOT NULL,
    organization_id text NOT NULL,
    email text NOT NULL,
    role text,
    status text NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    inviter_id text NOT NULL,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public.member (
    id text NOT NULL,
    organization_id text NOT NULL,
    user_id text NOT NULL,
    role text NOT NULL,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public."order" (
    id text NOT NULL,
    user_id text NOT NULL,
    event_id text NOT NULL,
    status text DEFAULT 'pending_payment'::text NOT NULL,
    payment_method text NOT NULL,
    bank text,
    va_number text,
    qris_url text,
    reference_number text,
    barcode text,
    subtotal integer NOT NULL,
    tax integer NOT NULL,
    service_fee integer NOT NULL,
    total integer NOT NULL,
    voucher_code text,
    voucher_discount integer DEFAULT 0 NOT NULL,
    send_eticket boolean DEFAULT false NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    eticket_sent_at timestamp without time zone
);

CREATE TABLE public.order_item (
    id text NOT NULL,
    order_id text NOT NULL,
    ticket_type_id text NOT NULL,
    ticket_type_name text NOT NULL,
    price integer NOT NULL,
    quantity integer NOT NULL
);

CREATE TABLE public.organization (
    id text NOT NULL,
    name text NOT NULL,
    slug text,
    logo text,
    created_at timestamp without time zone NOT NULL,
    metadata text,
    status text DEFAULT 'pending'::text NOT NULL,
    verification_data text,
    status_note text,
    allow_resubmit boolean DEFAULT false NOT NULL
);

CREATE TABLE public.organization_status_history (
    id text NOT NULL,
    organization_id text NOT NULL,
    from_status text,
    to_status text NOT NULL,
    note text,
    actor_id text,
    actor_type text NOT NULL,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public.payout_request (
    id text NOT NULL,
    organization_id text NOT NULL,
    amount integer NOT NULL,
    bank_name text NOT NULL,
    account_number text NOT NULL,
    account_holder_name text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp without time zone NOT NULL,
    processed_at timestamp without time zone,
    platform_fee integer DEFAULT 0 NOT NULL,
    rejection_reason text,
    processed_by text,
    proof_url text
);

CREATE TABLE public.performer (
    id text NOT NULL,
    event_id text NOT NULL,
    name text NOT NULL,
    image_url text,
    display_order integer DEFAULT 0 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    deleted_at timestamp without time zone
);

CREATE TABLE public.session (
    id text NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    token text NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    ip_address text,
    user_agent text,
    active_organization_id text,
    user_id text NOT NULL
);

CREATE TABLE public.site_setting (
    key text NOT NULL,
    value text NOT NULL,
    updated_at timestamp without time zone NOT NULL
);

CREATE TABLE public.ticket (
    id text NOT NULL,
    order_id text NOT NULL,
    order_item_id text NOT NULL,
    event_id text NOT NULL,
    ticket_type_id text NOT NULL,
    ticket_number text NOT NULL,
    barcode text NOT NULL,
    status text DEFAULT 'valid'::text NOT NULL,
    attendee_name text,
    checked_in_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL
);

CREATE TABLE public.ticket_type (
    id text NOT NULL,
    event_id text NOT NULL,
    name text NOT NULL,
    price integer DEFAULT 0 NOT NULL,
    quantity integer NOT NULL,
    benefits jsonb,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    sold integer DEFAULT 0 NOT NULL,
    display_order integer DEFAULT 0 NOT NULL,
    CONSTRAINT ticket_type_sold_within_quantity CHECK ((sold <= quantity))
);

CREATE TABLE public."user" (
    id text NOT NULL,
    name text NOT NULL,
    email text NOT NULL,
    email_verified boolean NOT NULL,
    image text,
    role text DEFAULT 'user'::text NOT NULL,
    cover_image text,
    location text,
    gender text,
    instagram text,
    tiktok text,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);

CREATE TABLE public.verification (
    id text NOT NULL,
    identifier text NOT NULL,
    value text NOT NULL,
    expires_at timestamp without time zone NOT NULL,
    created_at timestamp without time zone,
    updated_at timestamp without time zone
);

CREATE TABLE public.voucher (
    id text NOT NULL,
    event_id text NOT NULL,
    code text NOT NULL,
    type text DEFAULT 'percent'::text NOT NULL,
    amount integer NOT NULL,
    usage_limit integer,
    usage_count integer DEFAULT 0 NOT NULL,
    expires_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    deleted_at timestamp without time zone
);

CREATE TABLE public.wall_comment (
    id text NOT NULL,
    event_id text NOT NULL,
    user_id text NOT NULL,
    content text NOT NULL,
    created_at timestamp without time zone NOT NULL
);

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.admin_invitation
    ADD CONSTRAINT admin_invitation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.admin_invitation
    ADD CONSTRAINT admin_invitation_token_unique UNIQUE (token);

ALTER TABLE ONLY public.banner
    ADD CONSTRAINT banner_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.event
    ADD CONSTRAINT event_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.favorite
    ADD CONSTRAINT favorite_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.invitation
    ADD CONSTRAINT invitation_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.member
    ADD CONSTRAINT member_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public."order"
    ADD CONSTRAINT order_barcode_unique UNIQUE (barcode);

ALTER TABLE ONLY public.order_item
    ADD CONSTRAINT order_item_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public."order"
    ADD CONSTRAINT order_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.organization
    ADD CONSTRAINT organization_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.organization
    ADD CONSTRAINT organization_slug_unique UNIQUE (slug);

ALTER TABLE ONLY public.organization_status_history
    ADD CONSTRAINT organization_status_history_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.payout_request
    ADD CONSTRAINT payout_request_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.performer
    ADD CONSTRAINT performer_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_token_unique UNIQUE (token);

ALTER TABLE ONLY public.site_setting
    ADD CONSTRAINT site_setting_pkey PRIMARY KEY (key);

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_barcode_unique UNIQUE (barcode);

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_ticket_number_unique UNIQUE (ticket_number);

ALTER TABLE ONLY public.ticket_type
    ADD CONSTRAINT ticket_type_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public."user"
    ADD CONSTRAINT user_email_unique UNIQUE (email);

ALTER TABLE ONLY public."user"
    ADD CONSTRAINT user_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.verification
    ADD CONSTRAINT verification_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.voucher
    ADD CONSTRAINT voucher_pkey PRIMARY KEY (id);

ALTER TABLE ONLY public.wall_comment
    ADD CONSTRAINT wall_comment_pkey PRIMARY KEY (id);

CREATE UNIQUE INDEX favorite_user_event_idx ON public.favorite USING btree (user_id, event_id);

CREATE INDEX order_status_created_idx ON public."order" USING btree (status, created_at);

CREATE INDEX order_status_event_idx ON public."order" USING btree (status, event_id);

CREATE INDEX order_status_payment_idx ON public."order" USING btree (status, payment_method);

CREATE UNIQUE INDEX voucher_event_code_idx ON public.voucher USING btree (event_id, code);

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.admin_invitation
    ADD CONSTRAINT admin_invitation_invited_by_user_id_fk FOREIGN KEY (invited_by) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.event
    ADD CONSTRAINT event_organization_id_organization_id_fk FOREIGN KEY (organization_id) REFERENCES public.organization(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.favorite
    ADD CONSTRAINT favorite_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.favorite
    ADD CONSTRAINT favorite_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.invitation
    ADD CONSTRAINT invitation_inviter_id_user_id_fk FOREIGN KEY (inviter_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.invitation
    ADD CONSTRAINT invitation_organization_id_organization_id_fk FOREIGN KEY (organization_id) REFERENCES public.organization(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.member
    ADD CONSTRAINT member_organization_id_organization_id_fk FOREIGN KEY (organization_id) REFERENCES public.organization(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.member
    ADD CONSTRAINT member_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public."order"
    ADD CONSTRAINT order_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.order_item
    ADD CONSTRAINT order_item_order_id_order_id_fk FOREIGN KEY (order_id) REFERENCES public."order"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.order_item
    ADD CONSTRAINT order_item_ticket_type_id_ticket_type_id_fk FOREIGN KEY (ticket_type_id) REFERENCES public.ticket_type(id);

ALTER TABLE ONLY public."order"
    ADD CONSTRAINT order_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.organization_status_history
    ADD CONSTRAINT organization_status_history_actor_id_user_id_fk FOREIGN KEY (actor_id) REFERENCES public."user"(id) ON DELETE SET NULL;

ALTER TABLE ONLY public.organization_status_history
    ADD CONSTRAINT organization_status_history_organization_id_organization_id_fk FOREIGN KEY (organization_id) REFERENCES public.organization(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.payout_request
    ADD CONSTRAINT payout_request_organization_id_organization_id_fk FOREIGN KEY (organization_id) REFERENCES public.organization(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.performer
    ADD CONSTRAINT performer_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.session
    ADD CONSTRAINT session_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_order_id_order_id_fk FOREIGN KEY (order_id) REFERENCES public."order"(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_order_item_id_order_item_id_fk FOREIGN KEY (order_item_id) REFERENCES public.order_item(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.ticket
    ADD CONSTRAINT ticket_ticket_type_id_ticket_type_id_fk FOREIGN KEY (ticket_type_id) REFERENCES public.ticket_type(id);

ALTER TABLE ONLY public.ticket_type
    ADD CONSTRAINT ticket_type_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.voucher
    ADD CONSTRAINT voucher_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.wall_comment
    ADD CONSTRAINT wall_comment_event_id_event_id_fk FOREIGN KEY (event_id) REFERENCES public.event(id) ON DELETE CASCADE;

ALTER TABLE ONLY public.wall_comment
    ADD CONSTRAINT wall_comment_user_id_user_id_fk FOREIGN KEY (user_id) REFERENCES public."user"(id) ON DELETE CASCADE;

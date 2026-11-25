package organizer

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/physicalxlsx"
)

type orgPhysicalBatchItem struct {
	ID               string      `json:"id"`
	EventID          string      `json:"eventId"`
	RequestID        string      `json:"requestId"`
	Name             string      `json:"name"`
	AccessType       string      `json:"accessType"`
	TicketTypeID     *string     `json:"ticketTypeId"`
	Purpose          string      `json:"purpose"`
	ActivationMode   string      `json:"activationMode"`
	Quantity         int         `json:"quantity"`
	MerchantRef      *string     `json:"merchantRef"`
	GeneratedBy      string      `json:"generatedBy"`
	GeneratedByName  string      `json:"generatedByName"`
	GeneratedByEmail string      `json:"generatedByEmail"`
	GeneratedAt      httpx.Time  `json:"generatedAt"`
	ExportedBy       *string     `json:"exportedBy"`
	ExportedByName   *string     `json:"exportedByName"`
	ExportedByEmail  *string     `json:"exportedByEmail"`
	ExportedAt       *httpx.Time `json:"exportedAt"`
	ExportCount      int         `json:"exportCount"`
	StockCount       int         `json:"stockCount"`
	ActiveCount      int         `json:"activeCount"`
	VoidCount        int         `json:"voidCount"`
}

type orgBatchHistoryItem struct {
	ID         string     `json:"id"`
	Action     string     `json:"action"`
	BatchID    string     `json:"batchId"`
	BatchName  string     `json:"batchName"`
	AccessType string     `json:"accessType"`
	Quantity   int        `json:"quantity"`
	Purpose    string     `json:"purpose"`
	Note       *string    `json:"note"`
	ActorType  string     `json:"actorType"`
	ActorName  *string    `json:"actorName"`
	ActorEmail *string    `json:"actorEmail"`
	CreatedAt  httpx.Time `json:"createdAt"`
}

type orgEventVolunteerItem struct {
	ID             string      `json:"id"`
	EventID        string      `json:"eventId"`
	Email          string      `json:"email"`
	UserID         *string     `json:"userId"`
	UserName       *string     `json:"userName"`
	Role           string      `json:"role"`
	Status         string      `json:"status"`
	InvitedByName  string      `json:"invitedByName"`
	InvitedByEmail string      `json:"invitedByEmail"`
	CreatedAt      httpx.Time  `json:"createdAt"`
	AcceptedAt     *httpx.Time `json:"acceptedAt"`
}

type orgVolunteerHistoryItem struct {
	ID             string     `json:"id"`
	FromStatus     *string    `json:"fromStatus"`
	ToStatus       string     `json:"toStatus"`
	VolunteerEmail string     `json:"volunteerEmail"`
	VolunteerName  *string    `json:"volunteerName"`
	Note           *string    `json:"note"`
	ActorType      string     `json:"actorType"`
	ActorName      *string    `json:"actorName"`
	ActorEmail     *string    `json:"actorEmail"`
	CreatedAt      httpx.Time `json:"createdAt"`
}

func (h *handlers) listEventBatches(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, eventID) {
		return
	}

	ctx := r.Context()
	rows, err := h.db.Query(ctx, `
		select b.id, b.event_id, b.request_id, b.name, b.access_type, b.ticket_type_id,
			b.purpose, b.activation_mode, b.quantity, b.merchant_ref,
			b.generated_by, coalesce(u.name, ''), coalesce(u.email, ''), b.generated_at,
			b.exported_by, ue.name, ue.email, b.exported_at, b.export_count,
			count(pt.id) filter (where pt.validity_status = 'STOCK')::int,
			count(pt.id) filter (where pt.validity_status = 'ACTIVE')::int,
			count(pt.id) filter (where pt.validity_status = 'VOID')::int
		from physical_batch b
		left join "user" u on u.id = b.generated_by
		left join "user" ue on ue.id = b.exported_by
		left join physical_ticket pt on pt.batch_id = b.id
		where b.event_id = $1
		group by b.id, u.name, u.email, ue.name, ue.email
		order by b.generated_at desc, b.id desc`, eventID)
	if err != nil {
		serverError(w, "list batches", err)
		return
	}
	batches, err := pgx.CollectRows(rows, pgx.RowToStructByPos[orgPhysicalBatchItem])
	if err != nil {
		serverError(w, "list batches scan", err)
		return
	}
	if batches == nil {
		batches = []orgPhysicalBatchItem{}
	}

	var history []orgBatchHistoryItem
	hRows, err := h.db.Query(ctx, `
		select
			eh.id as id,
			eh.action as action,
			coalesce(eh.target_id, '') as batch_id,
			coalesce(eh.metadata->>'batchName', b.name, '') as batch_name,
			coalesce(eh.metadata->>'accessType', b.access_type, '') as access_type,
			coalesce((eh.metadata->>'quantity')::int, b.quantity, 0) as quantity,
			coalesce(eh.metadata->>'purpose', b.purpose, 'ALLOCATION') as purpose,
			eh.note as note,
			eh.actor_type as actor_type,
			u.name as actor_name,
			u.email as actor_email,
			eh.created_at
		from event_activity_history eh
		left join physical_batch b on b.id = eh.target_id
		left join "user" u on u.id = eh.actor_id
		where eh.event_id = $1 and eh.category = 'batch'
		order by eh.created_at desc
		limit 50`, eventID)
	if err == nil {
		history, _ = pgx.CollectRows(hRows, pgx.RowToStructByPos[orgBatchHistoryItem])
	} else {
		log.Printf("organizer: list batch history: %v", err)
	}
	if history == nil {
		history = []orgBatchHistoryItem{}
	}

	var enteredCount, totalTickets int
	_ = h.db.QueryRow(ctx, `
		select
			count(pt.id)::int,
			count(pt.id) filter (where tsu.state in ('INSIDE', 'EXITED'))::int
		from physical_ticket pt
		left join ticket_session_usage tsu on tsu.physical_ticket_id = pt.id
		where pt.event_id = $1`, eventID).Scan(&totalTickets, &enteredCount)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"batches": batches,
		"history": history,
		"summary": map[string]int{
			"enteredCount":  enteredCount,
			"totalCapacity": totalTickets,
		},
	})
}

func (h *handlers) exportEventBatch(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	eventID := chi.URLParam(r, "id")
	batchID := chi.URLParam(r, "batchId")
	if !h.requireEvent(w, r, c, eventID) {
		return
	}

	ctx := r.Context()
	var eventName, batchName, accessType string
	err := h.db.QueryRow(ctx, `
		select e.name, b.name, b.access_type
		from physical_batch b
		join event e on e.id = b.event_id
		where b.id = $1 and b.event_id = $2 and e.organization_id = $3 and e.deleted_at is null`,
		batchID, eventID, c.orgID).Scan(&eventName, &batchName, &accessType)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, "export batch lookup", err)
		return
	}

	unlock, ok := physicalxlsx.TryLock(batchID)
	if !ok {
		httpx.Error(w, http.StatusTooManyRequests, "Export Excel untuk batch ini sedang diproses. Mohon tunggu beberapa saat.")
		return
	}
	defer unlock()

	rows, err := h.db.Query(ctx, `
		select code, serial_number
		from physical_ticket
		where batch_id = $1 and event_id = $2
		order by serial_number asc`, batchID, eventID)
	if err != nil {
		serverError(w, "export batch tickets", err)
		return
	}
	defer rows.Close()

	var ticketRows []physicalxlsx.TicketRow
	for rows.Next() {
		var code, serial string
		if err := rows.Scan(&code, &serial); err != nil {
			serverError(w, "export batch scan", err)
			return
		}
		ticketRows = append(ticketRows, physicalxlsx.TicketRow{
			Code:         code,
			SerialNumber: serial,
			AccessType:   accessType,
			EventName:    eventName,
			BatchName:    batchName,
			BatchID:      batchID,
		})
	}
	if err := rows.Err(); err != nil {
		serverError(w, "export batch rows", err)
		return
	}

	xlsxBytes, err := physicalxlsx.Export(ticketRows)
	if err != nil {
		serverError(w, "export batch xlsx", err)
		return
	}

	if _, err := h.db.Exec(ctx, `
		update physical_batch
		set exported_by = $1, exported_at = now(), export_count = export_count + 1
		where id = $2 and event_id = $3`,
		c.user.ID, batchID, eventID); err != nil {
		serverError(w, "export batch audit", err)
		return
	}

	metaJSON, _ := json.Marshal(map[string]any{
		"batchName":  batchName,
		"accessType": accessType,
		"quantity":   len(ticketRows),
	})
	note := fmt.Sprintf("Unduh Excel QR: %s (%s • %d tiket)", batchName, accessType, len(ticketRows))
	if _, err := h.db.Exec(ctx, `
		insert into event_activity_history(
			id, event_id, category, action, target_id, note, metadata, actor_id, actor_type, created_at
		) values($1, $2, 'batch', 'exported', $3, $4, $5::jsonb, $6, 'organizer', now())`,
		dbx.UUID(), eventID, batchID, note, string(metaJSON), c.user.ID); err != nil {
		log.Printf("organizer: record export activity history (non-fatal): %v", err)
	}

	w.Header().Set("Content-Type", physicalxlsx.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="hectix-qr-batch-%s.xlsx"`, batchID))
	w.Write(xlsxBytes)
}

func (h *handlers) listEventVolunteers(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	eventID := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, eventID) {
		return
	}

	ctx := r.Context()
	rows, err := h.db.Query(ctx, `
		select ec.id, ec.event_id, ec.email, ec.user_id, u.name,
			ec.role, ec.status, coalesce(inv.name, ''), coalesce(inv.email, ''),
			ec.created_at, ec.accepted_at
		from event_crew ec
		left join "user" u on u.id = ec.user_id
		left join "user" inv on inv.id = ec.invited_by
		where ec.event_id = $1 and ec.status <> 'revoked'
		order by ec.created_at desc, ec.id desc`, eventID)
	if err != nil {
		serverError(w, "list volunteers", err)
		return
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[orgEventVolunteerItem])
	if err != nil {
		serverError(w, "list volunteers scan", err)
		return
	}
	if items == nil {
		items = []orgEventVolunteerItem{}
	}

	var history []orgVolunteerHistoryItem
	hRows, err := h.db.Query(ctx, `
		select
			eh.id as id,
			eh.from_status as from_status,
			coalesce(eh.to_status, 'pending') as to_status,
			coalesce(eh.metadata->>'email', ec.email, '') as volunteer_email,
			vu.name as volunteer_name,
			eh.note as note,
			eh.actor_type as actor_type,
			au.name as actor_name,
			au.email as actor_email,
			eh.created_at
		from event_activity_history eh
		left join event_crew ec on ec.id = eh.target_id
		left join "user" vu on (vu.id = ec.user_id or lower(vu.email) = lower(coalesce(eh.metadata->>'email', ec.email)))
		left join "user" au on au.id = eh.actor_id
		where eh.event_id = $1 and eh.category = 'volunteer'
		order by eh.created_at desc
		limit 50`, eventID)
	if err == nil {
		history, _ = pgx.CollectRows(hRows, pgx.RowToStructByPos[orgVolunteerHistoryItem])
	} else {
		log.Printf("organizer: list volunteer history: %v", err)
	}
	if history == nil {
		history = []orgVolunteerHistoryItem{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"volunteers": items,
		"history":    history,
	})
}

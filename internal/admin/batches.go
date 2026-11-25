package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/physicalxlsx"
)

type physicalBatchItem struct {
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

type batchHistoryItem struct {
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

var (
	validPurposes        = []string{"ALLOCATION", "RESERVE", "REPLACEMENT"}
	validActivationModes = []string{"ON_HANDOVER", "READY_ON_GENERATE"}
	errBatchConflict     = errors.New("batch request_id conflict")
	errMissingDates      = errors.New("ticket type missing valid dates")
)

func randomPhysicalCode() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "HXP-" + base64.RawURLEncoding.EncodeToString(b)
}

func batchPayloadHash(eventID, accessType, ticketTypeID, purpose, activationMode string, qty int) string {
	raw := fmt.Sprintf("%s|%s|%s|%s|%s|%d", eventID, accessType, ticketTypeID, purpose, activationMode, qty)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (h *handlers) loadBatchByID(ctx context.Context, db dbx.DB, eventID, batchID string) (*physicalBatchItem, error) {
	return one[physicalBatchItem](ctx, db, `
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
		where b.event_id = $1 and b.id = $2
		group by b.id, u.name, u.email, ue.name, ue.email`, eventID, batchID)
}

func (h *handlers) listEventBatches(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")

	var exists bool
	if err := db.QueryRow(ctx, `select exists(select 1 from event where id = $1 and deleted_at is null)`, eventID).Scan(&exists); err != nil {
		serverError(w, "list batches event", err)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}

	batches, err := list[physicalBatchItem](ctx, db, `
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
	if batches == nil {
		batches = []physicalBatchItem{}
	}

	history, err := list[batchHistoryItem](ctx, db, `
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
	if err != nil {
		log.Printf("admin: list batch history: %v", err)
		history = []batchHistoryItem{}
	}
	if history == nil {
		history = []batchHistoryItem{}
	}

	type sessionItem struct {
		ID            string     `json:"id"`
		Name          string     `json:"name"`
		StartsAt      httpx.Time `json:"startsAt"`
		EndsAt        httpx.Time `json:"endsAt"`
		AllowReentry  bool       `json:"allowReentry"`
		QuotaLimit    int        `json:"quotaLimit"`
		CurrentInside int        `json:"currentInside"`
		UniqueEntered int        `json:"uniqueEntered"`
	}
	sessions, err := list[sessionItem](ctx, db, `
		select id, name, starts_at, ends_at, allow_reentry, quota_limit, current_inside, unique_entered
		from event_session
		where event_id = $1
		order by starts_at asc`, eventID)
	if err != nil || sessions == nil {
		sessions = []sessionItem{}
	}

	var enteredCount, totalTickets int
	_ = db.QueryRow(ctx, `
		select
			count(pt.id)::int,
			count(pt.id) filter (where tsu.state in ('INSIDE', 'EXITED'))::int
		from physical_ticket pt
		left join ticket_session_usage tsu on tsu.physical_ticket_id = pt.id
		where pt.event_id = $1`, eventID).Scan(&totalTickets, &enteredCount)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"batches":  batches,
		"history":  history,
		"sessions": sessions,
		"summary": map[string]int{
			"enteredCount":  enteredCount,
			"totalCapacity": totalTickets,
		},
	})
}

func (h *handlers) createEventBatch(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")
	actor := auth.UserFrom(ctx)
	if actor == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var body struct {
		RequestID      string  `json:"requestId"`
		Name           string  `json:"name"`
		AccessType     string  `json:"accessType"`
		TicketTypeID   *string `json:"ticketTypeId"`
		Purpose        string  `json:"purpose"`
		ActivationMode string  `json:"activationMode"`
		Quantity       int     `json:"quantity"`
		MerchantRef    *string `json:"merchantRef"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}

	accessType := jsTrim(body.AccessType)
	if accessType == "" {
		httpx.Error(w, http.StatusBadRequest, "Jenis tiket (accessType) wajib diisi")
		return
	}
	if len(accessType) > 60 {
		httpx.Error(w, http.StatusBadRequest, "Jenis tiket (accessType) maksimal 60 karakter")
		return
	}
	if body.Quantity < 1 || body.Quantity > 10000 {
		httpx.Error(w, http.StatusBadRequest, "Jumlah tiket harus antara 1 sampai 10.000")
		return
	}
	purpose := jsTrim(body.Purpose)
	if purpose == "" {
		purpose = "ALLOCATION"
	}
	if !slices.Contains(validPurposes, purpose) {
		httpx.Error(w, http.StatusBadRequest, "Tujuan batch tidak valid")
		return
	}
	activationMode := jsTrim(body.ActivationMode)
	if activationMode == "" {
		activationMode = "ON_HANDOVER"
	}
	if !slices.Contains(validActivationModes, activationMode) {
		httpx.Error(w, http.StatusBadRequest, "Mode aktivasi tidak valid")
		return
	}

	var ticketTypeID *string
	ticketTypeKey := ""
	if body.TicketTypeID != nil && jsTrim(*body.TicketTypeID) != "" {
		t := jsTrim(*body.TicketTypeID)
		var validTT bool
		if err := db.QueryRow(ctx, `select exists(select 1 from ticket_type where id = $1 and event_id = $2)`, t, eventID).Scan(&validTT); err != nil {
			serverError(w, "validate ticket type", err)
			return
		}
		if !validTT {
			httpx.Error(w, http.StatusBadRequest, "Ticket type tidak valid atau bukan milik event ini")
			return
		}
		ticketTypeID = &t
		ticketTypeKey = t
	}
	var merchantRef *string
	if body.MerchantRef != nil && jsTrim(*body.MerchantRef) != "" {
		m := jsTrim(*body.MerchantRef)
		if len(m) > 120 {
			httpx.Error(w, http.StatusBadRequest, "Referensi merchant maksimal 120 karakter")
			return
		}
		merchantRef = &m
	}

	name := jsTrim(body.Name)
	if name == "" {
		name = fmt.Sprintf("Batch %s (%d tiket)", accessType, body.Quantity)
	}
	if len(name) > 120 {
		httpx.Error(w, http.StatusBadRequest, "Nama batch maksimal 120 karakter")
		return
	}
	reqID := jsTrim(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}
	pHash := batchPayloadHash(eventID, accessType, ticketTypeKey, purpose, activationMode, body.Quantity)

	var (
		startsAt time.Time
		endsAt   *time.Time
	)
	err := db.QueryRow(ctx, `select starts_at, ends_at from event where id = $1 and deleted_at is null`, eventID).Scan(&startsAt, &endsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "batch event lookup", err)
		return
	}

	var (
		batchID  string
		replayed bool
	)
	err = dbx.Tx(ctx, db, func(tx pgx.Tx) error {
		var existingID, existingEventID, existingHash string
		err := tx.QueryRow(ctx, `select id, event_id, payload_hash from physical_batch where request_id = $1`, reqID).
			Scan(&existingID, &existingEventID, &existingHash)
		if err == nil {
			if existingEventID != eventID || existingHash != pHash {
				return errBatchConflict
			}
			batchID = existingID
			replayed = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var sessionIDs []string
		if ticketTypeID != nil && *ticketTypeID != "" {
			sRows, qErr := tx.Query(ctx, `
				select distinct session_id
				from ticket_type_dates
				where ticket_type_id = $1 and session_id is not null
				order by session_id asc`, *ticketTypeID)
			if qErr == nil {
				for sRows.Next() {
					var sID string
					if err := sRows.Scan(&sID); err == nil {
						sessionIDs = append(sessionIDs, sID)
					}
				}
				sRows.Close()
			}
		}

		if len(sessionIDs) == 0 {
			if ticketTypeID != nil && *ticketTypeID != "" {
				return errMissingDates
			}
			var sessionID string
			err = tx.QueryRow(ctx, `select id from event_session where event_id = $1 and name = $2 limit 1`, eventID, accessType).Scan(&sessionID)
			if errors.Is(err, pgx.ErrNoRows) {
				sessionID = dbx.UUID()
				end := startsAt.Add(12 * time.Hour)
				if endsAt != nil {
					end = *endsAt
				}
				if _, err := tx.Exec(ctx, `
					insert into event_session(id, event_id, name, starts_at, ends_at, status)
					values($1, $2, $3, $4, $5, 'open')`,
					sessionID, eventID, accessType, startsAt, end); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			sessionIDs = []string{sessionID}
		}

		sessionsJSON, _ := json.Marshal(sessionIDs)
		batchID = dbx.UUID()
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `
			insert into physical_batch(
				id, event_id, request_id, payload_hash, name, access_type, ticket_type_id,
				session_ids, purpose, activation_mode, quantity, merchant_ref, generated_by, generated_at
			) values($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14)`,
			batchID, eventID, reqID, pHash, name, accessType, ticketTypeID,
			string(sessionsJSON), purpose, activationMode, body.Quantity, merchantRef, actor.ID, now); err != nil {
			return err
		}

		initialValidity := "STOCK"
		var activatedBy *string
		var activatedAt *time.Time
		if activationMode == "READY_ON_GENERATE" {
			initialValidity = "ACTIVE"
			activatedBy = &actor.ID
			activatedAt = &now
		}

		width := max(4, len(strconv.Itoa(body.Quantity)))
		serialFmt := fmt.Sprintf("%%0%dd", width)
		ticketRows := make([][]any, body.Quantity)
		for i := 1; i <= body.Quantity; i++ {
			serial := fmt.Sprintf(serialFmt, i)
			ticketRows[i-1] = []any{
				dbx.UUID(), batchID, eventID, serial, randomPhysicalCode(), accessType,
				string(sessionsJSON), initialValidity, "UNASSIGNED", activatedBy, activatedAt, now,
			}
		}
		if _, err := tx.CopyFrom(ctx,
			pgx.Identifier{"physical_ticket"},
			[]string{"id", "batch_id", "event_id", "serial_number", "code", "access_type", "session_ids", "validity_status", "assignment_status", "activated_by", "activated_at", "created_at"},
			pgx.CopyFromRows(ticketRows),
		); err != nil {
			return err
		}

		metaJSON, _ := json.Marshal(map[string]any{
			"batchName":      name,
			"accessType":     accessType,
			"quantity":       body.Quantity,
			"purpose":        purpose,
			"activationMode": activationMode,
		})
		note := fmt.Sprintf("Generate %d tiket fisik (%s • %s)", body.Quantity, accessType, purpose)
		if _, err := tx.Exec(ctx, `
			insert into event_activity_history(
				id, event_id, category, action, target_id, note, metadata, actor_id, actor_type, created_at
			) values($1, $2, 'batch', 'generated', $3, $4, $5::jsonb, $6, 'admin', $7)`,
			dbx.UUID(), eventID, batchID, note, string(metaJSON), actor.ID, now); err != nil {
			return err
		}
		return nil
	})
	if errors.Is(err, errBatchConflict) {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":  "Request ID sudah digunakan dengan parameter berbeda",
			"result": "CONFLICT",
		})
		return
	}
	if errors.Is(err, errMissingDates) {
		httpx.Error(w, http.StatusBadRequest, "Tanggal berlaku untuk tipe tiket ini belum diisi oleh Organizer. Silakan minta Organizer melengkapi tanggal tiket terlebih dahulu.")
		return
	}
	if err != nil {
		serverError(w, "create physical batch", err)
		return
	}

	batch, err := h.loadBatchByID(ctx, db, eventID, batchID)
	if err != nil || batch == nil {
		serverError(w, "load created batch", err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpx.JSON(w, status, map[string]any{
		"batch":    batch,
		"replayed": replayed,
	})
}

func (h *handlers) exportEventBatch(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")
	batchID := chi.URLParam(r, "batchId")
	actor := auth.UserFrom(ctx)
	if actor == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var eventName, batchName, accessType string
	err := db.QueryRow(ctx, `
		select e.name, b.name, b.access_type
		from physical_batch b
		join event e on e.id = b.event_id
		where b.id = $1 and b.event_id = $2 and e.deleted_at is null`,
		batchID, eventID).Scan(&eventName, &batchName, &accessType)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Batch tiket tidak ditemukan")
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

	type rawTicket struct {
		Code         string
		SerialNumber string
	}
	tickets, err := list[rawTicket](ctx, db, `
		select code, serial_number
		from physical_ticket
		where batch_id = $1 and event_id = $2
		order by serial_number asc`, batchID, eventID)
	if err != nil {
		serverError(w, "export batch tickets", err)
		return
	}

	rows := make([]physicalxlsx.TicketRow, len(tickets))
	for i, t := range tickets {
		rows[i] = physicalxlsx.TicketRow{
			Code:         t.Code,
			SerialNumber: t.SerialNumber,
			AccessType:   accessType,
			EventName:    eventName,
			BatchName:    batchName,
			BatchID:      batchID,
		}
	}

	xlsxBytes, err := physicalxlsx.Export(rows)
	if err != nil {
		serverError(w, "export batch xlsx", err)
		return
	}

	if _, err := db.Exec(ctx, `
		update physical_batch
		set exported_by = $1, exported_at = now(), export_count = export_count + 1
		where id = $2 and event_id = $3`,
		actor.ID, batchID, eventID); err != nil {
		serverError(w, "export batch audit", err)
		return
	}

	metaJSON, _ := json.Marshal(map[string]any{
		"batchName":  batchName,
		"accessType": accessType,
		"quantity":   len(tickets),
	})
	note := fmt.Sprintf("Unduh Excel QR: %s (%s • %d tiket)", batchName, accessType, len(tickets))
	if _, err := db.Exec(ctx, `
		insert into event_activity_history(
			id, event_id, category, action, target_id, note, metadata, actor_id, actor_type, created_at
		) values($1, $2, 'batch', 'exported', $3, $4, $5::jsonb, $6, 'admin', now())`,
		dbx.UUID(), eventID, batchID, note, string(metaJSON), actor.ID); err != nil {
		log.Printf("admin: record export activity history (non-fatal): %v", err)
	}

	w.Header().Set("Content-Type", physicalxlsx.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="hectix-qr-batch-%s.xlsx"`, batchID))
	w.Write(xlsxBytes)
}

func (h *handlers) updateEventSessionReentry(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")
	sessionID := chi.URLParam(r, "sessionId")

	var body struct {
		AllowReentry bool `json:"allowReentry"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}

	tag, err := db.Exec(ctx, `
		update event_session
		set allow_reentry = $1
		where id = $2 and event_id = $3`,
		body.AllowReentry, sessionID, eventID)
	if err != nil {
		serverError(w, "update session reentry", err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "Sesi tidak ditemukan")
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"id":           sessionID,
		"eventId":      eventID,
		"allowReentry": body.AllowReentry,
	})
}

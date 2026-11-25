package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type gateReq struct {
	RequestID string `json:"requestId"`
	Code      string `json:"code"`
	EventID   string `json:"eventId"`
	SessionID string `json:"sessionId"`
	GateLabel string `json:"gateLabel"`
}

type gateResp struct {
	Result        string      `json:"result"`
	Message       string      `json:"message"`
	TicketCode    string      `json:"ticketCode"`
	AccessType    string      `json:"accessType"`
	State         string      `json:"state"`
	EventID       string      `json:"eventId"`
	EventName     string      `json:"eventName"`
	SessionID     string      `json:"sessionId"`
	SessionName   string      `json:"sessionName"`
	ScannedAt     httpx.Time  `json:"scannedAt"`
	ScannerID     string      `json:"scannerId"`
	ScannerName   string      `json:"scannerName"`
	GateLabel     string      `json:"gateLabel"`
	EntryCount    int         `json:"entryCount"`
	CurrentInside int         `json:"currentInside"`
	UniqueEntered int         `json:"uniqueEntered"`
	OrderID       *string     `json:"orderId,omitempty"`
	OrderItemID   *string     `json:"orderItemId,omitempty"`
	AttendeeName  *string     `json:"attendeeName,omitempty"`
	OTSRef        *string     `json:"otsRef,omitempty"`
}

func (h *handlers) resolveEventSession(ctx context.Context, db dbx.DB, eventID, requestedSessionID string) (sessionID, sessionName string, allowReentry bool, quotaLimit, currentInside, uniqueEntered int, err error) {
	if strings.TrimSpace(requestedSessionID) != "" {
		err = db.QueryRow(ctx, `
			select id, name, allow_reentry, quota_limit, current_inside, unique_entered
			from event_session
			where id = $1 and event_id = $2`, requestedSessionID, eventID).
			Scan(&sessionID, &sessionName, &allowReentry, &quotaLimit, &currentInside, &uniqueEntered)
		if err == nil {
			return
		}
	}

	// Try finding an open session
	err = db.QueryRow(ctx, `
		select id, name, allow_reentry, quota_limit, current_inside, unique_entered
		from event_session
		where event_id = $1
		order by starts_at asc limit 1`, eventID).
		Scan(&sessionID, &sessionName, &allowReentry, &quotaLimit, &currentInside, &uniqueEntered)
	if err == nil {
		return
	}

	if errors.Is(err, pgx.ErrNoRows) {
		err = errors.New("Event ini belum memiliki sesi gate / tiket fisik")
	}
	return
}

func (h *handlers) scannerGateIn(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var body gateReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	code := strings.TrimSpace(body.Code)
	eventID := strings.TrimSpace(body.EventID)
	gateLabel := strings.TrimSpace(body.GateLabel)
	if gateLabel == "" {
		gateLabel = "Gate Masuk"
	}
	reqID := strings.TrimSpace(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}

	if code == "" || eventID == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Parameter code dan eventId wajib diisi",
		})
		return
	}

	ctx := r.Context()
	c, ok := h.eventAccess(w, r, eventID)
	if !ok {
		return
	}

	pHash := payloadHash(body)
	if h.checkScanIdempotency(ctx, w, reqID, pHash) {
		return
	}

	var resp gateResp
	now := time.Now().UTC()

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var (
			ptID, ptValidity, ptAssignment, ptAccessType string
			ptEventID, ptOrgID, eventName               string
			ptOrderID, ptOrderItemID, ptOTSRef          *string
			ptSessionIDs                                []string
		)
		err := tx.QueryRow(ctx, `
			select pt.id, pt.validity_status, pt.assignment_status, pt.access_type,
			       pt.event_id, e.organization_id, e.name,
			       pt.order_id, pt.order_item_id, pt.ots_reference,
			       pt.session_ids
			from physical_ticket pt
			join event e on e.id = pt.event_id
			where pt.code = $1 and pt.event_id = $2
			for update of pt`, code, eventID).Scan(
			&ptID, &ptValidity, &ptAssignment, &ptAccessType,
			&ptEventID, &ptOrgID, &eventName,
			&ptOrderID, &ptOrderItemID, &ptOTSRef,
			&ptSessionIDs,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("Gelang fisik tidak ditemukan untuk event ini")
		}
		if err != nil {
			return err
		}
		if ptOrgID != c.orgID {
			return errors.New("Tiket bukan milik organisasi ini")
		}

		if ptValidity == "STOCK" {
			return errors.New("Gelang fisik belum diaktivasi / belum ditukarkan (Status: STOCK)")
		}
		if ptValidity != "ACTIVE" {
			return fmt.Errorf("Gelang fisik tidak valid (Status: %s)", ptValidity)
		}

		reqSessionID := strings.TrimSpace(body.SessionID)
		if reqSessionID == "" && len(ptSessionIDs) > 0 {
			reqSessionID = ptSessionIDs[0]
		}

		// Resolve session
		sID, sName, allowReentry, _, currInside, uniqEntered, err := h.resolveEventSession(ctx, tx, eventID, reqSessionID)
		if err != nil {
			return fmt.Errorf("Gagal memuat sesi acara: %w", err)
		}
		if err := sessionAllowedForTicket(ptSessionIDs, sID, sName); err != nil {
			return err
		}

		// Check ticket_session_usage
		var (
			uState                     string
			uEntryCount                int
			uFirstIn, uLastIn, uLastOut *time.Time
			uLastActorID, uLastGate    *string
		)
		err = tx.QueryRow(ctx, `
			select state, entry_count, first_in_at, last_in_at, last_out_at, last_actor_id, last_gate_label
			from ticket_session_usage
			where physical_ticket_id = $1 and session_id = $2
			for update`, ptID, sID).Scan(
			&uState, &uEntryCount, &uFirstIn, &uLastIn, &uLastOut, &uLastActorID, &uLastGate,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			uState = "NOT_ENTERED"
			uEntryCount = 0
		} else if err != nil {
			return err
		}

		if uState == "INSIDE" {
			return fmt.Errorf("Pengunjung SUDAH BERADA DI DALAM (Check-in pada %s di %s)", formatWIBTime(uLastIn), derefStr(uLastGate, "Gate Masuk"))
		}

		var otherLastIn *time.Time
		var otherGate *string
		err = tx.QueryRow(ctx, `
			select last_in_at, last_gate_label
			from ticket_session_usage
			where physical_ticket_id = $1 and session_id <> $2 and state = 'INSIDE'
			limit 1`, ptID, sID).Scan(&otherLastIn, &otherGate)
		if err == nil {
			return fmt.Errorf("Pengunjung SUDAH BERADA DI DALAM (Check-in pada %s di %s)", formatWIBTime(otherLastIn), derefStr(otherGate, "Gate Masuk"))
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		if uState == "EXITED" && !allowReentry {
			return errors.New("Tiket ini sudah pernah keluar dan sesi ini tidak mengizinkan re-entry")
		}

		// Update or Insert usage
		newEntryCount := uEntryCount + 1
		firstInTime := now
		if uFirstIn != nil {
			firstInTime = *uFirstIn
		}

		if _, err := tx.Exec(ctx, `
			insert into ticket_session_usage(
				physical_ticket_id, session_id, event_id, state, entry_count, first_in_at, last_in_at, last_actor_id, last_gate_label
			) values($1, $2, $3, 'INSIDE', $4, $5, $6, $7, $8)
			on conflict (physical_ticket_id, session_id)
			do update set
				state = 'INSIDE',
				entry_count = ticket_session_usage.entry_count + 1,
				last_in_at = $6,
				last_actor_id = $7,
				last_gate_label = $8`,
			ptID, sID, eventID, newEntryCount, firstInTime, now, c.user.ID, gateLabel); err != nil {
			return err
		}

		// Update occupancy in event_session
		newInside := currInside + 1
		newUnique := uniqEntered
		if uEntryCount == 0 {
			newUnique++
		}
		_, _ = tx.Exec(ctx, `
			update event_session
			set current_inside = current_inside + 1,
			    unique_entered = case when $1 = 1 then unique_entered + 1 else unique_entered end
			where id = $2`, newEntryCount, sID)

		scannerDisplayName := c.user.Name
		if scannerDisplayName == "" {
			scannerDisplayName = c.user.Email
		}

		resp = gateResp{
			Result:        "GATE_IN_OK",
			Message:       "Check-in Masuk Berhasil",
			TicketCode:    code,
			AccessType:    ptAccessType,
			State:         "INSIDE",
			EventID:       eventID,
			EventName:     eventName,
			SessionID:     sID,
			SessionName:   sName,
			ScannedAt:     httpx.Time{Time: now},
			ScannerID:     c.user.ID,
			ScannerName:   scannerDisplayName,
			GateLabel:     gateLabel,
			EntryCount:    newEntryCount,
			CurrentInside: newInside,
			UniqueEntered: newUnique,
			OrderID:       ptOrderID,
			OrderItemID:   ptOrderItemID,
			OTSRef:        ptOTSRef,
		}

		respJSON, _ := json.Marshal(resp)
		_, err = tx.Exec(ctx, `
			insert into scan_ledger(
				request_id, payload_hash, event_id, session_id, actor_id, action, target_id, result_code, response_payload, created_at
			) values($1, $2, $3, $4, $5, 'GATE_IN', $6, 'GATE_IN_OK', $7, now())`,
			reqID, pHash, eventID, sID, c.user.ID, ptID, string(respJSON))
		return err
	})

	if err != nil {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"result": "GATE_IN_REJECTED",
			"error":  err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func (h *handlers) scannerGateOut(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var body gateReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	code := strings.TrimSpace(body.Code)
	eventID := strings.TrimSpace(body.EventID)
	gateLabel := strings.TrimSpace(body.GateLabel)
	if gateLabel == "" {
		gateLabel = "Gate Keluar"
	}
	reqID := strings.TrimSpace(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}

	if code == "" || eventID == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Parameter code dan eventId wajib diisi",
		})
		return
	}

	ctx := r.Context()
	c, ok := h.eventAccess(w, r, eventID)
	if !ok {
		return
	}

	pHash := payloadHash(body)
	if h.checkScanIdempotency(ctx, w, reqID, pHash) {
		return
	}

	var resp gateResp
	now := time.Now().UTC()

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var (
			ptID, ptValidity, ptAssignment, ptAccessType string
			ptEventID, ptOrgID, eventName               string
			ptOrderID, ptOrderItemID, ptOTSRef          *string
			ptSessionIDs                                []string
		)
		err := tx.QueryRow(ctx, `
			select pt.id, pt.validity_status, pt.assignment_status, pt.access_type,
			       pt.event_id, e.organization_id, e.name,
			       pt.order_id, pt.order_item_id, pt.ots_reference,
			       pt.session_ids
			from physical_ticket pt
			join event e on e.id = pt.event_id
			where pt.code = $1 and pt.event_id = $2
			for update of pt`, code, eventID).Scan(
			&ptID, &ptValidity, &ptAssignment, &ptAccessType,
			&ptEventID, &ptOrgID, &eventName,
			&ptOrderID, &ptOrderItemID, &ptOTSRef,
			&ptSessionIDs,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("Gelang fisik tidak ditemukan untuk event ini")
		}
		if err != nil {
			return err
		}
		if ptOrgID != c.orgID {
			return errors.New("Tiket bukan milik organisasi ini")
		}

		reqSessionID := strings.TrimSpace(body.SessionID)
		if reqSessionID == "" && len(ptSessionIDs) > 0 {
			reqSessionID = ptSessionIDs[0]
		}

		sID, sName, _, _, currInside, uniqEntered, err := h.resolveEventSession(ctx, tx, eventID, reqSessionID)
		if err != nil {
			return fmt.Errorf("Gagal memuat sesi acara: %w", err)
		}
		if err := sessionAllowedForTicket(ptSessionIDs, sID, sName); err != nil {
			return err
		}

		var (
			uState      string
			uEntryCount int
		)
		err = tx.QueryRow(ctx, `
			select state, entry_count
			from ticket_session_usage
			where physical_ticket_id = $1 and session_id = $2
			for update`, ptID, sID).Scan(&uState, &uEntryCount)
		if errors.Is(err, pgx.ErrNoRows) || uState != "INSIDE" {
			return errors.New("Pengunjung BELUM MASUK (Status belum tercatat INSIDE)")
		}
		if err != nil {
			return err
		}

		// Update usage to EXITED
		if _, err := tx.Exec(ctx, `
			update ticket_session_usage
			set state = 'EXITED',
			    last_out_at = $1,
			    last_actor_id = $2,
			    last_gate_label = $3
			where physical_ticket_id = $4 and session_id = $5`,
			now, c.user.ID, gateLabel, ptID, sID); err != nil {
			return err
		}

		newInside := currInside - 1
		if newInside < 0 {
			newInside = 0
		}
		_, _ = tx.Exec(ctx, `
			update event_session
			set current_inside = greatest(0, current_inside - 1)
			where id = $1`, sID)

		scannerDisplayName := c.user.Name
		if scannerDisplayName == "" {
			scannerDisplayName = c.user.Email
		}

		resp = gateResp{
			Result:        "GATE_OUT_OK",
			Message:       "Check-out Keluar Berhasil",
			TicketCode:    code,
			AccessType:    ptAccessType,
			State:         "EXITED",
			EventID:       eventID,
			EventName:     eventName,
			SessionID:     sID,
			SessionName:   sName,
			ScannedAt:     httpx.Time{Time: now},
			ScannerID:     c.user.ID,
			ScannerName:   scannerDisplayName,
			GateLabel:     gateLabel,
			EntryCount:    uEntryCount,
			CurrentInside: newInside,
			UniqueEntered: uniqEntered,
			OrderID:       ptOrderID,
			OrderItemID:   ptOrderItemID,
			OTSRef:        ptOTSRef,
		}

		respJSON, _ := json.Marshal(resp)
		_, err = tx.Exec(ctx, `
			insert into scan_ledger(
				request_id, payload_hash, event_id, session_id, actor_id, action, target_id, result_code, response_payload, created_at
			) values($1, $2, $3, $4, $5, 'GATE_OUT', $6, 'GATE_OUT_OK', $7, now())`,
			reqID, pHash, eventID, sID, c.user.ID, ptID, string(respJSON))
		return err
	})

	if err != nil {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"result": "GATE_OUT_REJECTED",
			"error":  err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func derefStr(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

func formatWIBTime(t *time.Time) string {
	if t == nil {
		return "beberapa saat lalu"
	}
	wib := time.FixedZone("WIB", 7*3600)
	return t.In(wib).Format("15:04:05 WIB")
}

func sessionAllowedForTicket(sessionIDs []string, sID, sName string) error {
	if len(sessionIDs) > 0 && !slices.Contains(sessionIDs, sID) {
		return fmt.Errorf("Tiket tidak berlaku untuk sesi %s", sName)
	}
	return nil
}

type physicalTicketRow struct {
	ID               string      `json:"id"`
	Code             string      `json:"code"`
	SerialNumber     string      `json:"serialNumber"`
	AccessType       string      `json:"accessType"`
	ValidityStatus   string      `json:"validityStatus"`
	AssignmentStatus string      `json:"assignmentStatus"`
	UsageState       string      `json:"usageState"`
	EventID          string      `json:"eventId"`
	EventName        string      `json:"eventName"`
	BatchName        string      `json:"batchName"`
	OrderRef         string      `json:"orderRef"`
	CustomerName     string      `json:"customerName"`
	ActivatedAt      *httpx.Time `json:"activatedAt"`
	ActivatedByName  string      `json:"activatedByName"`
	LastActorName    string      `json:"lastActorName"`
	LastInAt         *httpx.Time `json:"lastInAt"`
	LastOutAt        *httpx.Time `json:"lastOutAt"`
	EntryCount       int         `json:"entryCount"`
}

type physicalTicketStats struct {
	TotalTickets  int `json:"totalTickets"`
	Stock         int `json:"stock"`
	Active        int `json:"active"`
	Inside        int `json:"inside"`
	Exited        int `json:"exited"`
	EnteredCount  int `json:"enteredCount"`
	TotalCapacity int `json:"totalCapacity"`
}

func (h *handlers) scannerPhysicalTickets(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	eventID := strings.TrimSpace(r.URL.Query().Get("eventId"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	if eventID != "" && eventID != "all" {
		if _, ok := h.eventAccess(w, r, eventID); !ok {
			return
		}
	}

	// Base conditions
	var condParts []string
	var args []any
	argIdx := 1

	condParts = append(condParts, "e.deleted_at is null")
	if eventID != "" && eventID != "all" {
		condParts = append(condParts, fmt.Sprintf("pt.event_id = $%d", argIdx))
		args = append(args, eventID)
		argIdx++
	} else {
		condParts = append(condParts, fmt.Sprintf(
			`(exists (select 1 from member m where m.organization_id = e.organization_id and m.user_id = $%d and m.role <> 'volunteer')
			  or exists (select 1 from event_crew ec where ec.event_id = e.id and ec.status = 'accepted' and (ec.user_id = $%d or ec.email = lower($%d)))
			  or ($%d::bool and e.status = 'published'))`,
			argIdx, argIdx, argIdx+1, argIdx+2))
		args = append(args, u.ID, u.Email, isAdminStaff(u.Role))
		argIdx += 3
	}

	baseWhere := strings.Join(condParts, " and ")

	// 1. Compute Stats
	statsQuery := fmt.Sprintf(`
		select
			count(pt.id)::int as total,
			count(pt.id) filter (where pt.validity_status = 'STOCK')::int as stock,
			count(pt.id) filter (where pt.validity_status = 'ACTIVE' and coalesce(tsu.state, 'NOT_ENTERED') = 'NOT_ENTERED')::int as active,
			count(pt.id) filter (where pt.validity_status = 'ACTIVE' and tsu.state = 'INSIDE')::int as inside,
			count(pt.id) filter (where pt.validity_status = 'ACTIVE' and tsu.state = 'EXITED')::int as exited
		from physical_ticket pt
		join event e on e.id = pt.event_id
		left join lateral (
			select tsu.state
			from ticket_session_usage tsu
			where tsu.physical_ticket_id = pt.id
			order by coalesce(tsu.last_in_at, tsu.last_out_at) desc nulls last, tsu.session_id asc
			limit 1
		) tsu on true
		where %s`, baseWhere)

	var stats physicalTicketStats
	_ = h.db.QueryRow(ctx, statsQuery, args...).Scan(
		&stats.TotalTickets, &stats.Stock, &stats.Active, &stats.Inside, &stats.Exited,
	)
	stats.EnteredCount = stats.Inside + stats.Exited
	stats.TotalCapacity = stats.TotalTickets

	// 2. Add status & q filters for list
	listCondParts := append([]string{}, condParts...)
	listArgs := append([]any{}, args...)
	listArgIdx := argIdx

	if statusFilter != "" && statusFilter != "all" {
		switch strings.ToUpper(statusFilter) {
		case "STOCK":
			listCondParts = append(listCondParts, "pt.validity_status = 'STOCK'")
		case "ACTIVE":
			listCondParts = append(listCondParts, "pt.validity_status = 'ACTIVE' and coalesce(tsu.state, 'NOT_ENTERED') = 'NOT_ENTERED'")
		case "INSIDE":
			listCondParts = append(listCondParts, "pt.validity_status = 'ACTIVE' and tsu.state = 'INSIDE'")
		case "EXITED":
			listCondParts = append(listCondParts, "pt.validity_status = 'ACTIVE' and tsu.state = 'EXITED'")
		case "VOID", "REVOKED":
			listCondParts = append(listCondParts, "pt.validity_status in ('VOID', 'REVOKED')")
		}
	}

	if q != "" {
		listCondParts = append(listCondParts, fmt.Sprintf("(pt.code ilike $%d or pt.serial_number ilike $%d or pt.ots_reference ilike $%d or o.reference_number ilike $%d or u.name ilike $%d)", listArgIdx, listArgIdx, listArgIdx, listArgIdx, listArgIdx))
		listArgs = append(listArgs, "%"+q+"%")
		listArgIdx++
	}

	listWhere := strings.Join(listCondParts, " and ")

	listQuery := fmt.Sprintf(`
		select
			pt.id, pt.code, pt.serial_number, pt.access_type,
			pt.validity_status, pt.assignment_status,
			coalesce(tsu.state, 'NOT_ENTERED') as usage_state,
			pt.event_id, e.name as event_name,
			pb.name as batch_name,
			coalesce(o.reference_number, pt.ots_reference, '-') as order_ref,
			coalesce(u.name, '-') as customer_name,
			pt.activated_at,
			coalesce(act.name, act.email, '-') as activated_by_name,
			coalesce(la.name, la.email, '-') as last_actor_name,
			tsu.last_in_at, tsu.last_out_at,
			coalesce(tsu.entry_count, 0) as entry_count
		from physical_ticket pt
		join event e on e.id = pt.event_id
		join physical_batch pb on pb.id = pt.batch_id
		left join "order" o on o.id = pt.order_id
		left join "user" u on u.id = o.user_id
		left join "user" act on act.id = pt.activated_by
		left join lateral (
			select tsu.state, tsu.last_in_at, tsu.last_out_at, tsu.entry_count, tsu.last_actor_id
			from ticket_session_usage tsu
			where tsu.physical_ticket_id = pt.id
			order by coalesce(tsu.last_in_at, tsu.last_out_at) desc nulls last, tsu.session_id asc
			limit 1
		) tsu on true
		left join "user" la on la.id = tsu.last_actor_id
		where %s
		order by pt.created_at desc, pt.id asc
		limit 100`, listWhere)

	rows, err := h.db.Query(ctx, listQuery, listArgs...)
	if err != nil {
		serverError(w, "scanner physical tickets query", err)
		return
	}
	defer rows.Close()

	var tickets []physicalTicketRow
	for rows.Next() {
		var (
			row                   physicalTicketRow
			activatedAt, inAt, outAt *time.Time
		)
		if err := rows.Scan(
			&row.ID, &row.Code, &row.SerialNumber, &row.AccessType,
			&row.ValidityStatus, &row.AssignmentStatus,
			&row.UsageState, &row.EventID, &row.EventName,
			&row.BatchName, &row.OrderRef, &row.CustomerName,
			&activatedAt, &row.ActivatedByName, &row.LastActorName,
			&inAt, &outAt, &row.EntryCount,
		); err != nil {
			serverError(w, "scan physical ticket row", err)
			return
		}
		if activatedAt != nil {
			row.ActivatedAt = &httpx.Time{Time: *activatedAt}
		}
		if inAt != nil {
			row.LastInAt = &httpx.Time{Time: *inAt}
		}
		if outAt != nil {
			row.LastOutAt = &httpx.Time{Time: *outAt}
		}
		tickets = append(tickets, row)
	}
	if tickets == nil {
		tickets = []physicalTicketRow{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"stats":   stats,
		"tickets": tickets,
	})
}

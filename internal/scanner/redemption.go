package scanner

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type lookupEticketReq struct {
	Code    string `json:"code"`
	EventID string `json:"eventId"`
}

type redeemItemReq struct {
	OrderItemID   string   `json:"orderItemId"`
	Quantity      int      `json:"quantity"`
	PhysicalCodes []string `json:"physicalCodes"`
}

type redeemReq struct {
	RequestID string          `json:"requestId"`
	EventID   string          `json:"eventId"`
	OrderID   string          `json:"orderId"`
	Mode      string          `json:"mode"` // "LINKED" or "UNLINKED"
	Items     []redeemItemReq `json:"items"`
}

type otsReq struct {
	RequestID     string   `json:"requestId"`
	EventID       string   `json:"eventId"`
	CustomerName  string   `json:"customerName"`
	CustomerPhone string   `json:"customerPhone"`
	PaymentRef    string   `json:"paymentRef"`
	PhysicalCodes []string `json:"physicalCodes"`
}

type redemptionItemDetail struct {
	OrderItemID    string `json:"orderItemId"`
	TicketTypeID   string `json:"ticketTypeId"`
	TicketTypeName string `json:"ticketTypeName"`
	Price          int    `json:"price"`
	BoughtQty      int    `json:"boughtQty"`
	RedeemedQty    int    `json:"redeemedQty"`
	RemainingQty   int    `json:"remainingQty"`
	AccessType     string `json:"accessType"`
}

func (h *handlers) scannerLookupEticket(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body lookupEticketReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_CODE",
			"error":  "Kode e-ticket wajib diisi",
		})
		return
	}
	ctx := r.Context()

	var (
		orderID, eventID, refNum, barcode, status string
		total                                    int64
		createdAt                                time.Time
		buyerName, buyerEmail, eventName, orgID  string
	)
	err := h.db.QueryRow(ctx, `
		select o.id, o.event_id, o.reference_number, coalesce(o.barcode, ''), o.status, o.total, o.created_at,
		       coalesce(u.name, ''), coalesce(u.email, ''), e.name, e.organization_id
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		where (o.barcode = $1 or o.reference_number = $1)
		  and e.deleted_at is null
		limit 1`, code).Scan(
		&orderID, &eventID, &refNum, &barcode, &status, &total, &createdAt,
		&buyerName, &buyerEmail, &eventName, &orgID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, http.StatusNotFound, map[string]any{
			"result": "ORDER_NOT_FOUND",
			"error":  "E-ticket tidak ditemukan",
		})
		return
	}
	if err != nil {
		serverError(w, "lookup eticket query", err)
		return
	}

	if _, _, err := resolveEventRole(ctx, h.db, u, eventID); err != nil {
		httpx.JSON(w, http.StatusForbidden, map[string]any{
			"result": "FORBIDDEN",
			"error":  "Anda tidak memiliki akses scanner untuk event ini",
		})
		return
	}

	if body.EventID != "" && body.EventID != "all" && eventID != body.EventID {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "WRONG_EVENT",
			"error":  "Tiket ini untuk acara lain: " + eventName,
		})
		return
	}

	if status != "paid" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "ORDER_NOT_PAID",
			"error":  fmt.Sprintf("Transaksi belum lunas (status: %s)", status),
		})
		return
	}

	rows, err := h.db.Query(ctx, `
		select oi.id, oi.ticket_type_id, oi.ticket_type_name, oi.price, oi.quantity,
		       coalesce(oir.redeemed_qty, 0), coalesce(oir.voided_qty, 0),
		       coalesce(tam.access_type, 'ALL_DAYS')
		from order_item oi
		left join order_item_redemption oir on oir.order_item_id = oi.id
		left join ticket_type_access_map tam on tam.ticket_type_id = oi.ticket_type_id
		where oi.order_id = $1
		order by oi.id asc`, orderID)
	if err != nil {
		serverError(w, "lookup order items", err)
		return
	}
	defer rows.Close()

	var (
		items          []redemptionItemDetail
		totalBought    int
		totalRedeemed  int
		totalRemaining int
	)
	for rows.Next() {
		var it redemptionItemDetail
		var voided int
		if err := rows.Scan(
			&it.OrderItemID, &it.TicketTypeID, &it.TicketTypeName, &it.Price, &it.BoughtQty,
			&it.RedeemedQty, &voided, &it.AccessType,
		); err != nil {
			serverError(w, "scan redemption items", err)
			return
		}
		rem := it.BoughtQty - it.RedeemedQty - voided
		if rem < 0 {
			rem = 0
		}
		it.RemainingQty = rem
		totalBought += it.BoughtQty
		totalRedeemed += it.RedeemedQty
		totalRemaining += rem
		items = append(items, it)
	}
	if items == nil {
		items = []redemptionItemDetail{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"result": "LOOKUP_OK",
		"order": map[string]any{
			"id":              orderID,
			"referenceNumber": refNum,
			"barcode":         barcode,
			"status":          status,
			"total":           total,
			"buyerName":       buyerName,
			"buyerEmail":      buyerEmail,
			"eventId":         eventID,
			"eventName":       eventName,
			"createdAt":       httpx.Time{Time: createdAt},
		},
		"summary": map[string]any{
			"totalBought":    totalBought,
			"totalRedeemed":  totalRedeemed,
			"totalRemaining": totalRemaining,
		},
		"items": items,
	})
}

func (h *handlers) scannerRedeem(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var body redeemReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	eventID := strings.TrimSpace(body.EventID)
	orderID := strings.TrimSpace(body.OrderID)
	reqID := strings.TrimSpace(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}
	mode := strings.ToUpper(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = "LINKED"
	}
	if eventID == "" || orderID == "" || len(body.Items) == 0 {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Parameter eventId, orderId, dan items wajib diisi",
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

	var activatedCodes []string
	var totalRedeemedNow int
	var respPayload map[string]any

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var oStatus, oEventID, oOrgID string
		err := tx.QueryRow(ctx, `
			select o.status, o.event_id, e.organization_id
			from "order" o
			join event e on e.id = o.event_id
			where o.id = $1 for update`, orderID).Scan(&oStatus, &oEventID, &oOrgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("Order tidak ditemukan")
		}
		if err != nil {
			return err
		}
		if oEventID != eventID || oOrgID != c.orgID {
			return errors.New("Order tidak sesuai dengan event atau organisasi")
		}
		if oStatus != "paid" {
			return errors.New("Order belum lunas")
		}

		for _, item := range body.Items {
			if item.Quantity <= 0 {
				continue
			}
			var (
				itemQty      int
				redeemedSoFar int
				voidedQty    int
			)
			err := tx.QueryRow(ctx, `
				select oi.quantity, coalesce(oir.redeemed_qty, 0), coalesce(oir.voided_qty, 0)
				from order_item oi
				left join order_item_redemption oir on oir.order_item_id = oi.id
				where oi.id = $1 and oi.order_id = $2
				for update of oi`, item.OrderItemID, orderID).Scan(&itemQty, &redeemedSoFar, &voidedQty)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("Item order %s tidak ditemukan", item.OrderItemID)
			}
			if err != nil {
				return err
			}
			if redeemedSoFar+item.Quantity > (itemQty - voidedQty) {
				return fmt.Errorf("Jumlah penukaran (%d) melebihi sisa tiket (%d)", item.Quantity, itemQty-voidedQty-redeemedSoFar)
			}

			// Upsert order_item_redemption
			if _, err := tx.Exec(ctx, `
				insert into order_item_redemption(order_item_id, order_id, redeemed_qty, updated_at)
				values($1, $2, $3, now())
				on conflict (order_item_id)
				do update set redeemed_qty = order_item_redemption.redeemed_qty + $3, updated_at = now()`,
				item.OrderItemID, orderID, item.Quantity); err != nil {
				return err
			}

			if mode == "LINKED" {
				if len(item.PhysicalCodes) != item.Quantity {
					return fmt.Errorf("Jumlah kode gelang fisik (%d) harus sama dengan quantity yang ditukar (%d)", len(item.PhysicalCodes), item.Quantity)
				}
				for _, rawCode := range item.PhysicalCodes {
					pCode := strings.TrimSpace(rawCode)
					if pCode == "" {
						return errors.New("Kode gelang fisik tidak boleh kosong")
					}
					var (
						ptID, ptValidity, ptAssignment string
						sessionIDs                     []string
					)
					err := tx.QueryRow(ctx, `
						select id, validity_status, assignment_status, session_ids
						from physical_ticket
						where code = $1 and event_id = $2
						for update`, pCode, eventID).Scan(&ptID, &ptValidity, &ptAssignment, &sessionIDs)
					if errors.Is(err, pgx.ErrNoRows) {
						return fmt.Errorf("Kode gelang fisik %s tidak ditemukan di event ini", pCode)
					}
					if err != nil {
						return err
					}
					if ptValidity != "STOCK" {
						return fmt.Errorf("Gelang fisik %s sudah aktif / pernah digunakan", pCode)
					}

					now := time.Now().UTC()
					if _, err := tx.Exec(ctx, `
						update physical_ticket
						set validity_status = 'ACTIVE',
						    assignment_status = 'ASSIGNED',
						    order_id = $1,
						    order_item_id = $2,
						    activated_by = $3,
						    activated_at = $4
						where id = $5`,
						orderID, item.OrderItemID, c.user.ID, now, ptID); err != nil {
						return err
					}

					// Seed ticket_session_usage for each assigned session
					if len(sessionIDs) == 0 {
						// Fallback: assign to all open sessions in event
						sRows, err := tx.Query(ctx, `select id from event_session where event_id = $1`, eventID)
						if err == nil {
							for sRows.Next() {
								var sID string
								if err := sRows.Scan(&sID); err == nil {
									sessionIDs = append(sessionIDs, sID)
								}
							}
							sRows.Close()
						}
					}
					for _, sID := range sessionIDs {
						if _, err := tx.Exec(ctx, `
							insert into ticket_session_usage(physical_ticket_id, session_id, event_id, state)
							values($1, $2, $3, 'NOT_ENTERED')
							on conflict do nothing`,
							ptID, sID, eventID); err != nil {
							return err
						}
					}
					activatedCodes = append(activatedCodes, pCode)
				}
			}
			totalRedeemedNow += item.Quantity
		}

		respPayload = map[string]any{
			"result":           "REDEEM_OK",
			"message":          "Penukaran e-ticket berhasil",
			"orderId":          orderID,
			"redeemedCount":    totalRedeemedNow,
			"activatedTickets": activatedCodes,
		}
		respJSON, _ := json.Marshal(respPayload)

		// Record to scan_ledger
		if _, err := tx.Exec(ctx, `
			insert into scan_ledger(
				request_id, payload_hash, event_id, actor_id, action, target_id, result_code, response_payload, created_at
			) values($1, $2, $3, $4, 'REDEEM', $5, 'REDEEM_OK', $6, now())`,
			reqID, pHash, eventID, c.user.ID, orderID, string(respJSON)); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "REDEEM_FAILED",
			"error":  err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, respPayload)
}

func (h *handlers) scannerOTS(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var body otsReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	eventID := strings.TrimSpace(body.EventID)
	reqID := strings.TrimSpace(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}
	if eventID == "" || len(body.PhysicalCodes) == 0 {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Parameter eventId dan physicalCodes wajib diisi",
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

	var activatedCodes []string
	var respPayload map[string]any

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		otsRef := strings.TrimSpace(body.PaymentRef)
		if otsRef == "" {
			otsRef = "OTS-" + now.Format("20060102-150405")
		}

		for _, rawCode := range body.PhysicalCodes {
			pCode := strings.TrimSpace(rawCode)
			if pCode == "" {
				continue
			}
			var (
				ptID, ptValidity, ptAssignment string
				sessionIDs                     []string
			)
			err := tx.QueryRow(ctx, `
				select id, validity_status, assignment_status, session_ids
				from physical_ticket
				where code = $1 and event_id = $2
				for update`, pCode, eventID).Scan(&ptID, &ptValidity, &ptAssignment, &sessionIDs)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("Kode gelang fisik %s tidak ditemukan di event ini", pCode)
			}
			if err != nil {
				return err
			}
			if ptValidity != "STOCK" {
				return fmt.Errorf("Gelang fisik %s sudah aktif / pernah digunakan", pCode)
			}

			if _, err := tx.Exec(ctx, `
				update physical_ticket
				set validity_status = 'ACTIVE',
				    assignment_status = 'ASSIGNED',
				    ots_reference = $1,
				    activated_by = $2,
				    activated_at = $3
				where id = $4`,
				otsRef, c.user.ID, now, ptID); err != nil {
				return err
			}

			// Seed ticket_session_usage
			if len(sessionIDs) == 0 {
				sRows, err := tx.Query(ctx, `select id from event_session where event_id = $1`, eventID)
				if err == nil {
					for sRows.Next() {
						var sID string
						if err := sRows.Scan(&sID); err == nil {
							sessionIDs = append(sessionIDs, sID)
						}
					}
					sRows.Close()
				}
			}
			for _, sID := range sessionIDs {
				if _, err := tx.Exec(ctx, `
					insert into ticket_session_usage(physical_ticket_id, session_id, event_id, state)
					values($1, $2, $3, 'NOT_ENTERED')
					on conflict do nothing`,
					ptID, sID, eventID); err != nil {
					return err
				}
			}
			activatedCodes = append(activatedCodes, pCode)
		}

		respPayload = map[string]any{
			"result":           "OTS_OK",
			"message":          "Tiket OTS berhasil diaktifkan",
			"activatedCount":   len(activatedCodes),
			"activatedTickets": activatedCodes,
			"otsReference":     otsRef,
		}
		respJSON, _ := json.Marshal(respPayload)

		// Record to scan_ledger
		if _, err := tx.Exec(ctx, `
			insert into scan_ledger(
				request_id, payload_hash, event_id, actor_id, action, target_id, result_code, reason, response_payload, created_at
			) values($1, $2, $3, $4, 'OTS', $5, 'OTS_OK', $6, $7, now())`,
			reqID, pHash, eventID, c.user.ID, otsRef, strings.TrimSpace(body.CustomerName), string(respJSON)); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "OTS_FAILED",
			"error":  err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, respPayload)
}

type redeemedEticketRow struct {
	OrderItemID    string     `json:"orderItemId"`
	OrderID        string     `json:"orderId"`
	OrderRef       string     `json:"orderRef"`
	Barcode        string     `json:"barcode"`
	TicketTypeName string     `json:"ticketTypeName"`
	CustomerName   string     `json:"customerName"`
	CustomerEmail  string     `json:"customerEmail"`
	BoughtQty      int        `json:"boughtQty"`
	RedeemedQty    int        `json:"redeemedQty"`
	VoidedQty      int        `json:"voidedQty"`
	RemainingQty   int        `json:"remainingQty"`
	ScannerName    string     `json:"scannerName"`
	RedeemedAt     httpx.Time `json:"redeemedAt"`
}

func (h *handlers) scannerEtickets(w http.ResponseWriter, r *http.Request) {
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

	var condParts []string
	var args []any
	argIdx := 1

	condParts = append(condParts, "e.deleted_at is null", "o.status = 'paid'")
	if eventID != "" && eventID != "all" {
		condParts = append(condParts, fmt.Sprintf("o.event_id = $%d", argIdx))
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

	var totalRedeemed int
	_ = h.db.QueryRow(ctx, fmt.Sprintf(`
		select coalesce(sum(coalesce(oir.redeemed_qty, 0)), 0)::int
		from order_item oi
		join "order" o on o.id = oi.order_id
		join event e on e.id = o.event_id
		left join order_item_redemption oir on oir.order_item_id = oi.id
		where %s`, baseWhere), args...).Scan(&totalRedeemed)

	listCondParts := append([]string{}, condParts...)
	listArgs := append([]any{}, args...)
	listArgIdx := argIdx

	switch strings.ToLower(statusFilter) {
	case "unredeemed":
		listCondParts = append(listCondParts, "coalesce(oir.redeemed_qty, 0) = 0")
	case "redeemed":
		listCondParts = append(listCondParts, "coalesce(oir.redeemed_qty, 0) > 0")
	}

	if q != "" {
		listCondParts = append(listCondParts, fmt.Sprintf(
			`(o.reference_number ilike $%d or coalesce(o.barcode,'') ilike $%d or coalesce(u.name,'') ilike $%d or coalesce(u.email,'') ilike $%d or oi.ticket_type_name ilike $%d)`,
			listArgIdx, listArgIdx, listArgIdx, listArgIdx, listArgIdx))
		listArgs = append(listArgs, "%"+q+"%")
		listArgIdx++
	}

	whereClause := strings.Join(listCondParts, " and ")

	rows, err := h.db.Query(ctx, fmt.Sprintf(`
		select
			oi.id, o.id, o.reference_number, coalesce(o.barcode, ''),
			oi.ticket_type_name, coalesce(u.name, '-'), coalesce(u.email, ''),
			oi.quantity, coalesce(oir.redeemed_qty, 0), coalesce(oir.voided_qty, 0),
			greatest(0, oi.quantity - coalesce(oir.redeemed_qty, 0) - coalesce(oir.voided_qty, 0)) as remaining_qty,
			coalesce(act.name, act.email, '-') as scanner_name,
			coalesce(oir.updated_at, o.created_at) as redeemed_at
		from order_item oi
		join "order" o on o.id = oi.order_id
		join event e on e.id = o.event_id
		left join order_item_redemption oir on oir.order_item_id = oi.id
		left join "user" u on u.id = o.user_id
		left join lateral (
			select sl.actor_id
			from scan_ledger sl
			where sl.target_id = o.id and sl.action = 'REDEEM'
			order by sl.created_at desc
			limit 1
		) sl on true
		left join "user" act on act.id = sl.actor_id
		where %s
		order by o.created_at desc, oi.id asc
		limit 100`, whereClause), listArgs...)
	if err != nil {
		serverError(w, "scanner etickets query", err)
		return
	}
	defer rows.Close()

	var etickets []redeemedEticketRow
	for rows.Next() {
		var (
			row        redeemedEticketRow
			redeemedAt time.Time
		)
		if err := rows.Scan(
			&row.OrderItemID, &row.OrderID, &row.OrderRef, &row.Barcode,
			&row.TicketTypeName, &row.CustomerName, &row.CustomerEmail,
			&row.BoughtQty, &row.RedeemedQty, &row.VoidedQty, &row.RemainingQty,
			&row.ScannerName, &redeemedAt,
		); err != nil {
			serverError(w, "scan eticket row", err)
			return
		}
		row.RedeemedAt = httpx.Time{Time: redeemedAt}
		etickets = append(etickets, row)
	}
	if etickets == nil {
		etickets = []redeemedEticketRow{}
	}

	var totalCapacity int
	if eventID != "" && eventID != "all" {
		_ = h.db.QueryRow(ctx, `select coalesce(sum(quantity), 0)::int from ticket_type where event_id = $1`, eventID).Scan(&totalCapacity)
	} else {
		_ = h.db.QueryRow(ctx, `
			select coalesce(sum(tt.quantity), 0)::int
			from ticket_type tt
			join event e on e.id = tt.event_id
			where e.deleted_at is null
			  and (exists (select 1 from member m where m.organization_id = e.organization_id and m.user_id = $1 and m.role <> 'volunteer')
			       or exists (select 1 from event_crew ec where ec.event_id = e.id and ec.status = 'accepted' and (ec.user_id = $1 or ec.email = lower($2))))`,
			u.ID, u.Email).Scan(&totalCapacity)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"stats": map[string]any{
			"totalRedeemed": totalRedeemed,
			"totalCapacity": totalCapacity,
		},
		"etickets": etickets,
	})
}

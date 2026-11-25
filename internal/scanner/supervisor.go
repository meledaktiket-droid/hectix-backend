package scanner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type replaceWristbandReq struct {
	RequestID string `json:"requestId"`
	EventID   string `json:"eventId"`
	OldCode   string `json:"oldCode"`
	NewCode   string `json:"newCode"`
	Reason    string `json:"reason"`
}

type replaceWristbandResp struct {
	Result      string     `json:"result"`
	Message     string     `json:"message"`
	OldCode     string     `json:"oldCode"`
	NewCode     string     `json:"newCode"`
	AccessType  string     `json:"accessType"`
	UsageState  string     `json:"usageState"`
	Reason      string     `json:"reason"`
	ReplacedAt  httpx.Time `json:"replacedAt"`
	ReplacedBy  string     `json:"replacedBy"`
	EventName   string     `json:"eventName"`
}

type reconciliationSummary struct {
	EventID   string `json:"eventId"`
	EventName string `json:"eventName"`

	// E-ticket metrics
	TotalOrders        int     `json:"totalOrders"`
	TotalETicketsPaid  int     `json:"totalETicketsPaid"`
	TotalETicketsRedeemed int  `json:"totalETicketsRedeemed"`
	TotalETicketsNoShow   int  `json:"totalETicketsNoShow"`
	RedemptionRatePercent float64 `json:"redemptionRatePercent"`

	// Physical wristband metrics
	TotalWristbandsGenerated int `json:"totalWristbandsGenerated"`
	TotalWristbandsStock     int `json:"totalWristbandsStock"`
	TotalWristbandsActive    int `json:"totalWristbandsActive"`
	TotalWristbandsVoid      int `json:"totalWristbandsVoid"`
	TotalOTSTickets          int `json:"totalOtsTickets"`

	// Gate traffic metrics
	UniqueEntered int `json:"uniqueEntered"`
	CurrentInside int `json:"currentInside"`
	TotalExited   int `json:"totalExited"`
	TotalReEntries int `json:"totalReEntries"`

	// Incident metrics
	TotalReplacements int `json:"totalReplacements"`
}

func (h *handlers) scannerReplaceWristband(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	var body replaceWristbandReq
	if err := httpx.Decode(r, &body); err != nil {
		badJSON(w)
		return
	}
	eventID := strings.TrimSpace(body.EventID)
	oldCode := strings.TrimSpace(body.OldCode)
	newCode := strings.TrimSpace(body.NewCode)
	reason := strings.TrimSpace(body.Reason)
	reqID := strings.TrimSpace(body.RequestID)
	if reqID == "" {
		reqID = dbx.UUID()
	}

	if eventID == "" || oldCode == "" || newCode == "" || reason == "" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Event ID, kode lama, kode baru, dan alasan pergantian wajib diisi",
		})
		return
	}

	if oldCode == newCode {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Kode gelang baru tidak boleh sama dengan kode lama",
		})
		return
	}

	c, ok := h.eventAccess(w, r, eventID)
	if !ok {
		return
	}
	if c.role == "volunteer" {
		httpx.JSON(w, http.StatusForbidden, map[string]any{
			"result": "FORBIDDEN",
			"error":  "Hanya Supervisor / Admin yang memiliki izin mengganti gelang",
		})
		return
	}

	ctx := r.Context()
	pHash := payloadHash(body)
	if h.checkScanIdempotency(ctx, w, reqID, pHash) {
		return
	}

	now := time.Now().UTC()
	var resp replaceWristbandResp

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var (
			oldID, oldValidity, oldAssignment, oldAccess string
			oldOrgID, eventName                          string
			oldOrderID, oldOrderItemID, oldOTSRef        *string
		)
		err := tx.QueryRow(ctx, `
			select pt.id, pt.validity_status, pt.assignment_status, pt.access_type,
			       e.organization_id, e.name,
			       pt.order_id, pt.order_item_id, pt.ots_reference
			from physical_ticket pt
			join event e on e.id = pt.event_id
			where pt.code = $1 and pt.event_id = $2
			for update of pt`, oldCode, eventID).Scan(
			&oldID, &oldValidity, &oldAssignment, &oldAccess,
			&oldOrgID, &eventName,
			&oldOrderID, &oldOrderItemID, &oldOTSRef,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("Gelang lama %s tidak ditemukan di acara ini", oldCode)
		}
		if err != nil {
			return err
		}
		if oldOrgID != c.orgID {
			return errors.New("Acara bukan milik organisasi ini")
		}
		if oldValidity != "ACTIVE" {
			return fmt.Errorf("Hanya gelang berstatus ACTIVE yang dapat diganti (status saat ini: %s)", oldValidity)
		}

		// Check new ticket
		var (
			newID, newValidity, newAccess string
		)
		err = tx.QueryRow(ctx, `
			select id, validity_status, access_type
			from physical_ticket
			where code = $1 and event_id = $2
			for update`, newCode, eventID).Scan(
			&newID, &newValidity, &newAccess,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("Gelang baru pengganti %s tidak ditemukan di acara ini", newCode)
		}
		if err != nil {
			return err
		}
		if newValidity != "STOCK" {
			return fmt.Errorf("Gelang baru pengganti %s sudah tidak berstatus STOCK (status: %s)", newCode, newValidity)
		}

		// Update old ticket to VOID
		if _, err := tx.Exec(ctx, `
			update physical_ticket
			set validity_status = 'VOID',
			    replaced_by_id = $1
			where id = $2`, newID, oldID); err != nil {
			return err
		}

		// Update new ticket to ACTIVE & inherit assignment
		if _, err := tx.Exec(ctx, `
			update physical_ticket
			set validity_status = 'ACTIVE',
			    assignment_status = 'ASSIGNED',
			    order_id = $1,
			    order_item_id = $2,
			    ots_reference = $3,
			    activated_by = $4,
			    activated_at = $5
			where id = $6`,
			oldOrderID, oldOrderItemID, oldOTSRef, c.user.ID, now, newID); err != nil {
			return err
		}

		// Migrate session usage if exists
		var finalUsageState string = "NOT_ENTERED"
		rows, err := tx.Query(ctx, `
			select session_id, event_id, state, entry_count, first_in_at, last_in_at, last_out_at, last_actor_id, last_gate_label
			from ticket_session_usage
			where physical_ticket_id = $1`, oldID)
		if err == nil {
			type oldUsage struct {
				sessionID, eventID, state string
				entryCount               int
				firstIn, lastIn, lastOut *time.Time
				actorID, gateLabel       *string
			}
			var usages []oldUsage
			for rows.Next() {
				var u oldUsage
				if err := rows.Scan(
					&u.sessionID, &u.eventID, &u.state, &u.entryCount,
					&u.firstIn, &u.lastIn, &u.lastOut, &u.actorID, &u.gateLabel,
				); err == nil {
					usages = append(usages, u)
					finalUsageState = u.state
				}
			}
			rows.Close()

			for _, u := range usages {
				_, _ = tx.Exec(ctx, `
					insert into ticket_session_usage(
						physical_ticket_id, session_id, event_id, state, entry_count, first_in_at, last_in_at, last_out_at, last_actor_id, last_gate_label
					) values($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
					on conflict (physical_ticket_id, session_id)
					do update set
						state = $4,
						entry_count = $5,
						first_in_at = $6,
						last_in_at = $7,
						last_out_at = $8,
						last_actor_id = $9,
						last_gate_label = $10`,
					newID, u.sessionID, u.eventID, u.state, u.entryCount, u.firstIn, u.lastIn, u.lastOut, u.actorID, u.gateLabel)
			}
		}

		actorName := c.user.Name
		if actorName == "" {
			actorName = c.user.Email
		}

		resp = replaceWristbandResp{
			Result:     "REPLACE_OK",
			Message:    "Pergantian gelang berhasil dilakukan",
			OldCode:    oldCode,
			NewCode:    newCode,
			AccessType: oldAccess,
			UsageState: finalUsageState,
			Reason:     reason,
			ReplacedAt: httpx.Time{Time: now},
			ReplacedBy: actorName,
			EventName:  eventName,
		}

		respJSON, _ := json.Marshal(resp)
		stateBefore, _ := json.Marshal(map[string]any{"id": oldID, "code": oldCode, "status": oldValidity})
		stateAfter, _ := json.Marshal(map[string]any{"id": newID, "code": newCode, "status": "ACTIVE"})

		_, err = tx.Exec(ctx, `
			insert into scan_ledger(
				request_id, payload_hash, event_id, actor_id, action, target_id, result_code, reason, state_before, state_after, response_payload, created_at
			) values($1, $2, $3, $4, 'REPLACE', $5, 'REPLACE_OK', $6, $7, $8, $9, now())`,
			reqID, pHash, eventID, c.user.ID, newID, reason, stateBefore, stateAfter, string(respJSON))
		return err
	})

	if err != nil {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"result": "REPLACE_FAILED",
			"error":  err.Error(),
		})
		return
	}

	httpx.JSON(w, http.StatusOK, resp)
}

func (h *handlers) scannerReconciliation(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	ctx := r.Context()
	eventID := strings.TrimSpace(r.URL.Query().Get("eventId"))
	if eventID == "" || eventID == "all" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"result": "INVALID_PARAMS",
			"error":  "Pilih event spesifik untuk melihat laporan rekonsiliasi",
		})
		return
	}

	c, ok := h.eventAccess(w, r, eventID)
	if !ok {
		return
	}

	var eventName string
	err := h.db.QueryRow(ctx, `
		select name from event where id = $1 and organization_id = $2 and deleted_at is null`,
		eventID, c.orgID).Scan(&eventName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "Event tidak ditemukan"})
		return
	}
	if err != nil {
		serverError(w, "reconciliation event check", err)
		return
	}

	summary := reconciliationSummary{
		EventID:   eventID,
		EventName: eventName,
	}

	// 1. E-ticket summary
	_ = h.db.QueryRow(ctx, `
		select
			count(distinct o.id)::int as total_orders,
			coalesce(sum(oi.quantity), 0)::int as total_tickets_paid,
			coalesce(sum(oir.redeemed_qty), 0)::int as total_redeemed
		from "order" o
		join order_item oi on oi.order_id = o.id
		left join order_item_redemption oir on oir.order_item_id = oi.id
		where o.event_id = $1 and o.status = 'paid'`, eventID).Scan(
		&summary.TotalOrders, &summary.TotalETicketsPaid, &summary.TotalETicketsRedeemed,
	)
	summary.TotalETicketsNoShow = summary.TotalETicketsPaid - summary.TotalETicketsRedeemed
	if summary.TotalETicketsNoShow < 0 {
		summary.TotalETicketsNoShow = 0
	}
	if summary.TotalETicketsPaid > 0 {
		summary.RedemptionRatePercent = float64(summary.TotalETicketsRedeemed) / float64(summary.TotalETicketsPaid) * 100
	}

	// 2. Physical wristband inventory summary
	_ = h.db.QueryRow(ctx, `
		select
			count(*)::int as total_generated,
			count(*) filter (where validity_status = 'STOCK')::int as total_stock,
			count(*) filter (where validity_status = 'ACTIVE')::int as total_active,
			count(*) filter (where validity_status = 'VOID')::int as total_void,
			count(*) filter (where ots_reference is not null)::int as total_ots
		from physical_ticket
		where event_id = $1`, eventID).Scan(
		&summary.TotalWristbandsGenerated,
		&summary.TotalWristbandsStock,
		&summary.TotalWristbandsActive,
		&summary.TotalWristbandsVoid,
		&summary.TotalOTSTickets,
	)

	// 3. Gate Traffic summary
	_ = h.db.QueryRow(ctx, `
		select
			coalesce(sum(unique_entered), 0)::int as unique_entered,
			coalesce(sum(current_inside), 0)::int as current_inside
		from event_session
		where event_id = $1`, eventID).Scan(
		&summary.UniqueEntered, &summary.CurrentInside,
	)

	_ = h.db.QueryRow(ctx, `
		select
			count(*) filter (where action = 'GATE_OUT')::int as total_exited,
			count(*) filter (where action = 'REPLACE')::int as total_replacements
		from scan_ledger
		where event_id = $1`, eventID).Scan(
		&summary.TotalExited, &summary.TotalReplacements,
	)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"result":  "RECONCILIATION_OK",
		"summary": summary,
	})
}

func (h *handlers) scannerExportReconciliation(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	ctx := r.Context()
	eventID := strings.TrimSpace(r.URL.Query().Get("eventId"))
	if eventID == "" || eventID == "all" {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{"error": "Event ID wajib diisi"})
		return
	}

	c, ok := h.eventAccess(w, r, eventID)
	if !ok {
		return
	}

	var eventName string
	err := h.db.QueryRow(ctx, `
		select name from event where id = $1 and organization_id = $2 and deleted_at is null`,
		eventID, c.orgID).Scan(&eventName)
	if err != nil {
		httpx.JSON(w, http.StatusNotFound, map[string]any{"error": "Event tidak ditemukan"})
		return
	}

	f := excelize.NewFile()
	defer f.Close()

	sheet := "Rekonsiliasi"
	f.SetSheetName("Sheet1", sheet)

	// Title
	f.SetCellValue(sheet, "A1", "LAPORAN REKONSILIASI GERBANG & TIKET EVENT")
	f.SetCellValue(sheet, "A2", "Nama Event: "+eventName)
	f.SetCellValue(sheet, "A3", "Tanggal Cetak: "+time.Now().Format("02 Jan 2006 15:04:05 WIB"))

	// Headers
	headers := []string{"No", "Kode Gelang", "Nomor Serial", "Tipe Akses", "Status Validitas", "Status Gerbang", "Ref Pesanan / OTS", "Nama Pembeli", "Waktu Aktivasi", "Petugas", "Waktu Masuk", "Waktu Keluar"}
	for i, h := range headers {
		col, _ := excelize.ColumnNumberToName(i + 1)
		f.SetCellValue(sheet, fmt.Sprintf("%s5", col), h)
	}

	rows, err := h.db.Query(ctx, `
		select
			pt.code, pt.serial_number, pt.access_type, pt.validity_status,
			coalesce(tsu.state, 'NOT_ENTERED') as usage_state,
			coalesce(o.reference_number, pt.ots_reference, '-') as order_ref,
			coalesce(u.name, '-') as customer_name,
			pt.activated_at,
			coalesce(act.name, act.email, '-') as activated_by,
			tsu.last_in_at, tsu.last_out_at
		from physical_ticket pt
		left join "order" o on o.id = pt.order_id
		left join "user" u on u.id = o.user_id
		left join "user" act on act.id = pt.activated_by
		left join ticket_session_usage tsu on tsu.physical_ticket_id = pt.id
		where pt.event_id = $1
		order by pt.serial_number asc`, eventID)
	if err != nil {
		serverError(w, "export query", err)
		return
	}
	defer rows.Close()

	line := 6
	no := 1
	for rows.Next() {
		var (
			code, serial, access, validity, usage, ref, cust, act string
			actAt, inAt, outAt                                   *time.Time
		)
		if err := rows.Scan(
			&code, &serial, &access, &validity, &usage, &ref, &cust,
			&actAt, &act, &inAt, &outAt,
		); err == nil {
			f.SetCellValue(sheet, fmt.Sprintf("A%d", line), no)
			f.SetCellValue(sheet, fmt.Sprintf("B%d", line), code)
			f.SetCellValue(sheet, fmt.Sprintf("C%d", line), serial)
			f.SetCellValue(sheet, fmt.Sprintf("D%d", line), access)
			f.SetCellValue(sheet, fmt.Sprintf("E%d", line), validity)
			f.SetCellValue(sheet, fmt.Sprintf("F%d", line), usage)
			f.SetCellValue(sheet, fmt.Sprintf("G%d", line), ref)
			f.SetCellValue(sheet, fmt.Sprintf("H%d", line), cust)
			if actAt != nil {
				f.SetCellValue(sheet, fmt.Sprintf("I%d", line), actAt.Format("2006-01-02 15:04:05"))
			} else {
				f.SetCellValue(sheet, fmt.Sprintf("I%d", line), "-")
			}
			f.SetCellValue(sheet, fmt.Sprintf("J%d", line), act)
			if inAt != nil {
				f.SetCellValue(sheet, fmt.Sprintf("K%d", line), inAt.Format("2006-01-02 15:04:05"))
			} else {
				f.SetCellValue(sheet, fmt.Sprintf("K%d", line), "-")
			}
			if outAt != nil {
				f.SetCellValue(sheet, fmt.Sprintf("L%d", line), outAt.Format("2006-01-02 15:04:05"))
			} else {
				f.SetCellValue(sheet, fmt.Sprintf("L%d", line), "-")
			}
			line++
			no++
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		serverError(w, "write xlsx", err)
		return
	}

	filename := fmt.Sprintf("reconciliation-%s-%s.xlsx", eventID, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

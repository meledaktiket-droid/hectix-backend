package scanner

import (
	"context"
	"testing"
)

func TestScannerSupervisorReplacementAndReconciliation(t *testing.T) {
	f := setup(t)
	f.SeedUser(t, "vol_sup", "user")
	f.SeedUser(t, "admin_sup", "user")
	f.Exec(t, `insert into member(id, organization_id, user_id, role, created_at)
		values('m-vol-sup', 'org1', 'vol_sup', 'volunteer', now()),
		      ('m-adm-sup', 'org1', 'admin_sup', 'admin', now())`)
	org1 := "org1"
	vol := f.Login(t, "vol_sup", &org1)
	adm := f.Login(t, "admin_sup", &org1)

	// Setup event
	f.event(t, "ev_sup1", "org1")
	f.ticket(t, "tt_sup1", "ev_sup1", 75000, 20, 1, 0)
	f.order(t, "ord_sup1", "ev_sup1", "buyer", "paid", "tt_sup1", 75000, 1, "2026-11-20 10:00:00")

	// Crew assignment
	f.Exec(t, `insert into event_crew(id, event_id, email, user_id, role, status, token, invited_by, expires_at, created_at, accepted_at)
		values('crew-sup-vol', 'ev_sup1', 'vol_sup@example.com', 'vol_sup', 'volunteer', 'accepted', 'tok-svol', 'owner', now() + interval '7 days', now(), now()),
		      ('crew-sup-adm', 'ev_sup1', 'admin_sup@example.com', 'admin_sup', 'admin', 'accepted', 'tok-sadm', 'owner', now() + interval '7 days', now(), now())`)

	// Seed session
	f.Exec(t, `insert into event_session(id, event_id, name, starts_at, ends_at, status, allow_reentry, quota_limit, current_inside, unique_entered, created_at)
		values('ses_sup1', 'ev_sup1', 'Supervisor Session', now() - interval '1 hour', now() + interval '5 hours', 'open', true, 1000, 1, 1, now())`)

	// Seed physical tickets:
	// PT-OLD (ACTIVE, INSIDE), PT-NEW (STOCK)
	f.Exec(t, `insert into physical_batch(id, event_id, request_id, payload_hash, name, access_type, quantity, generated_by, generated_at)
		values('pb_sup', 'ev_sup1', 'req-pbsup', 'hash-pbsup', 'Batch Sup', 'VIP', 2, 'owner', now())`)
	f.Exec(t, `insert into physical_ticket(id, batch_id, event_id, serial_number, code, access_type, validity_status, assignment_status, order_id, created_at)
		values('pt_old', 'pb_sup', 'ev_sup1', '0101', 'WR-OLD-01', 'VIP', 'ACTIVE', 'ASSIGNED', 'ord_sup1', now()),
		      ('pt_new', 'pb_sup', 'ev_sup1', '0102', 'WR-NEW-02', 'VIP', 'STOCK', 'UNASSIGNED', null, now())`)

	// Put PT-OLD in INSIDE state
	f.Exec(t, `insert into ticket_session_usage(physical_ticket_id, session_id, event_id, state, entry_count, first_in_at, last_in_at, last_actor_id, last_gate_label)
		values('pt_old', 'ses_sup1', 'ev_sup1', 'INSIDE', 1, now(), now(), 'vol_sup', 'Gate VIP')`)

	// 1. Volunteer cannot replace wristband
	t.Run("volunteer is forbidden from replacement", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/supervisor/replace", map[string]any{
			"eventId": "ev_sup1",
			"oldCode": "WR-OLD-01",
			"newCode": "WR-NEW-02",
			"reason":  "Gelang robek saat penonton berdesakan",
		}, vol)
		expect(t, rec, 403, "")
	})

	// 2. Admin successfully replaces wristband
	t.Run("admin successfully replaces wristband", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/supervisor/replace", map[string]any{
			"requestId": "req-rep-1",
			"eventId":   "ev_sup1",
			"oldCode":   "WR-OLD-01",
			"newCode":   "WR-NEW-02",
			"reason":    "Gelang robek saat penonton berdesakan",
		}, adm)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "REPLACE_OK" {
			t.Fatalf("expected REPLACE_OK, got %+v", res)
		}
		if res["oldCode"] != "WR-OLD-01" || res["newCode"] != "WR-NEW-02" {
			t.Fatalf("unexpected codes in response: %+v", res)
		}
		if res["usageState"] != "INSIDE" {
			t.Fatalf("expected usageState INSIDE inherited from old ticket, got %v", res["usageState"])
		}

		// Verify DB state
		var oldStatus, replacedBy string
		f.DB.QueryRow(context.Background(), `select validity_status, replaced_by_id from physical_ticket where id = 'pt_old'`).
			Scan(&oldStatus, &replacedBy)
		if oldStatus != "VOID" || replacedBy != "pt_new" {
			t.Fatalf("expected pt_old to be VOID and replaced_by pt_new, got %s / %s", oldStatus, replacedBy)
		}

		var newStatus, newAssign, newOrderID string
		f.DB.QueryRow(context.Background(), `select validity_status, assignment_status, order_id from physical_ticket where id = 'pt_new'`).
			Scan(&newStatus, &newAssign, &newOrderID)
		if newStatus != "ACTIVE" || newAssign != "ASSIGNED" || newOrderID != "ord_sup1" {
			t.Fatalf("expected pt_new to be ACTIVE/ASSIGNED with ord_sup1, got %s / %s / %s", newStatus, newAssign, newOrderID)
		}

		// Verify new ticket inherited ticket_session_usage INSIDE
		var inheritedState string
		f.DB.QueryRow(context.Background(), `select state from ticket_session_usage where physical_ticket_id = 'pt_new' and session_id = 'ses_sup1'`).
			Scan(&inheritedState)
		if inheritedState != "INSIDE" {
			t.Fatalf("expected pt_new state to be INSIDE, got %s", inheritedState)
		}

		// Verify scan_ledger log
		var ledgerAction, ledgerResult string
		f.DB.QueryRow(context.Background(), `select action, result_code from scan_ledger where request_id = 'req-rep-1'`).
			Scan(&ledgerAction, &ledgerResult)
		if ledgerAction != "REPLACE" || ledgerResult != "REPLACE_OK" {
			t.Fatalf("expected REPLACE/REPLACE_OK in ledger, got %s/%s", ledgerAction, ledgerResult)
		}
	})

	// 3. Idempotent re-run returns same result
	t.Run("idempotent replacement re-run", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/supervisor/replace", map[string]any{
			"requestId": "req-rep-1",
			"eventId":   "ev_sup1",
			"oldCode":   "WR-OLD-01",
			"newCode":   "WR-NEW-02",
			"reason":    "Gelang robek saat penonton berdesakan",
		}, adm)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "REPLACE_OK" {
			t.Fatalf("expected REPLACE_OK on idempotent re-run, got %+v", res)
		}
	})

	// 4. Test Reconciliation Summary
	t.Run("get reconciliation summary", func(t *testing.T) {
		rec := f.do(t, "GET", "/api/scanner/reconciliation?eventId=ev_sup1", nil, adm)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "RECONCILIATION_OK" {
			t.Fatalf("expected RECONCILIATION_OK, got %+v", res)
		}
		summary, ok := res["summary"].(map[string]any)
		if !ok {
			t.Fatalf("expected summary object, got %+v", res)
		}
		if summary["totalOrders"].(float64) != 1 {
			t.Fatalf("expected totalOrders 1, got %v", summary["totalOrders"])
		}
		if summary["totalReplacements"].(float64) < 1 {
			t.Fatalf("expected at least 1 replacement, got %v", summary["totalReplacements"])
		}
	})

	// 5. Test Reconciliation Export (.xlsx)
	t.Run("export reconciliation report", func(t *testing.T) {
		rec := f.do(t, "GET", "/api/scanner/reconciliation/export?eventId=ev_sup1", nil, adm)
		expect(t, rec, 200, "")
		contentType := rec.Header().Get("Content-Type")
		expectedCT := "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		if contentType != expectedCT {
			t.Fatalf("expected Content-Type %s, got %s", expectedCT, contentType)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("expected non-empty xlsx file body")
		}
	})
}

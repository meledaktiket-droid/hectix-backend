package scanner

import (
	"testing"
)

func TestScannerGateInAndOut(t *testing.T) {
	f := setup(t)
	f.SeedUser(t, "vol_gate", "user")
	f.Exec(t, `insert into member(id, organization_id, user_id, role, created_at) values('m-vol-gate', 'org1', 'vol_gate', 'volunteer', now())`)
	org1 := "org1"
	vol := f.Login(t, "vol_gate", &org1)

	// Setup event
	f.event(t, "ev_gate1", "org1")
	f.ticket(t, "tt_gate1", "ev_gate1", 50000, 10, 1, 0)
	f.order(t, "ord_gate1", "ev_gate1", "buyer", "paid", "tt_gate1", 50000, 1, "2026-11-19 10:00:00")

	// Seed session
	f.Exec(t, `insert into event_session(id, event_id, name, starts_at, ends_at, status, allow_reentry, quota_limit, current_inside, unique_entered, created_at)
		values('ses_gate1', 'ev_gate1', 'Gate Session', now() - interval '1 hour', now() + interval '5 hours', 'open', true, 1000, 0, 0, now())`)

	// Seed physical batch & tickets
	f.Exec(t, `insert into physical_batch(id, event_id, request_id, payload_hash, name, access_type, quantity, generated_by, generated_at)
		values('pb_gate', 'ev_gate1', 'req-pbg', 'hash-pbg', 'Batch Gate', 'DAY_1', 2, 'owner', now())`)
	f.Exec(t, `insert into physical_ticket(id, batch_id, event_id, serial_number, code, access_type, validity_status, assignment_status, created_at)
		values('pt_g1', 'pb_gate', 'ev_gate1', '0001', 'WR-GATE-01', 'DAY_1', 'STOCK', 'UNASSIGNED', now()),
		      ('pt_g2', 'pb_gate', 'ev_gate1', '0002', 'WR-GATE-02', 'DAY_1', 'ACTIVE', 'ASSIGNED', now())`)

	// 1. Unassigned volunteer is forbidden
	t.Run("unassigned volunteer is forbidden", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-in", map[string]any{
			"code":    "WR-GATE-02",
			"eventId": "ev_gate1",
		}, vol)
		expect(t, rec, 403, "")
	})

	// Assign volunteer to ev_gate1
	f.Exec(t, `insert into event_crew(id, event_id, email, user_id, role, status, token, invited_by, expires_at, created_at, accepted_at)
		values('crew-gate', 'ev_gate1', 'vol_gate@example.com', 'vol_gate', 'volunteer', 'accepted', 'tok-gate', 'owner', now() + interval '7 days', now(), now())`)

	// 2. STOCK ticket cannot gate-in
	t.Run("STOCK ticket cannot gate in", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-in", map[string]any{
			"code":    "WR-GATE-01",
			"eventId": "ev_gate1",
		}, vol)
		expect(t, rec, 409, "")
		res := decode(t, rec)
		if res["result"] != "GATE_IN_REJECTED" {
			t.Fatalf("expected GATE_IN_REJECTED, got %+v", res)
		}
	})

	// 3. Successful Gate-In with ACTIVE ticket
	t.Run("successful gate-in", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-in", map[string]any{
			"requestId": "req-in-1",
			"code":      "WR-GATE-02",
			"eventId":   "ev_gate1",
			"gateLabel": "Gate Barat",
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "GATE_IN_OK" || res["entryCount"] != float64(1) || res["currentInside"] != float64(1) {
			t.Fatalf("unexpected gate-in response: %+v", res)
		}
		if res["scannerName"] == "" {
			t.Fatalf("expected scannerName to be populated, got empty")
		}

		// Verify state in DB
		state := f.scalar(t, `select state from ticket_session_usage where physical_ticket_id = 'pt_g2'`).(string)
		if state != "INSIDE" {
			t.Fatalf("expected state INSIDE, got %s", state)
		}
	})

	// 4. Duplicate Gate-In while INSIDE is rejected
	t.Run("duplicate gate-in while inside rejected", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-in", map[string]any{
			"requestId": "req-in-2",
			"code":      "WR-GATE-02",
			"eventId":   "ev_gate1",
		}, vol)
		expect(t, rec, 409, "")
		res := decode(t, rec)
		if res["result"] != "GATE_IN_REJECTED" {
			t.Fatalf("expected GATE_IN_REJECTED, got %+v", res)
		}
	})

	// 5. Gate-Out when INSIDE succeeds
	t.Run("gate-out succeeds", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-out", map[string]any{
			"requestId": "req-out-1",
			"code":      "WR-GATE-02",
			"eventId":   "ev_gate1",
			"gateLabel": "Gate Barat",
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "GATE_OUT_OK" || res["state"] != "EXITED" || res["currentInside"] != float64(0) {
			t.Fatalf("unexpected gate-out response: %+v", res)
		}

		// Verify state in DB
		state := f.scalar(t, `select state from ticket_session_usage where physical_ticket_id = 'pt_g2'`).(string)
		if state != "EXITED" {
			t.Fatalf("expected state EXITED, got %s", state)
		}
	})

	// 6. Duplicate Gate-Out when already EXITED is rejected
	t.Run("duplicate gate-out when already exited is rejected", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-out", map[string]any{
			"requestId": "req-out-2",
			"code":      "WR-GATE-02",
			"eventId":   "ev_gate1",
		}, vol)
		expect(t, rec, 409, "")
		res := decode(t, rec)
		if res["result"] != "GATE_OUT_REJECTED" {
			t.Fatalf("expected GATE_OUT_REJECTED, got %+v", res)
		}
	})

	// 7. Re-entry (Gate-In again after Gate-Out) succeeds because allow_reentry = true
	t.Run("re-entry succeeds with incremented entryCount", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/gate-in", map[string]any{
			"requestId": "req-in-3",
			"code":      "WR-GATE-02",
			"eventId":   "ev_gate1",
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "GATE_IN_OK" || res["entryCount"] != float64(2) || res["currentInside"] != float64(1) {
			t.Fatalf("unexpected re-entry response: %+v", res)
		}
	})

	// 8. List physical tickets and stats
	t.Run("list physical tickets and stats", func(t *testing.T) {
		rec := f.do(t, "GET", "/api/scanner/physical-tickets?eventId=ev_gate1", nil, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		stats := res["stats"].(map[string]any)
		if stats["totalTickets"] != float64(2) || stats["stock"] != float64(1) || stats["active"] != float64(0) || stats["inside"] != float64(1) || stats["enteredCount"] != float64(1) || stats["totalCapacity"] != float64(2) {
			t.Fatalf("unexpected stats: %+v", stats)
		}
		tickets := res["tickets"].([]any)
		if len(tickets) != 2 {
			t.Fatalf("expected 2 tickets, got %d", len(tickets))
		}
		for _, item := range tickets {
			row := item.(map[string]any)
			if row["code"] == "WR-GATE-02" && row["lastActorName"] != "vol_gate" {
				t.Fatalf("expected lastActorName vol_gate, got %v", row["lastActorName"])
			}
		}

		// ACTIVE filter must exclude tickets that are already INSIDE
		recActive := f.do(t, "GET", "/api/scanner/physical-tickets?eventId=ev_gate1&status=ACTIVE", nil, vol)
		expect(t, recActive, 200, "")
		if activeList := decode(t, recActive)["tickets"].([]any); len(activeList) != 0 {
			t.Fatalf("expected 0 un-entered ACTIVE tickets, got %d", len(activeList))
		}

		// Multiple ticket_session_usage rows for one physical ticket must not duplicate rows or stats
		f.Exec(t, `insert into event_session(id, event_id, name, starts_at, ends_at, status, allow_reentry, quota_limit, current_inside, unique_entered, created_at)
			values('ses_gate2', 'ev_gate1', 'Gate Session 2', now() - interval '1 hour', now() + interval '5 hours', 'open', true, 1000, 0, 0, now())`)
		f.Exec(t, `insert into ticket_session_usage(physical_ticket_id, session_id, event_id, state, entry_count)
			values('pt_g2', 'ses_gate2', 'ev_gate1', 'NOT_ENTERED', 0)`)
		recNoDup := f.do(t, "GET", "/api/scanner/physical-tickets?eventId=ev_gate1", nil, vol)
		expect(t, recNoDup, 200, "")
		resNoDup := decode(t, recNoDup)
		if resNoDup["stats"].(map[string]any)["totalTickets"] != float64(2) || len(resNoDup["tickets"].([]any)) != 2 {
			t.Fatalf("expected deduplicated 2 tickets, got %+v", resNoDup)
		}
	})

	t.Run("list event sessions", func(t *testing.T) {
		rec := f.do(t, "GET", "/api/scanner/sessions?eventId=ev_gate1", nil, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		sessions := res["sessions"].([]any)
		if len(sessions) == 0 {
			t.Fatalf("expected at least 1 session, got 0")
		}
	})
}

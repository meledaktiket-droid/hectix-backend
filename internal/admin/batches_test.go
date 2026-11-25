package admin

import (
	"testing"
)

func TestPhysicalBatchGenerateAndExport(t *testing.T) {
	e := setup(t)
	e.seedOrg(t, "org1", "Org One", "active")
	e.seedEvent(t, "ev1", "org1", "Gauland Fest", "published")

	e.guarded(t, "GET", "/api/events/ev1/batches", nil)
	e.guarded(t, "POST", "/api/events/ev1/batches", map[string]any{"accessType": "DAY_1", "quantity": 10})

	// T01: Generate 100 physical codes
	rec1 := e.do(t, "POST", "/api/events/ev1/batches", map[string]any{
		"requestId":      "req-batch-100",
		"name":           "Batch Day 1",
		"accessType":     "DAY_1",
		"purpose":        "ALLOCATION",
		"activationMode": "ON_HANDOVER",
		"quantity":       100,
	}, e.admin)
	want(t, rec1, 201, "")
	res1 := decode[map[string]any](t, rec1)
	batch1 := res1["batch"].(map[string]any)
	batchID := batch1["id"].(string)
	if batch1["quantity"] != float64(100) || batch1["stockCount"] != float64(100) || batch1["activeCount"] != float64(0) {
		t.Fatalf("unexpected batch counts: %+v", batch1)
	}

	// T02: Retry same requestId with identical payload returns 200 and same batch (no duplicate tickets)
	rec2 := e.do(t, "POST", "/api/events/ev1/batches", map[string]any{
		"requestId":      "req-batch-100",
		"name":           "Batch Day 1",
		"accessType":     "DAY_1",
		"purpose":        "ALLOCATION",
		"activationMode": "ON_HANDOVER",
		"quantity":       100,
	}, e.admin)
	want(t, rec2, 200, "")
	res2 := decode[map[string]any](t, rec2)
	if res2["replayed"] != true || res2["batch"].(map[string]any)["id"] != batchID {
		t.Fatalf("expected replayed batch %s, got %+v", batchID, res2)
	}

	// T02: Same requestId with different payload (quantity 50 instead of 100) returns 409 CONFLICT
	recConflict := e.do(t, "POST", "/api/events/ev1/batches", map[string]any{
		"requestId":      "req-batch-100",
		"accessType":     "DAY_1",
		"activationMode": "ON_HANDOVER",
		"quantity":       50,
	}, e.admin)
	want(t, recConflict, 409, "")

	// T13: READY_ON_GENERATE sets initial status to ACTIVE
	recReady := e.do(t, "POST", "/api/events/ev1/batches", map[string]any{
		"requestId":      "req-batch-ready",
		"accessType":     "ALL_DAYS",
		"activationMode": "READY_ON_GENERATE",
		"quantity":       5,
	}, e.admin)
	want(t, recReady, 201, "")
	batchReady := decode[map[string]any](t, recReady)["batch"].(map[string]any)
	if batchReady["activeCount"] != float64(5) || batchReady["stockCount"] != float64(0) {
		t.Fatalf("expected 5 ACTIVE tickets in READY_ON_GENERATE mode, got %+v", batchReady)
	}

	// T03: Export Excel twice increments exportCount without increasing ticket count
	for i := 1; i <= 2; i++ {
		recExp := e.do(t, "GET", "/api/events/ev1/batches/"+batchID+"/export", nil, e.admin)
		want(t, recExp, 200, "")
		if len(recExp.Body.Bytes()) == 0 {
			t.Fatal("expected non-empty xlsx export")
		}
	}

	var totalTickets, exportCount int
	e.scalar(t, &totalTickets, `select count(*) from physical_ticket where batch_id = $1`, batchID)
	e.scalar(t, &exportCount, `select export_count from physical_batch where id = $1`, batchID)
	if totalTickets != 100 || exportCount != 2 {
		t.Fatalf("want totalTickets=100 exportCount=2, got totalTickets=%d exportCount=%d", totalTickets, exportCount)
	}

	// List batches returns sessions + summary, and admin can toggle allowReentry
	recList := e.do(t, "GET", "/api/events/ev1/batches", nil, e.admin)
	want(t, recList, 200, "")
	listBody := decode[map[string]any](t, recList)
	sessions := listBody["sessions"].([]any)
	if len(sessions) == 0 {
		t.Fatal("expected at least 1 event session in batches response")
	}
	sesID := sessions[0].(map[string]any)["id"].(string)

	recToggle := e.do(t, "PATCH", "/api/events/ev1/sessions/"+sesID, map[string]any{
		"allowReentry": true,
	}, e.admin)
	want(t, recToggle, 200, "")
	if decode[map[string]any](t, recToggle)["allowReentry"] != true {
		t.Fatal("expected allowReentry=true")
	}

	var allowReentry bool
	e.scalar(t, &allowReentry, `select allow_reentry from event_session where id = $1`, sesID)
	if !allowReentry {
		t.Fatal("expected allow_reentry=true in DB")
	}

	// TicketType without valid_dates is blocked from batch generation
	e.Exec(t, `insert into ticket_type(id, event_id, name, price, quantity, created_at, updated_at) values('tt_nodate', 'ev1', 'VVIP', 500000, 50, now(), now())`)
	recBlocked := e.do(t, "POST", "/api/events/ev1/batches", map[string]any{
		"requestId":    "req-batch-nodate",
		"accessType":   "VVIP",
		"ticketTypeId": "tt_nodate",
		"quantity":     10,
	}, e.admin)
	want(t, recBlocked, 400, "")
}

package admin

import (
	"testing"
)

func TestAdminScanner(t *testing.T) {
	e := setup(t)
	e.seedOrg(t, "org1", "Org One", "active")
	e.seedEvent(t, "ev1", "org1", "Fest One", "published")
	e.seedTicketType(t, "tt1", "ev1", 100000, 10, 0)
	e.seedOrder(t, "ord1", "usr", "ev1", "tt1", "paid", "qris", 2, 100000, "1 hour")
	e.Exec(t, `update "order" set barcode = 'ADM-QR-1' where id = 'ord1'`)
	e.Exec(t, `insert into ticket(id,order_id,order_item_id,event_id,ticket_type_id,ticket_number,barcode,status,attendee_name,created_at) values
		('atk1','ord1','ord1-i','ev1','tt1','HX-2026-1001','ADM-TK1','valid','User',now()),
		('atk2','ord1','ord1-i','ev1','tt1','HX-2026-1002','ADM-TK2','valid','User',now())`)

	e.guarded(t, "GET", "/api/scanner/samples", nil)
	e.guarded(t, "POST", "/api/scanner/lookup", map[string]any{"code": "ADM-QR-1"})
	e.guarded(t, "POST", "/api/scanner/checkin", map[string]any{"code": "ADM-QR-1"})
	e.guarded(t, "POST", "/api/scanner/reset", map[string]any{"orderId": "ord1"})

	// 1. Lookup
	recLook := e.do(t, "POST", "/api/scanner/lookup", map[string]any{"code": "ADM-QR-1"}, e.staff)
	want(t, recLook, 200, "")

	// 2. First checkin succeeds
	recIn := e.do(t, "POST", "/api/scanner/checkin", map[string]any{"code": "ADM-QR-1"}, e.staff)
	want(t, recIn, 200, "")
	inData := decode[map[string]any](t, recIn)
	if inData["result"] != "checked_in" || inData["checkedInNow"] != float64(2) {
		t.Fatalf("unexpected checkin result: %+v", inData)
	}

	// 3. Second checkin returns 409 already_checked_in
	recDup := e.do(t, "POST", "/api/scanner/checkin", map[string]any{"code": "ADM-QR-1"}, e.staff)
	want(t, recDup, 409, "")
	dupData := decode[map[string]any](t, recDup)
	if dupData["result"] != "already_checked_in" {
		t.Fatalf("expected already_checked_in, got %+v", dupData)
	}

	// 4. Reset restores valid status
	recReset := e.do(t, "POST", "/api/scanner/reset", map[string]any{"orderId": "ord1"}, e.staff)
	want(t, recReset, 200, "")
}

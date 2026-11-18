package organizer

import (
	"testing"
)

func TestScannerAndVolunteerRole(t *testing.T) {
	f := setup(t)
	f.SeedUser(t, "vol", "user")
	f.Exec(t, `insert into member(id,organization_id,user_id,role,created_at) values('mvol','org1','vol','volunteer',now())`)
	org1 := "org1"
	vol := f.Login(t, "vol", &org1)

	f.event(t, "ev1", "org1")
	f.ticket(t, "tt1", "ev1", 50000, 10, 2, 0)
	f.order(t, "ord1", "ev1", "buyer", "paid", "tt1", 50000, 2, "2026-11-16 08:00:00")
	f.Exec(t, `update "order" set barcode = 'QR-ORD1' where id = 'ord1'`)
	f.Exec(t, `insert into ticket(id,order_id,order_item_id,event_id,ticket_type_id,ticket_number,barcode,status,attendee_name,created_at) values
		('tk1','ord1','ord1-i','ev1','tt1','HX-2026-0001-0001','BAR-TK1','valid','Buyer',now()),
		('tk2','ord1','ord1-i','ev1','tt1','HX-2026-0001-0002','BAR-TK2','valid','Buyer',now())`)

	f.event(t, "ev2", "org2")
	f.ticket(t, "tt2", "ev2", 50000, 10, 1, 0)
	f.order(t, "ord2", "ev2", "buyer", "paid", "tt2", 50000, 1, "2026-11-16 08:00:00")
	f.Exec(t, `update "order" set barcode = 'QR-ORD2' where id = 'ord2'`)

	t.Run("volunteer is blocked from non-scanner organizer endpoints", func(t *testing.T) {
		expect(t, f.do(t, "GET", "/api/dashboard", nil, vol), 403, errBody("Forbidden"))
		expect(t, f.do(t, "GET", "/api/events", nil, vol), 403, errBody("Forbidden"))
		expect(t, f.do(t, "GET", "/api/orders", nil, vol), 403, errBody("Forbidden"))
		expect(t, f.do(t, "GET", "/api/finance", nil, vol), 403, errBody("Forbidden"))
	})

	t.Run("volunteer and owner can access scanner events and samples", func(t *testing.T) {
		authCases(t, f, "GET", "/api/scanner/events", nil)
		authCases(t, f, "GET", "/api/scanner/samples", nil)

		evRes := decode(t, f.do(t, "GET", "/api/scanner/events", nil, vol))
		if evRes["role"] != "volunteer" || len(evRes["events"].([]any)) != 1 {
			t.Fatalf("unexpected events response: %+v", evRes)
		}

		smpRes := decode(t, f.do(t, "GET", "/api/scanner/samples", nil, vol))
		if len(smpRes["samples"].([]any)) != 1 {
			t.Fatalf("unexpected samples response: %+v", smpRes)
		}
	})

	t.Run("lookup and cross-organization isolation", func(t *testing.T) {
		expect(t, f.do(t, "POST", "/api/scanner/lookup", map[string]any{"code": "QR-ORD2"}, vol),
			404, errBody("Tiket tidak ditemukan di organisasi ini"))

		res := decode(t, f.do(t, "POST", "/api/scanner/lookup", map[string]any{"code": "QR-ORD1"}, vol))
		if res["result"] != "ok" {
			t.Fatalf("expected ok, got %+v", res)
		}
	})

	t.Run("single ticket checkin, full order checkin, duplicate detection, and reset", func(t *testing.T) {
		// 1. Check in single ticket #1 via volunteer
		r1 := decode(t, f.do(t, "POST", "/api/scanner/checkin", map[string]any{"code": "QR-ORD1", "ticketId": "tk1"}, vol))
		if r1["result"] != "checked_in" || r1["checkedInNow"] != float64(1) {
			t.Fatalf("expected 1 ticket checked in, got %+v", r1)
		}

		// 2. Check in remaining tickets on order via volunteer
		r2 := decode(t, f.do(t, "POST", "/api/scanner/checkin", map[string]any{"code": "QR-ORD1"}, vol))
		if r2["result"] != "checked_in" || r2["checkedInNow"] != float64(1) {
			t.Fatalf("expected remaining 1 ticket checked in, got %+v", r2)
		}

		// 3. Scanning again returns 409 already_checked_in
		recDup := f.do(t, "POST", "/api/scanner/checkin", map[string]any{"code": "QR-ORD1"}, vol)
		expect(t, recDup, 409, "")
		if d := decode(t, recDup); d["result"] != "already_checked_in" {
			t.Fatalf("expected already_checked_in, got %+v", d)
		}

		// 4. Volunteer cannot reset, owner can reset
		expect(t, f.do(t, "POST", "/api/scanner/reset", map[string]any{"orderId": "ord1"}, vol), 403, errBody("Forbidden"))
		rReset := decode(t, f.do(t, "POST", "/api/scanner/reset", map[string]any{"orderId": "ord1"}, f.owner))
		if rReset["result"] != "reset" {
			t.Fatalf("expected reset, got %+v", rReset)
		}
	})
}

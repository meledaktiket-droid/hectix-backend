package scanner

import (
	"fmt"
	"testing"
)

func TestScannerRedemptionAndOTS(t *testing.T) {
	f := setup(t)
	f.SeedUser(t, "vol_red", "user")
	f.Exec(t, `insert into member(id, organization_id, user_id, role, created_at) values('m-vol-red', 'org1', 'vol_red', 'volunteer', now())`)
	org1 := "org1"
	vol := f.Login(t, "vol_red", &org1)

	// Setup event 1 & event 2
	f.event(t, "ev_red1", "org1")
	f.ticket(t, "tt_red1", "ev_red1", 75000, 10, 3, 0)
	f.order(t, "ord_red1", "ev_red1", "buyer", "paid", "tt_red1", 75000, 3, "2026-11-19 10:00:00")
	f.Exec(t, `update "order" set barcode = 'E-QR-RED1' where id = 'ord_red1'`)

	f.order(t, "ord_unpaid", "ev_red1", "buyer", "pending_payment", "tt_red1", 75000, 1, "2026-11-19 10:00:00")
	f.Exec(t, `update "order" set barcode = 'E-QR-UNPAID' where id = 'ord_unpaid'`)

	f.event(t, "ev_red2", "org2")
	f.ticket(t, "tt_red2", "ev_red2", 50000, 10, 1, 0)
	f.order(t, "ord_red2", "ev_red2", "buyer", "paid", "tt_red2", 50000, 1, "2026-11-19 10:00:00")
	f.Exec(t, `update "order" set barcode = 'E-QR-RED2' where id = 'ord_red2'`)

	// Generate batch of physical tickets in ev_red1
	f.Exec(t, `insert into physical_batch(id, event_id, request_id, payload_hash, name, access_type, quantity, generated_by, generated_at)
		values('pb1', 'ev_red1', 'req-pb1', 'hash1', 'Batch 1', 'DAY_1', 10, 'owner', now())`)
	for i := 1; i <= 5; i++ {
		code := fmt.Sprintf("HXP-CODE-%04d", i)
		serial := fmt.Sprintf("%04d", i)
		ptID := fmt.Sprintf("pt-%d", i)
		f.Exec(t, `insert into physical_ticket(id, batch_id, event_id, serial_number, code, access_type, validity_status, assignment_status, created_at)
			values($1, 'pb1', 'ev_red1', $2, $3, 'DAY_1', 'STOCK', 'UNASSIGNED', now())`, ptID, serial, code)
	}

	// 1. Volunteer without accepted crew is blocked
	t.Run("unassigned volunteer is forbidden from lookup", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/lookup-eticket", map[string]any{"code": "E-QR-RED1"}, vol)
		expect(t, rec, 403, "")
	})

	// Assign volunteer to ev_red1
	f.Exec(t, `insert into event_crew(id, event_id, email, user_id, role, status, token, invited_by, expires_at, created_at, accepted_at)
		values('crew-red', 'ev_red1', 'vol_red@example.com', 'vol_red', 'volunteer', 'accepted', 'tok-red', 'owner', now() + interval '7 days', now(), now())`)

	t.Run("lookup unpaid order returns ORDER_NOT_PAID", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/lookup-eticket", map[string]any{"code": "E-QR-UNPAID"}, vol)
		expect(t, rec, 400, "")
		res := decode(t, rec)
		if res["result"] != "ORDER_NOT_PAID" {
			t.Fatalf("expected ORDER_NOT_PAID, got %+v", res)
		}
	})

	t.Run("lookup order from other org returns FORBIDDEN", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/lookup-eticket", map[string]any{"code": "E-QR-RED2"}, vol)
		expect(t, rec, 403, "")
	})

	var orderItemID string
	t.Run("lookup valid paid e-ticket returns breakdown", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/lookup-eticket", map[string]any{"code": "E-QR-RED1"}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "LOOKUP_OK" {
			t.Fatalf("expected LOOKUP_OK, got %+v", res)
		}
		summary := res["summary"].(map[string]any)
		if summary["totalBought"] != float64(3) || summary["totalRemaining"] != float64(3) || summary["totalRedeemed"] != float64(0) {
			t.Fatalf("unexpected summary: %+v", summary)
		}
		items := res["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("expected 1 order item, got %d", len(items))
		}
		firstItem := items[0].(map[string]any)
		orderItemID = firstItem["orderItemId"].(string)
	})

	t.Run("partial redeem 1 ticket linked to physical wristband", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/redeem", map[string]any{
			"requestId": "req-redeem-1",
			"eventId":   "ev_red1",
			"orderId":   "ord_red1",
			"mode":      "LINKED",
			"items": []map[string]any{
				{
					"orderItemId":   orderItemID,
					"quantity":      1,
					"physicalCodes": []string{"HXP-CODE-0001"},
				},
			},
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "REDEEM_OK" || res["redeemedCount"] != float64(1) {
			t.Fatalf("expected REDEEM_OK with 1 ticket, got %+v", res)
		}

		// Verify physical ticket is now ACTIVE
		status := f.scalar(t, `select validity_status from physical_ticket where code = 'HXP-CODE-0001'`).(string)
		if status != "ACTIVE" {
			t.Fatalf("expected ACTIVE physical ticket, got %s", status)
		}

		// Verify remaining is now 2
		recLookup := f.do(t, "POST", "/api/scanner/lookup-eticket", map[string]any{"code": "E-QR-RED1"}, vol)
		expect(t, recLookup, 200, "")
		resLookup := decode(t, recLookup)
		summary := resLookup["summary"].(map[string]any)
		if summary["totalRemaining"] != float64(2) || summary["totalRedeemed"] != float64(1) {
			t.Fatalf("expected 2 remaining and 1 redeemed, got %+v", summary)
		}
	})

	t.Run("cannot redeem with already active physical code", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/redeem", map[string]any{
			"requestId": "req-redeem-dup",
			"eventId":   "ev_red1",
			"orderId":   "ord_red1",
			"mode":      "LINKED",
			"items": []map[string]any{
				{
					"orderItemId":   orderItemID,
					"quantity":      1,
					"physicalCodes": []string{"HXP-CODE-0001"}, // Already ACTIVE
				},
			},
		}, vol)
		expect(t, rec, 400, "")
	})

	t.Run("idempotent replay of same requestId returns identical response", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/redeem", map[string]any{
			"requestId": "req-redeem-1",
			"eventId":   "ev_red1",
			"orderId":   "ord_red1",
			"mode":      "LINKED",
			"items": []map[string]any{
				{
					"orderItemId":   orderItemID,
					"quantity":      1,
					"physicalCodes": []string{"HXP-CODE-0001"},
				},
			},
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "REDEEM_OK" {
			t.Fatalf("expected idempotent replay, got %+v", res)
		}
	})

	t.Run("OTS physical ticket activation", func(t *testing.T) {
		rec := f.do(t, "POST", "/api/scanner/ots", map[string]any{
			"requestId":     "req-ots-1",
			"eventId":       "ev_red1",
			"customerName":  "Budi Customer",
			"customerPhone": "08123456789",
			"paymentRef":    "CASH-KASIR-01",
			"physicalCodes": []string{"HXP-CODE-0002", "HXP-CODE-0003"},
		}, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		if res["result"] != "OTS_OK" || res["activatedCount"] != float64(2) {
			t.Fatalf("expected OTS_OK with 2 tickets, got %+v", res)
		}

		// Verify both tickets are now ACTIVE with ots_reference
		ref2 := f.scalar(t, `select ots_reference from physical_ticket where code = 'HXP-CODE-0002'`).(string)
		if ref2 != "CASH-KASIR-01" {
			t.Fatalf("expected CASH-KASIR-01, got %s", ref2)
		}

		// Trying to activate HXP-CODE-0002 again in OTS must fail
		recDup := f.do(t, "POST", "/api/scanner/ots", map[string]any{
			"requestId":     "req-ots-2",
			"eventId":       "ev_red1",
			"paymentRef":    "CASH-KASIR-02",
			"physicalCodes": []string{"HXP-CODE-0002"},
		}, vol)
		expect(t, recDup, 400, "")
	})

	t.Run("list redeemed and unredeemed e-tickets returns scannerName and stats", func(t *testing.T) {
		f.order(t, "ord_red_wait", "ev_red1", "buyer", "paid", "tt_red1", 100000, 1, "2026-11-19 11:00:00")

		rec := f.do(t, "GET", "/api/scanner/etickets?eventId=ev_red1", nil, vol)
		expect(t, rec, 200, "")
		res := decode(t, rec)
		stats := res["stats"].(map[string]any)
		if stats["totalRedeemed"] != float64(1) {
			t.Fatalf("expected totalRedeemed=1, got %+v", stats)
		}
		items := res["etickets"].([]any)
		if len(items) != 2 {
			t.Fatalf("expected 2 paid etickets (1 redeemed, 1 unredeemed), got %d", len(items))
		}
		for _, it := range items {
			row := it.(map[string]any)
			if row["orderId"] == "ord_red1" && row["scannerName"] != "vol_red" {
				t.Fatalf("expected scannerName vol_red for ord_red1, got %v", row["scannerName"])
			}
			if row["orderId"] == "ord_red_wait" && row["redeemedQty"] != float64(0) {
				t.Fatalf("expected redeemedQty=0 for ord_red_wait, got %v", row["redeemedQty"])
			}
		}
	})
}

package organizer

import (
	"testing"
)

func TestListTickets(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t2", "e1", 500, 5, 0, 1)
	f.ticket(t, "t1", "e1", 1000, 10, 2, 0)
	f.ticket(t, "tx", "x1", 1000, 10, 0, 0)
	authCases(t, f, "GET", "/api/events/e1/tickets", nil)
	expect(t, f.do(t, "GET", "/api/events/x1/tickets", nil, f.owner), 404, errBody("Not found"))
	got := ok200(t, f.do(t, "GET", "/api/events/e1/tickets", nil, f.member), "events-{id}-tickets.json")
	tk := got["tickets"].([]any)
	if len(tk) != 2 || tk[0].(map[string]any)["id"] != "t1" || tk[0].(map[string]any)["sold"] != 2.0 {
		t.Fatalf("tickets %v", tk)
	}
}

func TestCreateTicket(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	valid := map[string]any{"name": "VIP", "price": 0, "quantity": 5, "benefits": []string{"Merch"}, "displayOrder": 2}
	authCases(t, f, "POST", "/api/events/e1/tickets", valid)
	expect(t, f.do(t, "POST", "/api/events/x1/tickets", valid, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "POST", "/api/events/e1/tickets", valid, f.member), 403, errBody("Forbidden"))
	for _, c := range []struct {
		body map[string]any
		msg  string
	}{
		{map[string]any{"price": 1, "quantity": 1}, "Name is required"},
		{map[string]any{"name": "a", "quantity": 1}, "Valid price is required"},
		{map[string]any{"name": "a", "price": -1, "quantity": 1}, "Valid price is required"},
		{map[string]any{"name": "a", "price": 1}, "Quantity must be at least 1"},
		{map[string]any{"name": "a", "price": 1, "quantity": 0}, "Quantity must be at least 1"},
	} {
		expect(t, f.do(t, "POST", "/api/events/e1/tickets", c.body, f.owner), 400, errBody(c.msg))
	}
	rec := f.do(t, "POST", "/api/events/e1/tickets", valid, f.admin)
	expect(t, rec, 201, "")
	tk := decode(t, rec)["ticket"].(map[string]any)
	assertShape(t, goldenPart(t, "events-{id}-tickets.json", "tickets", 0), tk)
	if tk["name"] != "VIP" || tk["sold"] != 0.0 || tk["displayOrder"] != 2.0 || tk["benefits"].([]any)[0] != "Merch" {
		t.Fatalf("ticket %v", tk)
	}
	rec = f.do(t, "POST", "/api/events/e1/tickets", map[string]any{"name": "B", "price": 1, "quantity": 1, "benefits": "x", "displayOrder": "3"}, f.admin)
	expect(t, rec, 201, "")
	if tk := decode(t, rec)["ticket"].(map[string]any); len(tk["benefits"].([]any)) != 0 || tk["displayOrder"] != 0.0 {
		t.Fatalf("defaults %v", tk)
	}
}

func TestUpdateTicket(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 10, 4, 0)
	f.ticket(t, "tx", "x1", 1000, 10, 0, 0)
	authCases(t, f, "PATCH", "/api/events/e1/tickets/t1", map[string]any{})
	expect(t, f.do(t, "PATCH", "/api/events/e1/tickets/t1", map[string]any{}, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "PATCH", "/api/events/x1/tickets/tx", map[string]any{"price": 1}, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "PATCH", "/api/events/e2/tickets/t1", map[string]any{"price": 1}, f.owner), 404, errBody("Ticket not found"))

	got := ok200(t, f.do(t, "PATCH", "/api/events/e1/tickets/t1", map[string]any{
		"name": "Reg", "price": 2000, "quantity": 4, "benefits": []string{}, "displayOrder": 3,
	}, f.admin), "")
	tk := got["ticket"].(map[string]any)
	assertShape(t, goldenPart(t, "events-{id}-tickets.json", "tickets", 0), tk)
	if tk["name"] != "Reg" || tk["price"] != 2000.0 || tk["quantity"] != 4.0 || tk["displayOrder"] != 3.0 {
		t.Fatalf("ticket %v", tk)
	}
	expect(t, f.do(t, "PATCH", "/api/events/e1/tickets/t1", map[string]any{"quantity": 3}, f.owner), 409, errBody(msgQuantityBelowSold))
	if f.scalar(t, `select quantity from ticket_type where id='t1'`) != int32(4) {
		t.Fatal("quantity changed")
	}
	expect(t, f.do(t, "PATCH", "/api/events/e1/tickets/t1", map[string]any{"price": -5}, f.owner), 400, errBody("Valid price is required"))
	expect(t, f.do(t, "PATCH", "/api/events/e1/tickets/t1", map[string]any{"quantity": 1.5}, f.owner), 400, errBody("Quantity must be at least 1"))
}

func TestDeleteTicket(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 10, 0, 0)
	f.ticket(t, "t2", "e1", 1000, 10, 0, 1)
	f.ticket(t, "tx", "x1", 1000, 10, 0, 0)
	f.order(t, "o1", "e1", "buyer", "expired", "t2", 1000, 1, "now")
	authCases(t, f, "DELETE", "/api/events/e1/tickets/t1", nil)
	expect(t, f.do(t, "DELETE", "/api/events/e1/tickets/t1", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "DELETE", "/api/events/x1/tickets/tx", nil, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "DELETE", "/api/events/e1/tickets/tx", nil, f.owner), 200, `{"ok":true}`)
	if f.scalar(t, `select count(*) from ticket_type where id='tx'`) != int64(1) {
		t.Fatal("other org's ticket deleted")
	}
	expect(t, f.do(t, "DELETE", "/api/events/e1/tickets/t2", nil, f.owner), 409, errBody(msgTicketHasOrders))
	expect(t, f.do(t, "DELETE", "/api/events/e1/tickets/t1", nil, f.admin), 200, `{"ok":true}`)
	if f.scalar(t, `select count(*) from ticket_type where event_id='e1'`) != int64(1) {
		t.Fatal("not deleted")
	}
}

package admin

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func (e *env) seedPayout(t *testing.T, id, org, status string, amount int, age string) {
	t.Helper()
	e.Exec(t, `insert into payout_request(id,organization_id,amount,bank_name,account_number,account_holder_name,status,created_at)
		values($1,$2,$3,'BNI','123','Holder',$4,now()-$5::interval)`, id, org, amount, status, age)
}

func TestFormatIDR(t *testing.T) {
	for n, want := range map[int64]string{0: "Rp 0", 1250000: "Rp 1.250.000", 999: "Rp 999", -5000: "-Rp 5.000"} {
		if got := formatIDR(n); got != want {
			t.Errorf("%d: %q want %q", n, got, want)
		}
	}
}

func TestListPayouts(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/payouts", nil)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedPayout(t, "p1", "o1", "processed", 1000, "3 days")
	e.seedPayout(t, "p2", "o1", "pending", 2000, "1 day")
	e.seedPayout(t, "p3", "o1", "pending", 3000, "2 days")
	e.seedPayout(t, "p4", "o1", "rejected", 4000, "4 days")

	rec := e.do(t, "GET", "/api/payouts", nil, e.staff)
	assertGolden(t, "payouts.json", rec)
	p := decode[page[payoutRow]](t, rec)
	var ids []string
	for _, r := range p.Data {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "p3,p2,p1,p4" || p.Total != 4 || p.Data[0].OrgName != "Org One" || p.Data[0].Amount != 3000 {
		t.Fatalf("payouts %+v", p)
	}
	if p = decode[page[payoutRow]](t, e.do(t, "GET", "/api/payouts?status=pending", nil, e.staff)); p.Total != 2 {
		t.Fatalf("filtered %+v", p)
	}
	want(t, e.do(t, "GET", "/api/payouts?status=done", nil, e.staff), 400, `{"error":"Invalid status"}`)
}

func TestGetPayout(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/payouts/p1", nil)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedPayout(t, "p1", "o1", "pending", 1000, "1 day")
	e.seedPayout(t, "p2", "o1", "processed", 1000, "1 day")
	e.Exec(t, `update payout_request set processed_by='adm', processed_at=now(), proof_url='/api/files/x.png' where id='p2'`)

	rec := e.do(t, "GET", "/api/payouts/p1", nil, e.staff)
	assertGolden(t, "payouts-{id}.json", rec)
	if !strings.Contains(rec.Body.String(), `"processedAt":null,"rejectionReason":null,"proofUrl":null,"processedBy":null,"processedByName":null}`) {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(t, "GET", "/api/payouts/p2", nil, e.staff)
	assertGolden(t, "payouts-{id}.json", rec)
	if !strings.Contains(rec.Body.String(), `"proofUrl":"/api/files/x.png","processedBy":"adm","processedByName":"adm"}`) {
		t.Fatal(rec.Body.String())
	}
	want(t, e.do(t, "GET", "/api/payouts/nope", nil, e.staff), 404, `{"error":"Not found"}`)
}

func TestProcessPayout(t *testing.T) {
	e := setup(t)
	body := map[string]any{"proofUrl": "https://f/r.png"}
	e.guarded(t, "POST", "/api/payouts/p1/process", body, e.staff)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedMember(t, "o1", "stf", "member")
	e.seedMember(t, "o1", "usr", "owner")
	e.seedPayout(t, "p1", "o1", "pending", 1250000, "1 day")

	for _, b := range []any{map[string]any{}, map[string]any{"proofUrl": ""}, map[string]any{"proofUrl": 5}} {
		want(t, e.do(t, "POST", "/api/payouts/p1/process", b, e.admin), 400, `{"error":"Transfer proof is required"}`)
	}
	for _, u := range []string{"javascript:alert(1)", "http://f/r.png", "//f/r.png", "r.png"} {
		want(t, e.do(t, "POST", "/api/payouts/p1/process", map[string]any{"proofUrl": u}, e.admin), 400, `{"error":"Invalid transfer proof URL"}`)
	}
	want(t, e.do(t, "POST", "/api/payouts/p1/process", body, e.admin), 200, `{"ok":true}`)
	var status, by, proof string
	var hasAt bool
	if err := e.DB.QueryRow(t.Context(), `select status, processed_by, proof_url, processed_at is not null from payout_request where id='p1'`).Scan(&status, &by, &proof, &hasAt); err != nil {
		t.Fatal(err)
	}
	if status != "processed" || by != "adm" || proof != "https://f/r.png" || !hasAt {
		t.Fatalf("%s %s %s %v", status, by, proof, hasAt)
	}
	msgs := e.mail.all()
	if len(msgs) != 1 || msgs[0].To != "usr@example.com" || msgs[0].Subject != "Your payout of Rp 1.250.000 has been sent" ||
		!strings.Contains(msgs[0].Text, "Org One") || !strings.Contains(msgs[0].Text, "https://f/r.png") {
		t.Fatalf("mail %+v", msgs)
	}

	want(t, e.do(t, "POST", "/api/payouts/p1/process", body, e.super), 409, `{"error":"This payout is no longer pending"}`)
	want(t, e.do(t, "POST", "/api/payouts/p1/reject", map[string]any{"reason": "x"}, e.super), 409, `{"error":"This payout is no longer pending"}`)
	want(t, e.do(t, "POST", "/api/payouts/nope/process", body, e.super), 409, `{"error":"This payout is no longer pending"}`)
	if len(e.mail.all()) != 1 {
		t.Fatal("extra mail")
	}
}

func TestProcessPayoutConcurrent(t *testing.T) {
	e := setup(t)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedMember(t, "o1", "usr", "owner")
	e.seedPayout(t, "p1", "o1", "pending", 1000, "1 day")
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Go(func() {
			path, body := "/api/payouts/p1/process", any(map[string]any{"proofUrl": "/uploads/payout-proofs/u.png"})
			if i%2 == 1 {
				path, body = "/api/payouts/p1/reject", map[string]any{"reason": "r"}
			}
			codes[i] = e.do(t, "POST", path, body, e.admin).Code
		})
	}
	wg.Wait()
	n := 0
	for _, c := range codes {
		if c == 200 {
			n++
		}
	}
	if n != 1 || len(e.mail.all()) != 1 {
		t.Fatalf("codes %v, mails %d", codes, len(e.mail.all()))
	}
}

func TestRejectPayout(t *testing.T) {
	e := setup(t)
	body := map[string]any{"reason": "Wrong account"}
	e.guarded(t, "POST", "/api/payouts/p1/reject", body, e.staff)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedMember(t, "o1", "stf", "member")
	e.seedPayout(t, "p1", "o1", "pending", 5000, "1 day")
	e.mail.err = errors.New("resend down")

	for _, b := range []any{map[string]any{}, map[string]any{"reason": "  \n "}, map[string]any{"reason": true}} {
		want(t, e.do(t, "POST", "/api/payouts/p1/reject", b, e.admin), 400, `{"error":"A rejection reason is required"}`)
	}
	want(t, e.do(t, "POST", "/api/payouts/p1/reject", map[string]any{"reason": "  Wrong account "}, e.super), 200, `{"ok":true}`)
	var status, reason, by string
	var hasAt bool
	if err := e.DB.QueryRow(t.Context(), `select status, rejection_reason, processed_by, processed_at is not null from payout_request where id='p1'`).Scan(&status, &reason, &by, &hasAt); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || reason != "Wrong account" || by != "super" || hasAt {
		t.Fatalf("%s %q %s %v", status, reason, by, hasAt)
	}
	msgs := e.mail.all()
	if len(msgs) != 1 || msgs[0].To != "stf@example.com" || msgs[0].Subject != "We couldn't process your payout" ||
		!strings.Contains(msgs[0].Text, "Reason: Wrong account\n") || !strings.Contains(msgs[0].Text, "Rp 5.000 for Org One") {
		t.Fatalf("mail %+v", msgs)
	}
	want(t, e.do(t, "POST", "/api/payouts/p1/reject", body, e.super), 409, `{"error":"This payout is no longer pending"}`)

	e.seedOrg(t, "o2", "Lonely", "active")
	e.seedPayout(t, "p2", "o2", "pending", 5000, "1 day")
	want(t, e.do(t, "POST", "/api/payouts/p2/reject", body, e.super), 200, `{"ok":true}`)
	if len(e.mail.all()) != 1 {
		t.Fatal("unexpected mail")
	}
}

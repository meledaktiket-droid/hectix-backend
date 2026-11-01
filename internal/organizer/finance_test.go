package organizer

import (
	"sync"
	"testing"
)

func TestLocaleID(t *testing.T) {
	for in, want := range map[float64]string{0: "0", 50000: "50.000", 1234567: "1.234.567", 999: "999", 1500.5: "1.500,5", 0.7: "0,7"} {
		if got := localeID(in); got != want {
			t.Errorf("%v: %q want %q", in, got, want)
		}
	}
}

const bankJSON = `{"ktpNumber":"123","bankName":"BNI","accountNumber":"0001","accountHolderName":"Owner"}`

func (f *fx) revenue(t *testing.T, amount int) {
	t.Helper()
	f.event(t, "e1", "org1")
	f.ticket(t, "t1", "e1", amount, 10, 1, 0)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", amount, 1, "now")
}

func TestFinance(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/finance", nil)
	f.revenue(t, 1_000_000)
	f.event(t, "x1", "org2")
	f.ticket(t, "tx", "x1", 5000, 10, 1, 0)
	f.order(t, "ox", "x1", "buyer", "paid", "tx", 5000, 1, "now")
	f.Exec(t, `insert into site_setting(key,value,updated_at) values('platform_fee','7000',now())`)
	f.Exec(t, `insert into payout_request(id,organization_id,amount,platform_fee,bank_name,account_number,account_holder_name,status,created_at) values
		('p1','org1',93000,7000,'BNI','1','O','processed','2026-01-01'),
		('p2','org1',50000,7000,'BNI','1','O','rejected','2026-01-02'),
		('p3','org2',1,1,'BNI','1','O','processed','2026-01-02')`)
	got := ok200(t, f.do(t, "GET", "/api/finance", nil, f.member), "finance.json")
	if got["availableBalance"] != 900000.0 || got["platformFee"] != 7000.0 || got["minPayout"] != 50000.0 {
		t.Fatalf("finance %v", got)
	}
	if h := got["history"].([]any); len(h) != 2 || h[0].(map[string]any)["id"] != "p1" {
		t.Fatalf("history %v", h)
	}
}

func TestRequestPayout(t *testing.T) {
	f := setup(t)
	authCases(t, f, "POST", "/api/finance", map[string]any{"amount": 60000})
	f.revenue(t, 100_000)
	f.Exec(t, `insert into site_setting(key,value,updated_at) values('platform_fee','7000',now())`)

	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": 60000}, f.member), 403, errBody("Forbidden"))
	f.Exec(t, `update organization set status = 'review' where id = 'org1'`)
	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": 60000}, f.owner), 403, errBody(msgOrgNotVerified))
	f.Exec(t, `update organization set status = 'active' where id = 'org1'`)
	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": 60000}, f.admin), 400, errBody("Bank account not set up"))
	f.Exec(t, `update organization set verification_data = $1 where id = 'org1'`, bankJSON)
	for _, c := range []struct {
		body any
		msg  string
	}{
		{"not json", "Jumlah tidak valid"},
		{map[string]any{}, "Jumlah tidak valid"},
		{map[string]any{"amount": 60000.5}, "Jumlah tidak valid"},
		{map[string]any{"amount": -1}, "Jumlah tidak valid"},
		{map[string]any{"amount": 49999}, "Minimal pencairan Rp 50.000"},
		{map[string]any{"amount": 100001}, "Jumlah melebihi saldo tersedia"},
	} {
		expect(t, f.do(t, "POST", "/api/finance", c.body, f.admin), 400, errBody(c.msg))
	}
	f.Exec(t, `insert into site_setting(key,value,updated_at) values('min_payout','1000',now())`)
	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": 7000}, f.owner), 400, errBody("Jumlah harus lebih besar dari biaya platform"))

	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": "100000"}, f.owner), 200, `{"ok":true}`)
	if f.scalar(t, `select amount || ':' || platform_fee || ':' || bank_name || ':' || account_number || ':' || account_holder_name || ':' || status
		from payout_request where organization_id = 'org1'`) != "93000:7000:BNI:0001:Owner:pending" {
		t.Fatal("payout row wrong")
	}
	expect(t, f.do(t, "POST", "/api/finance", map[string]any{"amount": 60000}, f.owner), 409, errBody("A payout request is already pending"))
}

func TestRequestPayoutConcurrent(t *testing.T) {
	f := setup(t)
	f.revenue(t, 100_000)
	f.Exec(t, `update organization set verification_data = $1 where id = 'org1'`, bankJSON)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.do(t, "POST", "/api/finance", map[string]any{"amount": 100000}, f.owner).Code
		}()
	}
	wg.Wait()
	okCount := 0
	for _, c := range codes {
		if c == 200 {
			okCount++
		} else if c != 409 {
			t.Fatalf("codes %v", codes)
		}
	}
	if okCount != 1 || f.scalar(t, `select count(*) from payout_request`) != int64(1) {
		t.Fatalf("codes %v", codes)
	}
}

func TestPayoutAccount(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/org/payout", nil)
	expect(t, f.do(t, "GET", "/api/org/payout", nil, f.admin), 403, errBody("Forbidden"))
	expect(t, f.do(t, "PATCH", "/api/org/payout", map[string]any{"bankName": "X"}, f.admin), 403, errBody("Forbidden"))
	expect(t, f.do(t, "GET", "/api/org/payout", nil, f.owner), 200, `{"bankName":"","accountNumber":"","accountHolderName":""}`)

	f.Exec(t, `update organization set verification_data = $1 where id = 'org1'`, `{"ktpNumber":"123","bankName":"BNI","accountNumber":42}`)
	ok200(t, f.do(t, "GET", "/api/org/payout", nil, f.owner), "org-payout.json")
	expect(t, f.do(t, "GET", "/api/org/payout", nil, f.owner), 200, `{"bankName":"BNI","accountNumber":"","accountHolderName":""}`)

	expect(t, f.do(t, "PATCH", "/api/org/payout", map[string]any{"bankName": nil, "accountNumber": "0001", "accountHolderName": "A & B"}, f.owner), 200, `{"ok":true}`)
	if got := f.scalar(t, `select verification_data from organization where id='org1'`); got != `{"ktpNumber":"123","bankName":"BNI","accountNumber":"0001","accountHolderName":"A & B"}` {
		t.Fatalf("verification_data %v", got)
	}
	for _, c := range []struct {
		body map[string]any
		msg  string
	}{
		{map[string]any{"bankName": 5}, "Invalid bankName"},
		{map[string]any{"accountNumber": "   "}, "Invalid accountNumber"},
		{map[string]any{"accountHolderName": []string{"x"}}, "Invalid accountHolderName"},
	} {
		expect(t, f.do(t, "PATCH", "/api/org/payout", c.body, f.owner), 400, errBody(c.msg))
	}
	expect(t, f.do(t, "PATCH", "/api/org/payout", map[string]any{"bankName": "  Mandiri "}, f.owner), 200, `{"ok":true}`)
	if got := f.scalar(t, `select verification_data::json->>'bankName' from organization where id='org1'`); got != "Mandiri" {
		t.Fatalf("bankName %v", got)
	}
	f.Exec(t, `update organization set verification_data = null where id = 'org2'`)
	expect(t, f.do(t, "PATCH", "/api/org/payout", map[string]any{"bankName": "BCA"}, f.other), 200, `{"ok":true}`)
	if got := f.scalar(t, `select verification_data from organization where id='org2'`); got != `{"bankName":"BCA"}` {
		t.Fatalf("org2 verification_data %v", got)
	}
}

package web

import (
	"testing"
)

func TestValidateVoucher(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into voucher(id,event_id,code,type,amount,usage_limit,usage_count,expires_at,deleted_at,created_at) values
		('v1','e1','HEMAT','percent',10,null,5,null,null,now()),
		('v2','e1','FLAT','fixed',50000,0,9,now()+interval '1 day',null,now()),
		('v3','e1','OLD','percent',10,null,0,now()-interval '1 minute',null,now()),
		('v4','e1','FULL','percent',10,3,3,null,null,now()),
		('v5','e1','DEL','percent',10,null,0,null,now(),now()),
		('v6','e1','BIG','fixed',1500000,2,1,null,null,now())`)

	cases := []struct {
		body any
		want string
	}{
		{map[string]any{"eventId": "e1", "code": "  "}, `{"message":"Kode tidak valid","valid":false}`},
		{map[string]any{"code": "HEMAT"}, `{"message":"Kode tidak valid","valid":false}`},
		{map[string]any{"eventId": "e1", "code": "NOPE"}, `{"message":"Kode tidak ditemukan","valid":false}`},
		{map[string]any{"eventId": "e1", "code": "DEL"}, `{"message":"Kode tidak ditemukan","valid":false}`},
		{map[string]any{"eventId": "other", "code": "HEMAT"}, `{"message":"Kode tidak ditemukan","valid":false}`},
		{map[string]any{"eventId": "e1", "code": "old"}, `{"message":"Kode sudah kedaluwarsa","valid":false}`},
		{map[string]any{"eventId": "e1", "code": "FULL"}, `{"message":"Kode sudah habis dipakai","valid":false}`},
		{map[string]any{"eventId": "e1", "code": " hemat ", "subtotal": 125005},
			`{"amount":10,"code":"HEMAT","discount":12501,"message":"Voucher HEMAT diterapkan (10%)","type":"percent","valid":true}`},
		{map[string]any{"eventId": "e1", "code": "FLAT", "subtotal": 200000},
			`{"amount":50000,"code":"FLAT","discount":50000,"message":"Voucher FLAT diterapkan (Rp 50.000)","type":"fixed","valid":true}`},
		{map[string]any{"eventId": "e1", "code": "BIG", "subtotal": 1250000},
			`{"amount":1500000,"code":"BIG","discount":1250000,"message":"Voucher BIG diterapkan (Rp 1.250.000)","type":"fixed","valid":true}`},
	}
	for _, c := range cases {
		rec := env.Do(t, "POST", "/api/vouchers/validate", c.body, nil)
		if rec.Code != 200 || rec.Body.String() != c.want+"\n" {
			t.Errorf("%v: got %d %s", c.body, rec.Code, rec.Body)
		}
	}
	assertError(t, env.Do(t, "POST", "/api/vouchers/validate", "{", nil), 400, "Invalid body")
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1.000", 1234567: "1.234.567"} {
		if got := formatIDR(n); got != want {
			t.Errorf("formatIDR(%d)=%s", n, got)
		}
	}
}

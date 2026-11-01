package settings

import (
	"context"
	"encoding/json"
	"testing"

	"hectix/backend/internal/golden"
	"hectix/backend/internal/testdb"
)

func TestDefaults(t *testing.T) {
	db := testdb.New(t)
	s, err := Get(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if s != Defaults {
		t.Fatalf("%+v", s)
	}
	b, _ := json.Marshal(s)
	want := `{"whatsappNumber":"","contactEmail":"","instagramUrl":"","tiktokUrl":"","serviceFee":7120,"platformFee":0,"minPayout":50000,"taxPercent":10,"midtransVaFee":4000,"midtransQrisPercent":0.7}`
	if string(b) != want {
		t.Fatalf("got %s", b)
	}
	golden.Assert(t, "../../testdata/golden/web/routes/config.json", b)
}

func TestOverridesAndFallbacks(t *testing.T) {
	db := testdb.New(t)
	_, err := db.Exec(context.Background(), `insert into site_setting(key,value,updated_at) values
		('whatsapp_number','620000000000',now()),
		('contact_email','',now()),
		('service_fee',' 4000 ',now()),
		('platform_fee','1e3',now()),
		('tax_percent','abc',now()),
		('min_payout','   ',now()),
		('midtrans_va_fee','Infinity',now()),
		('midtrans_qris_percent','0.85',now())`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Get(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults
	want.WhatsappNumber = "620000000000"
	want.ServiceFee = 4000
	want.PlatformFee = 1000
	want.MidtransQrisPercent = 0.85
	if s != want {
		t.Fatalf("got  %+v\nwant %+v", s, want)
	}
}

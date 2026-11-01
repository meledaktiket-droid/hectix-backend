package eticket

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/mail"
	"hectix/backend/internal/testdb"
)

type sent struct {
	to, subject, html, text string
	att                     []mail.Attachment
}

type fakeMail struct {
	calls []sent
	err   error
}

func (f *fakeMail) Send(ctx context.Context, to, subject, html, text string, att ...mail.Attachment) error {
	f.calls = append(f.calls, sent{to, subject, html, text, att})
	return f.err
}

func exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func seedBase(t *testing.T, db *pgxpool.Pool) {
	exec(t, db, `insert into "user"(id,name,email,email_verified,role,created_at,updated_at) values('u1','Siti Rahmawati','siti@example.com',true,'user',now(),now())`)
	exec(t, db, `insert into organization(id,name,created_at) values('o1','Org',now())`)
	exec(t, db, `insert into event(id,organization_id,name,venue_name,city,banner_url,starts_at,created_at,updated_at)
		values('e1','o1','Konser Senja','Gelora Bung Karno','Jakarta Pusat','https://dev.hectix.id/uploads/e1.png','2026-11-14 12:30:00',now(),now())`)
	exec(t, db, `insert into ticket_type(id,event_id,name,price,quantity,sold,created_at,updated_at) values('tt1','e1','Reguler',100000,10,3,now(),now())`)
}

func seedOrder(t *testing.T, db *pgxpool.Pool, id string, ref, barcode any, sendEticket bool) {
	t.Helper()
	exec(t, db, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,
		reference_number,barcode,send_eticket,expires_at,created_at,updated_at)
		values($1,'u1','e1','paid','qris',300000,30000,7120,337120,$2,$3,$4,now(),'2026-10-01 18:15:00',now())`,
		id, ref, barcode, sendEticket)
	exec(t, db, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1,$2,'tt1','Reguler',100000,2)`, id+"-a", id)
	exec(t, db, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1,$2,'tt1','Reguler',100000,1)`, id+"-b", id)
}

func stamped(t *testing.T, db *pgxpool.Pool, id string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(context.Background(), `select eticket_sent_at is not null from "order" where id=$1`, id).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func payload(id string) []byte { return []byte(`{"orderId":"` + id + `"}`) }

func TestHandle(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	seedBase(t, db)

	t.Run("sends once and stamps", func(t *testing.T) {
		seedOrder(t, db, "ord1", "HX-2026-0914-2345", "dGVzdC1iYXJjb2Rl", true)
		fm := &fakeMail{}
		var bannerURLs []string
		s := &Sender{DB: db, Mail: fm, Banners: func(u string) ([]byte, error) {
			bannerURLs = append(bannerURLs, u)
			return nil, errors.New("none")
		}}
		if err := s.Handle(ctx, payload("ord1")); err != nil {
			t.Fatal(err)
		}
		if len(bannerURLs) != 1 || bannerURLs[0] != "https://dev.hectix.id/uploads/e1.png" {
			t.Fatalf("banner loader got %v", bannerURLs)
		}
		if len(fm.calls) != 1 {
			t.Fatalf("calls %d", len(fm.calls))
		}
		c := fm.calls[0]
		want := mail.Eticket(mail.EticketData{EventName: "Konser Senja", ReferenceNumber: "HX-2026-0914-2345", Quantity: 3, Total: 337120})
		if c.to != "siti@example.com" || c.subject != want.Subject || c.html != want.HTML || c.text != want.Text {
			t.Fatalf("message %+v", c)
		}
		if len(c.att) != 1 {
			t.Fatalf("attachments %d", len(c.att))
		}
		a := c.att[0]
		if a.Filename != "hectix-ticket-HX-2026-0914-2345.pdf" || a.ContentType != "application/pdf" || !bytes.HasPrefix(a.Content, []byte("%PDF")) {
			t.Fatalf("attachment %q %q %q", a.Filename, a.ContentType, a.Content[:min(8, len(a.Content))])
		}
		if !stamped(t, db, "ord1") {
			t.Fatal("eticket_sent_at not stamped")
		}

		if err := s.Handle(ctx, payload("ord1")); err != nil {
			t.Fatal(err)
		}
		if len(fm.calls) != 1 {
			t.Fatalf("second call sent again: %d", len(fm.calls))
		}
	})

	t.Run("mailer error leaves unstamped", func(t *testing.T) {
		seedOrder(t, db, "ord2", "HX-2026-0000-0002", "bc2", true)
		fm := &fakeMail{err: errors.New("resend down")}
		err := (&Sender{DB: db, Mail: fm}).Handle(ctx, payload("ord2"))
		if err == nil || !strings.Contains(err.Error(), "resend down") {
			t.Fatalf("err %v", err)
		}
		if stamped(t, db, "ord2") {
			t.Fatal("stamped despite send failure")
		}
	})

	skips := []struct {
		name, id     string
		ref, barcode any
		send         bool
	}{
		{"missing reference", "ord3", nil, "bc3", true},
		{"missing barcode", "ord4", "HX-2026-0000-0004", nil, true},
	}
	for _, c := range skips {
		t.Run(c.name, func(t *testing.T) {
			seedOrder(t, db, c.id, c.ref, c.barcode, c.send)
			fm := &fakeMail{}
			if err := (&Sender{DB: db, Mail: fm}).Handle(ctx, payload(c.id)); err != nil {
				t.Fatal(err)
			}
			if len(fm.calls) != 0 || stamped(t, db, c.id) {
				t.Fatalf("calls=%d stamped=%v", len(fm.calls), stamped(t, db, c.id))
			}
		})
	}

	t.Run("missing order skipped", func(t *testing.T) {
		fm := &fakeMail{}
		if err := (&Sender{DB: db, Mail: fm}).Handle(ctx, payload("nope")); err != nil || len(fm.calls) != 0 {
			t.Fatalf("err=%v calls=%d", err, len(fm.calls))
		}
	})

	t.Run("bad payload rejected", func(t *testing.T) {
		s := &Sender{DB: db, Mail: &fakeMail{}}
		for _, p := range []string{`{}`, `{"orderId":""}`, `not json`} {
			if err := s.Handle(ctx, []byte(p)); err == nil {
				t.Errorf("%s: want error", p)
			}
		}
	})
}

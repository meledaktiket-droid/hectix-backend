package payments

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/testdb"
)

const testServerKey = "SB-Mid-server-test"

func sig(parts ...string) string {
	h := sha512.Sum512([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(h[:])
}

func post(t *testing.T, db *pgxpool.Pool, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	(&Webhook{DB: db, ServerKey: testServerKey}).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/midtrans", strings.NewReader(body)))
	return w
}

func notification(t *testing.T, txStatus, fraud string) string {
	t.Helper()
	m := map[string]string{
		"order_id": "ord1", "status_code": "200", "gross_amount": "205120.00",
		"transaction_status": txStatus,
		"signature_key":      sig("ord1", "200", "205120.00", testServerKey),
	}
	if fraud != "" {
		m["fraud_status"] = fraud
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func jobStatuses(t *testing.T, db *pgxpool.Pool) []string {
	t.Helper()
	rows, err := db.Query(t.Context(), `select payload->>'orderId', payload->>'status' from job where kind='order.status' order by id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			t.Fatal(err)
		}
		if id != "ord1" {
			t.Fatalf("orderId=%q", id)
		}
		out = append(out, st)
	}
	return out
}

func expectOK(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"received":true}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestWebhookStatusMapping(t *testing.T) {
	db := testdb.New(t)
	cases := []struct{ tx, fraud, want string }{
		{"settlement", "", "paid"},
		{"capture", "accept", "paid"},
		{"capture", "challenge", ""},
		{"capture", "", ""},
		{"expire", "", "expired"},
		{"cancel", "", "cancelled"},
		{"deny", "", "cancelled"},
		{"pending", "", ""},
	}
	for _, c := range cases {
		t.Run(c.tx+"/"+c.fraud, func(t *testing.T) {
			if _, err := db.Exec(t.Context(), `delete from job`); err != nil {
				t.Fatal(err)
			}
			expectOK(t, post(t, db, notification(t, c.tx, c.fraud)))
			got := jobStatuses(t, db)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("want no job, got %v", got)
				}
				return
			}
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("want [%s], got %v", c.want, got)
			}
		})
	}
}

func TestWebhookBadSignature(t *testing.T) {
	db := testdb.New(t)
	for _, body := range []string{
		`{"order_id":"ord1","status_code":"200","gross_amount":"205120.00","transaction_status":"settlement","signature_key":"deadbeef"}`,
		`{"order_id":"ord1","status_code":"200","gross_amount":"205120.00","transaction_status":"settlement"}`,
		`{"order_id":"ord1","status_code":"200","gross_amount":"205120.00","transaction_status":"settlement","signature_key":"` + sig("ord1", "200", "205120.00", "wrong-key") + `"}`,
	} {
		w := post(t, db, body)
		if w.Code != 403 || w.Body.String() != "{\"error\":\"Invalid signature\"}\n" {
			t.Fatalf("%d %q", w.Code, w.Body)
		}
	}
	if got := jobStatuses(t, db); len(got) != 0 {
		t.Fatalf("jobs=%v", got)
	}
}

func TestWebhookBadJSON(t *testing.T) {
	db := testdb.New(t)
	w := post(t, db, `{not json`)
	if w.Code != 400 || w.Body.String() != "{\"error\":\"Invalid body\"}\n" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}

func TestWebhookNumericGrossAmount(t *testing.T) {
	db := testdb.New(t)
	body := `{"order_id":"ord1","status_code":"200","gross_amount":205120.00,"transaction_status":"settlement","signature_key":"` +
		sig("ord1", "200", "205120.00", testServerKey) + `"}`
	expectOK(t, post(t, db, body))
	if got := jobStatuses(t, db); len(got) != 1 || got[0] != "paid" {
		t.Fatalf("jobs=%v", got)
	}
}

func TestWebhookEnqueueFailure(t *testing.T) {
	db := testdb.New(t)
	if _, err := db.Exec(t.Context(), `alter table job rename to job_gone`); err != nil {
		t.Fatal(err)
	}
	w := post(t, db, notification(t, "settlement", ""))
	if w.Code != 500 || w.Body.String() != "{\"error\":\"Internal server error\"}\n" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
}

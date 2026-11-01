package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkGolden(t *testing.T, name string, m Message) {
	t.Helper()
	for ext, got := range map[string]string{"subject": m.Subject, "html": m.HTML, "txt": m.Text} {
		want, err := os.ReadFile(filepath.Join("testdata", name+"."+ext))
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Errorf("%s.%s mismatch\n--- want\n%s\n--- got\n%s", name, ext, want, got)
		}
	}
}

func TestOrgInviteGolden(t *testing.T) {
	checkGolden(t, "org_invite", OrgInvite("budi@example.com", "Budi Santoso", "Konser Nusantara", "Admin", "https://organizer.hectix.id/invite/abc123"))
}

func TestPayoutProcessedGolden(t *testing.T) {
	checkGolden(t, "payout_processed_receipt", PayoutProcessed("Konser Nusantara", "Rp 1.250.000", "https://files.hectix.id/receipts/r-1.pdf"))
	checkGolden(t, "payout_processed_noreceipt", PayoutProcessed("Konser Nusantara", "Rp 1.250.000", ""))
}

func TestPayoutRejectedGolden(t *testing.T) {
	checkGolden(t, "payout_rejected", PayoutRejected("Konser Nusantara", "Rp 1.250.000", "Nama rekening tidak sesuai dengan KTP"))
}

func TestAdminInviteGolden(t *testing.T) {
	checkGolden(t, "admin_invite", AdminInvite("siti@example.com", "Support", "https://admin.hectix.id/invite/xyz789"))
}

func TestEticketGolden(t *testing.T) {
	checkGolden(t, "eticket", Eticket(EticketData{EventName: "Java Jazz Festival", ReferenceNumber: "HX-2026-0914-2345", Quantity: 3, Total: 1250000}))
}

func TestEticketFilename(t *testing.T) {
	if got := EticketFilename("HX-2026-0914-2345"); got != "hectix-ticket-HX-2026-0914-2345.pdf" {
		t.Fatal(got)
	}
}

func TestFormatIDR(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1.000", 25000: "25.000", 1250000: "1.250.000", 1234567890: "1.234.567.890", -1500: "-1.500"} {
		if got := formatThousands(n); got != want {
			t.Errorf("%d: got %q want %q", n, got, want)
		}
	}
}

func TestHTMLEscapesUserValues(t *testing.T) {
	evil := `<script>alert(1)</script>`
	msgs := []Message{
		OrgInvite("a@b.c", evil, evil, evil, "https://x.id/i"),
		PayoutProcessed(evil, evil, "https://x.id/r"),
		PayoutRejected(evil, evil, evil),
		AdminInvite(evil, evil, "https://x.id/i"),
		Eticket(EticketData{EventName: evil, ReferenceNumber: evil}),
	}
	for i, m := range msgs {
		if strings.Contains(m.HTML, "<script>") {
			t.Errorf("msg %d html not escaped:\n%s", i, m.HTML)
		}
		if !strings.Contains(m.HTML, "&lt;script&gt;") {
			t.Errorf("msg %d html missing escaped value", i)
		}
	}
	if m := OrgInvite("a@b.c", "x", "y", "z", "javascript:alert(1)"); strings.Contains(m.HTML, "javascript:") {
		t.Errorf("unsafe url kept: %s", m.HTML)
	}
	if m := OrgInvite("a@b.c", evil, "O", "R", "https://x.id"); !strings.HasPrefix(m.Subject, evil) || !strings.HasPrefix(m.Text, evil) {
		t.Errorf("subject/text should be raw: %q %q", m.Subject, m.Text)
	}
}

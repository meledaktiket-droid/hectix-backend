package ticketpdf

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

func sample() Data {
	return Data{
		EventName:       "Jakarta Night Music Festival 2026",
		EventStartsAt:   time.Date(2026, 11, 14, 12, 30, 0, 0, time.UTC),
		EventVenueName:  "Gelora Bung Karno",
		EventCity:       "Jakarta Pusat",
		BuyerName:       "Siti Rahmawati",
		TicketTypeName:  "Presale 1 (Reguler)",
		Quantity:        3,
		ReferenceNumber: "HX-2026-0914-2345",
		CreatedAt:       time.Date(2026, 10, 1, 18, 15, 0, 0, time.UTC),
		Total:           1227120,
		Barcode:         "dGVzdC1iYXJjb2RlLTEy",
	}
}

func utf16be(s string) []byte {
	var b []byte
	for _, r := range utf16.Encode([]rune(s)) {
		b = append(b, byte(r>>8), byte(r))
	}
	return b
}

func TestRenderIsPDF(t *testing.T) {
	out, err := Render(sample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("not a PDF: %q", out[:min(16, len(out))])
	}
	if p := os.Getenv("TICKETPDF_OUT"); p != "" {
		os.WriteFile(p, out, 0o644)
	}
}

func TestRenderContainsText(t *testing.T) {
	out, err := render(sample(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"(Siti Rahmawati)", "(HX-2026-0914-2345)", "(Rp1.227.120)", "(2 Oktober 2026)", "(3 Tiket)", "(Presale 1 \\(Reguler\\))"} {
		if !bytes.Contains(out, []byte(s)) {
			t.Errorf("missing %s", s)
		}
	}
	for _, s := range []string{"Jakarta", "Night", "14 November 2026", "Valid Ticket"} {
		if !bytes.Contains(out, utf16be(s)) {
			t.Errorf("missing %q", s)
		}
	}
}

func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 17)
	copy(ihdr, "IHDR")
	binary.BigEndian.PutUint32(ihdr[4:], w)
	binary.BigEndian.PutUint32(ihdr[8:], h)
	ihdr[12], ihdr[13] = 8, 6
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
}

func TestRenderBanner(t *testing.T) {
	img, _ := assets.ReadFile("assets/mask-group.png")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(img)
	}))
	defer srv.Close()
	srvHost := strings.TrimPrefix(srv.URL, "http://")

	dir := t.TempDir()
	pub := filepath.Join(dir, "public")
	os.MkdirAll(filepath.Join(pub, "events"), 0o755)
	os.WriteFile(filepath.Join(pub, "events", "ok.png"), img, 0o644)
	os.WriteFile(filepath.Join(dir, "secret.png"), img, 0o644)
	os.WriteFile(filepath.Join(pub, "huge.png"), pngHeader(20000, 20000), 0o644)
	os.WriteFile(filepath.Join(pub, "toomany.png"), pngHeader(9000, 9000), 0o644)
	os.WriteFile(filepath.Join(pub, "junk.png"), []byte("not an image"), 0o644)
	os.Symlink(filepath.Join(dir, "secret.png"), filepath.Join(pub, "link.png"))
	big, _ := os.Create(filepath.Join(pub, "big.png"))
	big.Write(img)
	big.Truncate(maxBannerBytes + 1)
	big.Close()

	load := LocalBanners(dir, []string{"dev.hectix.id", srvHost})
	count := func(url string) int {
		d := sample()
		d.EventBannerURL = url
		out, err := render(d, load, false)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		return strings.Count(string(out), "/Subtype /Image")
	}
	base := count("")
	for _, u := range []string{"https://dev.hectix.id/uploads/events/ok.png", "http://DEV.hectix.id/uploads/events/ok.png?v=2", srv.URL + "/uploads/events/ok.png"} {
		if got := count(u); got <= base {
			t.Fatalf("%s: banner not embedded: %d vs %d", u, got, base)
		}
	}
	for _, u := range []string{
		srv.URL + "/ok.png",
		"https://evil.example/uploads/events/ok.png",
		"https://dev.hectix.id/events/ok.png",
		"https://dev.hectix.id/uploads/../secret.png",
		"https://dev.hectix.id/uploads/%2e%2e/secret.png",
		"https://dev.hectix.id/uploads/link.png",
		"https://dev.hectix.id/uploads/huge.png",
		"https://dev.hectix.id/uploads/toomany.png",
		"https://dev.hectix.id/uploads/big.png",
		"https://dev.hectix.id/uploads/junk.png",
		"https://dev.hectix.id/uploads/missing.png",
		"file:///etc/passwd",
	} {
		if got := count(u); got != base {
			t.Errorf("%s: images %d, want placeholder %d", u, got, base)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("remote server hit %d times", n)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(pngHeader(20000, 20000))); err != nil || cfg.Width != 20000 {
		t.Fatalf("bomb header not decodable: %v", err)
	}
}

func TestRenderLongValuesAndCategory(t *testing.T) {
	d := sample()
	d.EventCategory = "Festival"
	d.EventName = strings.Repeat("Sangat Panjang Sekali ", 6)
	d.BuyerName = "Ñoño Ünïcödé — 李雷"
	d.EventVenueName, d.EventCity = "", ""
	out, err := Render(d, nil)
	if err != nil || !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Fatalf("err=%v", err)
	}
}

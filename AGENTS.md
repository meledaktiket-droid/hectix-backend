# AGENTS.md — hectix-backend (Go 1.26, LEGACY MODE)

> Baca juga `../../AGENTS.md` (rule workspace). File ini = rule spesifik backend.
> Kode ini adalah port Go dari backend lama (better-auth compatible) yang **diverifikasi paritas kontrak** via `contracts.txt` + `testdata/golden/`. Perilaku = kontrak. Jangan diubah tanpa izin.

---

## 1. Struktur & Kepemilikan

```
cmd/api/        main.go (server+worker+sweeper, shutdown), server.go (routing), jobs.go (handler job), staff.go (gate admin)
cmd/contractdiff/  CLI pembanding respons lama vs baru (contracts.txt)
internal/
  auth/       session cookie, Google OAuth, OriginCheck (CSRF), organisasi/member/invitation (org.go 1191 baris — file terbesar, hati-hati)
  admin/      handler app admin + RequireStaff/IsAdminRole + AcceptPath
  organizer/  handler app organizer (events, tickets, vouchers, publish, finance, export)
  scanner/    handler app scanner (event/session access, ticket redemption, gate in/out, wristband, reconciliation)
  web/        handler app pembeli (events, search, favorites, histori, orders/cancel, profile, vouchers)
  orders/     checkout.go — reservasi stok + voucher + charge Midtrans
  payments/   midtrans.go (charge), webhook.go (/api/midtrans), transitions.go (ApplyStatus/markPaid/Release), sweep.go (expire)
  jobs/       antrian job Postgres (FOR UPDATE SKIP LOCKED, stale 10 menit)
  eticket/ ticketpdf/ mail/   kirim e-ticket PDF via Resend
  ordersexport/  CSV/XLSX/PDF export (textfit.go knuthPlass = kompleksitas tertinggi, jangan disentuh tanpa alasan)
  uploads/    upload ber-token HMAC, file privat, /uploads publik
  httpx/ dbx/ config/ settings/ deps/   helper inti (fan-in tinggi: httpx.JSON/Error, dbx.Tx, httpx.Time.Scan)
  testdb/ testutil/ golden/   helper test
migrations/   000001_baseline s.d. 000006_scanner_perf_indexes (semua SQL di-embed; 000001 dan 000002 jangan diedit sesuai AGENTS workspace; lihat file aktual sebelum membuat migrasi baru)
```

Contoh helper bersama dengan fan-in tinggi menurut indeks MCP: `httpx.Time.Scan`, `dbx.DB.QueryRow`, `dbx.DB.Exec`, `httpx.JSON`, `httpx.Error`, `settings.Get`, dan `auth.UserFrom`. Jumlah caller berubah seiring kode; cek indeks dan `rg` sebelum menyimpulkan dampak. **Jangan mengubah signature/perilaku helper bersama ini tanpa izin eksplisit.**

---

## 2. Routing & Auth (invariant)

- `server.go`: tiap app punya `chi.Router` sendiri; middleware urut `auth.OriginCheck` → `a.Load` → (admin: `staffOnly`). Router scanner juga memakai `OriginCheck` dan `a.Load`, tanpa `staffOnly`; urutan middleware **jangan diubah**.
- App dipilih `httpx.ByHost` dari `r.Host`; `X-Forwarded-Host` menimpa Host (asumsi: hanya dipanggil lewat Caddy/Next, port 8080 bind `127.0.0.1`).
- App `scanner` hanya didaftarkan bila `SCANNER_BASE_URL` tersedia. Handler scanner berada di `internal/scanner`; izin akses event/crew diperiksa backend berdasarkan event, bukan hanya role atau route UI.
- `staffOnly` membebaskan `/api/auth*` dan tepat `AcceptPath` (cek `RawPath` juga — anti bypass encoding). Jangan tambah pengecualian baru tanpa izin.
- Endpoint baru yang butuh login: taruh di `r.Group` dengan `r.Use(auth.Required)`. Ambil user hanya dari `auth.UserFrom(ctx)` — **jangan** dari body/query.
- Endpoint organizer: wajib scope ke organisasi aktif member (ikuti pola handler organizer yang ada). Endpoint admin: sudah tergate staff.
- Endpoint scanner: ikuti pemeriksaan akses event dan crew di `internal/scanner`; jangan mengandalkan pembatasan UI scanner sebagai otorisasi.
- Cookie: nama `{app}.session_token` (prefix `__Secure-` bila https), nilai `Sign(token)` HMAC. `ExpiresIn` 7 hari, `UpdateAge` 1 hari. Jangan ubah.

---

## 3. Kontrak API (paritas)

- Respons JSON lewat `httpx.JSON` (SetEscapeHTML=false) dan error lewat `httpx.Error(w, status, "pesan")` → `{"error": "..."}`. Route `/api/auth/*` pakai format better-auth (`authError` dengan `code`).
- Waktu pakai `httpx.Time` (format khusus). Jangan ganti ke `time.Time` mentah.
- Pesan error berbahasa Indonesia/Inggris campur **sesuai aslinya** — frontend kadang mencocokkan string. Jangan "rapikan".
- Field baru di respons: boleh **ditambah** bila diminta; **menghapus/rename/mengubah tipe = tanya user**.
- `contracts.txt` (203 kontrak) + `testdata/golden/` = acuan. Prefix `~` = kontrak yang toleran/berubah.

---

## 4. Uang, Stok & Status Order (ZONA MERAH)

Setiap perubahan di sini **wajib task eksplisit + persetujuan + test**.

- **Pembulatan**: `jsRound(x) = floor(x+0.5)` (meniru `Math.round` JS). Frontend checkout preview memakai `Math.round` → harus tetap identik. Jangan ganti ke `math.Round`.
- **Rumus total** (`orders/checkout.go`): `subtotal - voucherDiscount` → `tax = jsRound(discounted * TaxPercent/100)` → `serviceFee = jsRound(ServiceFee)` → `total`. Kolom `order.subtotal` menyimpan **nilai setelah diskon**. Jangan ubah semantik ini.
- **Harga selalu dari DB** (`ticket_type.price`), tidak pernah dari client.
- **Reservasi atomik**: `update ticket_type set sold = sold + $1 where id=$2 and sold + $1 <= quantity`; 0 row → `errSoldOut` (409 "Tiket habis").
- **Urutan lock** (anti-deadlock): ticket_type diurutkan by `id` (checkout sort `reserve`; Release/Sweep `order by id for update`), lalu voucher by `id`. Kode baru yang mengunci baris yang sama **wajib** ikut urutan ini.
- **State machine order**: `pending_payment` → `paid` | `expired` | `cancelled`. Semua update memakai guard `where status='pending_payment'` (idempoten). `markPaid` menerima `pending_payment|paid`, tidak membuat ulang ticket bila sudah ada; paid setelah expired hanya di-log.
- **Release kuota** ada di 2 tempat: `payments.Release` (per order) dan `payments.Sweep` (batch, inline SQL). Ubah satu → ubah keduanya + test keduanya.
- **Webhook Midtrans**: verifikasi `sha512(order_id+status_code+gross_amount+ServerKey)` constant-time → hanya enqueue job `order.status`. Pemrosesan di job (retry-able). Jangan proses langsung di handler, jangan longgarkan verifikasi.
- **Sweeper** expire order `expires_at < now() - 2 minutes` (grace untuk webhook telat). Order TTL 15 menit. Jangan ubah angka tanpa izin.
- Setelah charge Midtrans gagal → order di-cancel + release dalam tx terpisah (`context.WithoutCancel`). Pertahankan.

---

## 5. Upload & File

- Alur 2 langkah: `POST /api/upload` (auth, rate limit 60/jam/user, policy per app) → token HMAC 5 menit → `PUT /api/upload/put?t=` (cek Content-Type, ukuran, sniff 512 byte, tulis ke `.tmp` lalu `Link` via `os.OpenRoot`).
- Policy: web → `profile/{userId}` gambar; organizer → `verification/{userId}` gambar+PDF (**privat**); admin → `banners` gambar. Prefix `verification/` = privat, hanya pemilik / anggota org yang sama / staff admin.
- File privat disajikan dengan `no-store`, `nosniff`, `CSP: sandbox`. Jangan dihapus.
- Jangan ganti `os.OpenRoot`/`root.*` ke `os.*` biasa (proteksi traversal & symlink).

---

## 6. SQL & DB

- Driver `pgx/v5` + `pgxpool`. Transaksi lewat `dbx.Tx(ctx, pool, func(tx pgx.Tx) error {...})`.
- Parameter `$n` selalu. Pola filter dinamis: helper `conds.add("kolom = ?", v)` / `c.where()` (admin). `findOrg(ctx, field, value)` hanya boleh dipanggil dengan literal `"id"`/`"slug"`.
- Konkatenasi string SQL hanya untuk **konstanta** (`orgCols`, `eventCols`, filter literal di `histori.go`). Jangan pernah konkat input.
- Tabel `"order"` & `"user"` adalah reserved word → selalu dikutip.
- ID: `dbx.UUID()` / `dbx.ID()`. Waktu DB UTC.
- Schema baru = migrasi baru `00000N_nama.up.sql` + `.down.sql`, persetujuan user, jalankan `go run ./cmd/api migrate` di lokal dulu.

---

## 7. Verifikasi (jalankan dari `hectix/hectix-backend`)

```powershell
gofmt -l ./cmd ./internal          # harus kosong untuk file yang kamu ubah
go vet ./...                        # baseline audit: bersih (exit 0)
go build ./...
go test ./internal/<paket>/...      # native Windows via TEST_DATABASE_URL=postgres://.../hectix_test ATAU Docker
go test ./...                       # penuh, sebelum serah terima
```

- **Testing Wajib (No Excuses):** Setiap penambahan atau perubahan fitur/endpoint backend **wajib disertai unit / integration test** (`*_test.go`) dengan style yang matching dengan pola legacy (`setup(t)`, `seed*`, `do(t, ...)`, `want(t, ...)`, `golden.Assert`). Keberadaan AI coding agent bukan alasan untuk mengabaikan test, melainkan sarana untuk memastikan verifikasi otomatis selalu dibuat dan dijalankan sebelum diserahterimakan.
- Paket uang/auth/upload punya test luas (`checkout_test`, `transitions_test`, `sweep_test`, `webhook_test`, `session_test`, `oauth_test`, `org_test`, `uploads_test`, `cmd/api/*_routes_test`). Ubah perilaku → tambah/ubah test di file yang sama.
- Eksekusi Test di Windows: `testdb` sudah mendukung `TEST_DATABASE_URL` (menghubungkan ke `hectix_test` dan mereset schema secara otomatis dan terisolasi), sehingga test dapat berjalan cepat (1 detik) tanpa butuh Docker.
- Paritas kontrak (opsional, butuh server lama & baru): `go run ./cmd/contractdiff contracts.txt` dengan env `NEW`, `<APP>_HOST`, `<APP>_COOKIE`.
- Setelah edit: MCP `detect_changes` untuk project backend.

---

## 8. Dilarang Tanpa Izin Eksplisit

- Mengubah `go.mod`/`go.sum`, versi Go, Dockerfile, `deploy/*` (compose, Caddyfile, deploy.sh, backup.sh).
- Mengubah `config.Load` (env wajib) atau menambah env wajib baru.
- Mengubah jumlah worker (4), `staleAfter`, TTL order, grace sweeper, rate limit upload, `maxSize` upload.
- Mengganti library (chi, pgx, fpdf, excelize) atau menambah dependency baru.
- Commit `deploy/docker-compose.yml` yang sedang uncommitted (pool tuning) — milik user.

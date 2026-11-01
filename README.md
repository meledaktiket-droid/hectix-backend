# Hectix Backend (`hectix/backend`)

Backend monolith HTTP API, background job worker, dan payment sweeper untuk platform ticketing **Hectix**, ditulis dalam **Go 1.26**. Satu binary API ini melayani 3 aplikasi frontend sekaligus (`web`, `organizer`, dan `admin`) menggunakan routing berbasis header `Host` / `X-Forwarded-Host`.

---

## 1. Spesifikasi Runtime & Infrastruktur

| Komponen | Versi di Repo | Sumber Konfigurasi |
|---|---|---|
| **Go** | `1.26.0` (Docker build: `golang:1.26`) | `go.mod`, `Dockerfile` |
| **Runtime Container** | `gcr.io/distroless/static-debian12:nonroot` | `Dockerfile` |
| **Database** | PostgreSQL `17` (`postgres:17`, timezone `UTC`) | `deploy/docker-compose.yml`, `deploy/docker-compose.dev-db.yml` |
| **Reverse Proxy** | Caddy `2` (`caddy:2`) | `deploy/docker-compose.yml`, `deploy/Caddyfile` |

### Dependensi Langsung (`go.mod`)
| Package | Versi | Fungsi di Aplikasi |
|---|---|---|
| `github.com/go-chi/chi/v5` | `v5.3.2` | HTTP router untuk sub-router `web`, `organizer`, dan `admin` |
| `github.com/jackc/pgx/v5` | `v5.11.0` | Driver PostgreSQL & connection pool (`pgxpool`) |
| `github.com/golang-migrate/migrate/v4` | `v4.20.1` | Eksekusi migrasi SQL embedded (`migrations/*.sql`) |
| `github.com/go-pdf/fpdf` | `v0.9.0` | Generator PDF untuk E-Ticket (`internal/ticketpdf`) & Export Order (`internal/ordersexport`) |
| `github.com/xuri/excelize/v2` | `v2.11.0` | Generator file Excel `.xlsx` untuk Export Order (`internal/ordersexport`) |
| `github.com/skip2/go-qrcode` | `v0.0.0-20200617195104-da1b6568686e` | Pembuatan QR Code pada PDF E-Ticket |
| `golang.org/x/oauth2` | `v0.37.0` | Autentikasi Google OAuth 2.0 (`internal/auth`) |
| `github.com/testcontainers/testcontainers-go` | `v0.44.0` | Container PostgreSQL otomatis untuk integrasi unit test (`internal/testdb`) |

---

## 2. Penjelasan Struktur Folder

```text
hectix-backend/
├── cmd/
│   ├── api/                 # Entry point server utama (main.go, server.go, jobs.go, staff.go)
│   └── contractdiff/        # CLI tool pembanding respons JSON/binary terhadap contracts.txt
├── internal/
│   ├── admin/               # Handler API aplikasi Admin (banners, dashboard, events, orders, orgs, payouts, settings, team, users)
│   ├── auth/                # Session cookie, Google OAuth, Origin check, dan manajemen organisasi (better-auth compatible)
│   ├── config/              # Loader & validator environment variables (config.go)
│   ├── dbx/                 # Wrapper pgxpool, transaksi, migrasi database, dan generator UUID/ID
│   ├── deps/                # Struct dependency injection (DB, Config, Mailer)
│   ├── eticket/             # Background job handler pengiriman email E-Ticket beserta lampiran PDF
│   ├── golden/              # Helper pengujian snapshot terhadap file di testdata/golden/
│   ├── httpx/               # Helper JSON response, error response, time formatting, dan host-based router (ByHost)
│   ├── jobs/                # In-process PostgreSQL job queue worker (FOR UPDATE SKIP LOCKED, 4 worker concurrency)
│   ├── mail/                # Client pengiriman email via Resend HTTP API beserta template HTML/Text
│   ├── orders/              # Handler transaksi checkout tiket & reservasi kuota/voucher (checkout.go)
│   ├── ordersexport/        # Engine render laporan order ke format CSV, XLSX, dan PDF
│   ├── organizer/           # Handler API aplikasi Organizer (dashboard, events, export, finance, invite, orders, performers, publish, tickets, vouchers)
│   ├── payments/            # Integrasi Midtrans Core API, HTTP Webhook (/api/midtrans), status transition, dan sweeper order expired
│   ├── settings/            # Pembacaan konfigurasi tarif pajak & biaya layanan dari tabel site_setting
│   ├── testdb/              # Provisioning database PostgreSQL sementara untuk go test
│   ├── testutil/            # Helper assertion pengujian
│   ├── ticketpdf/           # Engine render desain PDF E-Ticket + font/asset embedded
│   ├── uploads/             # Sistem upload file lokal ber-token HMAC (/api/upload, /api/upload/put, /api/files/*, /uploads/*)
│   └── web/                 # Handler API aplikasi Web Buyer (home, events, search, comments, favorites, histori, orders, profile, vouchers)
├── migrations/              # File SQL migrasi database (000001_baseline, 000002_job) yang di-embed ke binary
├── testdata/golden/         # Snapshot referensi JSON untuk pengujian parity kontrak API
├── deploy/                  # Konfigurasi produksi (docker-compose.yml, Caddyfile, deploy.sh, backup.sh, .env.example)
├── Dockerfile               # Multi-stage Dockerfile untuk mem-build binary /api
├── dev.sh                   # Script bash untuk menjalankan server di lokal dengan membaca .env.local
├── .env.example             # Template variabel environment backend
├── go.mod                   # Definisi modul Go (hectix/backend)
└── go.sum                   # Checksum dependensi Go
```

---

## 3. Variabel Environment (`.env.local` / `.env`)

Berdasarkan `internal/config/config.go`, berikut daftar variabel environment yang digunakan oleh `cmd/api`:

| Variabel | Wajib? | Default | Keterangan |
|---|---|---|---|
| `DATABASE_URL` | **Ya** | - | URL koneksi PostgreSQL (`postgres://user:pass@host:5432/dbname?sslmode=disable&timezone=UTC`) |
| `AUTH_SECRET` | **Ya** | - | Secret key untuk HMAC signing session cookie & upload token |
| `GOOGLE_CLIENT_ID` | **Ya** | - | OAuth 2.0 Client ID dari Google Cloud Console |
| `GOOGLE_CLIENT_SECRET` | **Ya** | - | OAuth 2.0 Client Secret dari Google Cloud Console |
| `MIDTRANS_SERVER_KEY` | **Ya** | - | Server Key Midtrans Core API |
| `MIDTRANS_IS_PRODUCTION` | Tidak | `false` | Set ke `"true"` untuk mengarahkan Midtrans ke endpoint produksi (`https://api.midtrans.com`) |
| `RESEND_API_KEY` | **Ya** | - | API Key layanan email Resend (`https://api.resend.com/emails`) |
| `WEB_BASE_URL` | **Ya** | - | URL absolut aplikasi Web (contoh lokal: `http://localhost:3000`, prod: `https://hectix.id`) |
| `ORGANIZER_BASE_URL` | **Ya** | - | URL absolut aplikasi Organizer (contoh lokal: `http://localhost:3001`, prod: `https://organizer.hectix.id`) |
| `ADMIN_BASE_URL` | **Ya** | - | URL absolut aplikasi Admin (contoh lokal: `http://localhost:3002`, prod: `https://admin.hectix.id`) |
| `ADDR` | Tidak | `:8080` | Alamat dan port HTTP listen server Go |
| `MAIL_FROM` | Tidak | `Hectix <no-reply@hectix.id>` | Identitas pengirim email |
| `UPLOAD_DIR` | Tidak | `/var/hectix/uploads` | Direktori penyimpanan file upload (`public/` dan `private/`) |

Khusus pada `deploy/.env` (untuk produksi dengan Docker Compose & Caddy), terdapat tambahan:
- `API_HOST`: Domain untuk Caddy Backend (contoh: `api.hectix.id`).
- `FRONTEND_IP`: Daftar IP instance Frontend yang diizinkan di `trusted_proxies` Caddy agar header `X-Forwarded-Host` diteruskan ke Go API.
- `DB_PASSWORD`: Password untuk container PostgreSQL `db`.

---

## 4. Cara Menjalankan Mode Development (Lokal)

### A. Menyiapkan Database PostgreSQL Lokal
Anda bisa menggunakan PostgreSQL lokal (versi 17) atau menjalankan container database dev yang tersedia di `deploy/docker-compose.dev-db.yml` (port host `5433`):
```bash
docker compose -f deploy/docker-compose.dev-db.yml up -d
```

### B. Membuat File `.env.local`
Salin `.env.example` menjadi `.env.local` di root folder `hectix-backend`:
```bash
cp .env.example .env.local
```
Contoh isi `.env.local` untuk development lokal:
```dotenv
DATABASE_URL=postgres://hectix:hectix@localhost:5432/hectix?sslmode=disable&timezone=UTC
ADDR=:8080
WEB_BASE_URL=http://localhost:3000
ORGANIZER_BASE_URL=http://localhost:3001
ADMIN_BASE_URL=http://localhost:3002
AUTH_SECRET=dev-secret-key-minimal-32-karakter-123456
GOOGLE_CLIENT_ID=dummy-google-client-id
GOOGLE_CLIENT_SECRET=dummy-google-client-secret
MIDTRANS_SERVER_KEY=SB-Mid-server-dummy
MIDTRANS_IS_PRODUCTION=false
RESEND_API_KEY=re_dummy_key
MAIL_FROM="Hectix <no-reply@hectix.id>"
UPLOAD_DIR=./.uploads
```

### C. Menjalankan Migrasi Database & Server API
**Di Linux / macOS / Git Bash / WSL (menggunakan `dev.sh`):**
```bash
# Jalankan migrasi SQL
./dev.sh migrate

# Jalankan HTTP server (:8080), background job worker, dan payment sweeper
./dev.sh
```

**Di Windows PowerShell:**
```powershell
# Load variabel dari .env.local ke session PowerShell
Get-Content .env.local | ForEach-Object {
    if ($_ -match '^\s*([^#][^=]+)=(.*)$') {
        [Environment]::SetEnvironmentVariable($matches[1].Trim(), $matches[2].Trim().Trim('"'), 'Process')
    }
}

# Jalankan migrasi SQL
go run ./cmd/api migrate

# Jalankan server API
go run ./cmd/api
```

Verifikasi server berjalan dengan mengakses `http://localhost:8080/healthz` (mengembalikan `ok`).

---

## 5. Cara Build & Menjalankan Tanpa Docker (Perintah Langsung / Native)

Jika Anda ingin menjalankan aplikasi secara langsung di server Linux/Windows tanpa Docker:

### A. Persiapan Database & Direktori Upload
1. Pastikan **PostgreSQL 17** sudah berjalan di port `5432`, lalu buat user dan database:
   ```bash
   psql -U postgres -c "CREATE USER hectix WITH PASSWORD 'password_anda';"
   psql -U postgres -c "CREATE DATABASE hectix OWNER hectix;"
   ```
2. Buat direktori penyimpanan file upload (sesuai nilai `UPLOAD_DIR`):
   ```bash
   sudo mkdir -p /var/hectix/uploads
   sudo chown -R $USER:$USER /var/hectix/uploads
   ```

### B. Build Binary Go Secara Langsung
Dari root folder `hectix-backend`, jalankan perintah build (sesuai flag di `Dockerfile`):
```bash
# Linux / macOS
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o api ./cmd/api

# Windows PowerShell
$env:CGO_ENABLED="0"; go build -trimpath -ldflags="-s -w" -o api.exe ./cmd/api
```

### C. Menjalankan Migrasi & Server secara Langsung
1. Siapkan file `.env.local` (atau `.env`) berisi seluruh variabel wajib (`DATABASE_URL`, `AUTH_SECRET`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `MIDTRANS_SERVER_KEY`, `RESEND_API_KEY`, `WEB_BASE_URL`, `ORGANIZER_BASE_URL`, `ADMIN_BASE_URL`, `UPLOAD_DIR`).
2. **Menjalankan dengan `go run` (via `./dev.sh` di Linux/macOS/Git Bash):**
   ```bash
   # Jalankan migrasi
   ./dev.sh migrate

   # Jalankan server API di :8080
   ./dev.sh
   ```
3. **Menjalankan langsung dari hasil compile binary (`./api`):**
   ```bash
   # Load variabel environment dari file .env.local
   set -a && source .env.local && set +a

   # 1. Jalankan migrasi database
   ./api migrate

   # 2. Jalankan HTTP server, background job worker, dan payment sweeper
   ./api
   ```
   *(Di Windows PowerShell, ganti `./api` dengan `.\api.exe` setelah me-load `.env.local`).*

### D. Konfigurasi Caddy Native (Tanpa Docker)
Jika menggunakan Caddy yang terinstall langsung di OS server (bukan container), arahkan `reverse_proxy` di `/etc/caddy/Caddyfile` ke `localhost:8080`:
```caddyfile
{
	servers {
		trusted_proxies static <IP_SERVER_FRONTEND>
	}
}

api.hectix.id {
	encode zstd gzip
	reverse_proxy localhost:8080
}
```

---

## 6. Cara Build & Menjalankan dengan Docker Compose (`deploy/`)

Di dalam folder `deploy/` sudah tersedia `docker-compose.yml` (berisi service `db`, `api`, dan `caddy`), `Caddyfile`, `deploy.sh`, dan `backup.sh`.

1. Masuk ke folder `deploy/` dan buat file `.env`:
   ```bash
   cd deploy
   cp .env.example .env
   nano .env
   ```
2. Jalankan script `deploy.sh` (akan menjalankan `git pull --ff-only`, `docker compose build`, `docker compose run --rm api migrate`, dan `docker compose up -d --remove-orphans`):
   ```bash
   chmod +x deploy.sh backup.sh
   ./deploy.sh
   ```
3. Untuk melakukan backup database (`pg_dump -Fc`) dan volume upload (`hectix_uploads`) ke `/var/backups/hectix` (dengan retensi default 14 hari):
   ```bash
   ./backup.sh
   ```

---

## 7. Menjalankan Unit & Integration Test
Suite test Go di repo ini menggunakan `testcontainers-go` (membutuhkan Docker daemon aktif untuk menyalakan container Postgres test secara otomatis, atau variabel `TEST_DATABASE_URL`):
```bash
go test ./...
```

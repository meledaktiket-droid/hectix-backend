package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hectix/backend/internal/config"
)

func TestScannerHostRoutingAndIsolation(t *testing.T) {
	cfg := config.Config{
		AuthSecret: "test-secret",
		UploadDir:  t.TempDir(),
		Apps: map[string]config.App{
			"web-dev.hectix.id": {
				Name:    "web",
				Host:    "web-dev.hectix.id",
				BaseURL: "https://web-dev.hectix.id",
				Secure:  true,
			},
			"organizer-dev.hectix.id": {
				Name:    "organizer",
				Host:    "organizer-dev.hectix.id",
				BaseURL: "https://organizer-dev.hectix.id",
				Secure:  true,
			},
			"admin-dev.hectix.id": {
				Name:    "admin",
				Host:    "admin-dev.hectix.id",
				BaseURL: "https://admin-dev.hectix.id",
				Secure:  true,
			},
			"scanner-dev.hectix.id": {
				Name:    "scanner",
				Host:    "scanner-dev.hectix.id",
				BaseURL: "https://scanner-dev.hectix.id",
				Secure:  true,
			},
		},
	}

	srv := newServer(cfg, nil)

	tests := []struct {
		name           string
		host           string
		method         string
		path           string
		wantStatus     int
		wantNotStatus  int
	}{
		{
			name:          "scanner allows auth session",
			host:          "scanner-dev.hectix.id",
			method:        http.MethodGet,
			path:          "/api/auth/get-session",
			wantStatus:    http.StatusOK,
		},
		{
			name:          "scanner mounts scanner events (requires auth -> 401, not 404)",
			host:          "scanner-dev.hectix.id",
			method:        http.MethodGet,
			path:          "/api/scanner/events",
			wantStatus:    http.StatusUnauthorized,
		},
		{
			name:          "scanner blocks organizer finance route",
			host:          "scanner-dev.hectix.id",
			method:        http.MethodGet,
			path:          "/api/finance",
			wantStatus:    http.StatusNotFound,
		},
		{
			name:          "scanner blocks organizer payout route",
			host:          "scanner-dev.hectix.id",
			method:        http.MethodGet,
			path:          "/api/org/payout",
			wantStatus:    http.StatusNotFound,
		},
		{
			name:          "scanner blocks create organization route",
			host:          "scanner-dev.hectix.id",
			method:        http.MethodPost,
			path:          "/api/auth/organization/create",
			wantStatus:    http.StatusNotFound,
		},
		{
			name:          "organizer still mounts finance route",
			host:          "organizer-dev.hectix.id",
			method:        http.MethodGet,
			path:          "/api/finance",
			wantNotStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = tc.host
			req.Header.Set("X-Forwarded-Host", tc.host)
			if tc.method == http.MethodPost {
				req.Header.Set("Origin", "http://"+tc.host)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if tc.wantStatus != 0 && rec.Code != tc.wantStatus {
				t.Fatalf("got status %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantNotStatus != 0 && rec.Code == tc.wantNotStatus {
				t.Fatalf("did not expect status %d", tc.wantNotStatus)
			}
		})
	}
}

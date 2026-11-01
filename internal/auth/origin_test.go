package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hectix/backend/internal/httpx"
)

func TestOriginCheck(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	h := OriginCheck(ok)
	const (
		missing    = `{"message":"Missing or null Origin","code":"MISSING_OR_NULL_ORIGIN"}` + "\n"
		invalid    = `{"message":"Invalid origin","code":"INVALID_ORIGIN"}` + "\n"
		appInvalid = `{"error":"Invalid origin"}` + "\n"
		cookie     = "web.session_token=x"
		jsonCT     = "application/json"
		web        = "https://dev.hectix.id"
		authPath   = "/api/auth/sign-out"
		appPath    = "/api/orders"
		evil       = "https://evil.com"
	)
	cases := []struct {
		name, method, path, origin, referer, cookie, ct, body string
		code                                                  int
		want                                                  string
	}{
		{"cross-origin POST with cookie (auth)", "POST", authPath, evil, "", cookie, jsonCT, "{}", 403, invalid},
		{"cross-origin POST with cookie (app)", "POST", appPath, evil, "", cookie, jsonCT, "{}", 403, appInvalid},
		{"missing origin with cookie (auth)", "POST", authPath, "", "", cookie, jsonCT, "{}", 403, missing},
		{"null origin with cookie (auth)", "POST", authPath, "null", "", cookie, jsonCT, "{}", 403, missing},
		{"missing origin with cookie (app)", "DELETE", appPath, "", "", cookie, "", "", 403, appInvalid},
		{"subdomain is not same origin", "POST", appPath, "https://org.dev.hectix.id", "", cookie, jsonCT, "{}", 403, appInvalid},
		{"http vs https", "POST", appPath, "http://dev.hectix.id", "", cookie, jsonCT, "{}", 403, appInvalid},
		{"same origin passes", "POST", authPath, web, "", cookie, jsonCT, "{}", 200, "ok"},
		{"same origin app route passes", "PATCH", appPath, web, "", cookie, jsonCT, "{}", 200, "ok"},
		{"no cookie passes (webhook)", "POST", "/api/payments/notification", evil, "", "", jsonCT, "{}", 200, "ok"},
		{"no cookie, no origin passes", "POST", appPath, "", "", "", jsonCT, "{}", 200, "ok"},
		{"referer fallback ok", "POST", authPath, "", web + "/events/1?x=y", cookie, jsonCT, "{}", 200, "ok"},
		{"referer fallback cross-origin", "POST", authPath, "", evil + "/x", cookie, jsonCT, "{}", 403, invalid},
		{"GET unaffected", "GET", authPath, evil, "", cookie, "", "", 200, "ok"},
		{"HEAD unaffected", "HEAD", appPath, evil, "", cookie, "", "", 200, "ok"},
		{"OPTIONS unaffected", "OPTIONS", appPath, evil, "", cookie, "", "", 200, "ok"},
		{"auth body without content-type", "POST", authPath, web, "", cookie, "", "{}", 415,
			`{"message":"Content-Type is required. Allowed types: application/json","code":"UNSUPPORTED_MEDIA_TYPE"}` + "\n"},
		{"auth form body", "POST", authPath, evil, "", cookie, "application/x-www-form-urlencoded", "a=b", 415,
			`{"message":"Content-Type \"application/x-www-form-urlencoded\" is not allowed. Allowed types: application/json","code":"UNSUPPORTED_MEDIA_TYPE"}` + "\n"},
		{"auth json with charset", "POST", authPath, web, "", cookie, "Application/JSON; charset=utf-8", "{}", 200, "ok"},
		{"auth empty body needs no content-type", "POST", authPath, web, "", cookie, "", "", 200, "ok"},
		{"app route form body allowed", "POST", appPath, web, "", cookie, "multipart/form-data; boundary=x", "x", 200, "ok"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		a := apps["web"]
		a.BaseURL = web
		req = req.WithContext(httpx.WithApp(req.Context(), a))
		for k, v := range map[string]string{"Origin": c.origin, "Referer": c.referer, "Cookie": c.cookie, "Content-Type": c.ct} {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.code || rec.Body.String() != c.want {
			t.Errorf("%s: got %d %q, want %d %q", c.name, rec.Code, rec.Body.String(), c.code, c.want)
		}
	}
}

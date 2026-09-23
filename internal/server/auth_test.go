package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func authed(secure bool) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	return SecurityHeaders(RequireToken(AuthConfig{Token: "s3cret", Secure: secure}, ok))
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequireToken(t *testing.T) {
	h := authed(false)
	withBearer := httptest.NewRequest("GET", "/api/summary", nil)
	withBearer.Header.Set("Authorization", "Bearer s3cret")
	withCookie := httptest.NewRequest("GET", "/", nil)
	withCookie.AddCookie(&http.Cookie{Name: TokenCookie, Value: "s3cret"})
	wrongBearer := httptest.NewRequest("GET", "/api/summary", nil)
	wrongBearer.Header.Set("Authorization", "Bearer nope")
	login := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {"s3cret"}}.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badLogin := httptest.NewRequest("POST", "/login", strings.NewReader("token=nope"))
	badLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	tests := []struct {
		name string
		req  *http.Request
		code int
		body string
	}{
		{"bearer", withBearer, 200, "ok"},
		{"cookie", withCookie, 200, "ok"},
		{"wrong bearer on API", wrongBearer, 401, `"error": "authentication required`},
		{"no credentials on MCP", httptest.NewRequest("POST", "/mcp", nil), 401, "authentication required"},
		{"no credentials on UI", httptest.NewRequest("GET", "/", nil), 401, `<form method="post" action="/login">`},
		{"wrong URL token", httptest.NewRequest("GET", "/?token=nope", nil), 401, "not valid"},
		{"login", login, 303, ""},
		{"bad login", badLogin, 401, "not valid"},
	}
	for _, tt := range tests {
		rec := do(h, tt.req)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("%s: %d %q, want %d containing %q", tt.name, rec.Code, rec.Body.String(), tt.code, tt.body)
		}
		if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: missing security headers", tt.name)
		}
	}
}

func TestTokenURLSetsCookieAndDropsToken(t *testing.T) {
	for _, secure := range []bool{false, true} {
		rec := do(authed(secure), httptest.NewRequest("GET", "/?token=s3cret&x=1", nil))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?x=1" {
			t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies = %v", cookies)
		}
		c := cookies[0]
		if c.Name != TokenCookie || c.Value != "s3cret" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure != secure {
			t.Errorf("cookie = %+v (secure=%v)", c, secure)
		}
	}
}

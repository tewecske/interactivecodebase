package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// TokenCookie holds the access token in browsers.
const TokenCookie = "icb_token"

// AuthConfig configures RequireToken.
type AuthConfig struct {
	// Token is the shared secret. Requests carry it as a bearer token, in
	// the cookie, or once as ?token= to get the cookie.
	Token string
	// Secure marks the cookie Secure (serving over TLS).
	Secure bool
}

// RequireToken lets through only requests that carry the token:
//
//   - Authorization: Bearer <token> (API and MCP clients);
//   - the icb_token cookie (browsers);
//   - ?token=<token> on a GET, which sets the cookie and redirects to the
//     same URL without it, so the token does not linger in history;
//   - POST /login with a token form field, from the sign-in page.
//
// Other requests get 401: JSON for /api/, plain text for /mcp, and a
// sign-in page otherwise. The cookie is HttpOnly and SameSite=Strict, so
// other sites cannot make requests with it.
func RequireToken(cfg AuthConfig, next http.Handler) http.Handler {
	valid := func(t string) bool {
		return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(cfg.Token)) == 1
	}
	setCookie := func(w http.ResponseWriter) {
		http.SetCookie(w, &http.Cookie{
			Name: TokenCookie, Value: cfg.Token, Path: "/",
			HttpOnly: true, Secure: cfg.Secure, SameSite: http.SameSiteStrictMode,
			MaxAge: 30 * 24 * 60 * 60,
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && valid(bearer) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(TokenCookie); err == nil && valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodGet && r.URL.Query().Has("token") {
			if !valid(r.URL.Query().Get("token")) {
				signIn(w, r, http.StatusUnauthorized, "That token is not valid.")
				return
			}
			setCookie(w)
			u := *r.URL
			q := u.Query()
			q.Del("token")
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/login" {
			if !valid(r.PostFormValue("token")) {
				signIn(w, r, http.StatusUnauthorized, "That token is not valid.")
				return
			}
			setCookie(w)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="icb"`)
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "authentication required: send Authorization: Bearer <token>"}` + "\n"))
		case strings.HasPrefix(r.URL.Path, "/mcp"):
			http.Error(w, "authentication required: send Authorization: Bearer <token>", http.StatusUnauthorized)
		default:
			signIn(w, r, http.StatusUnauthorized, "")
		}
	})
}

// signIn renders the token form.
func signIn(w http.ResponseWriter, _ *http.Request, status int, problem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	msg := ""
	if problem != "" {
		msg = `<p class="err">` + problem + `</p>`
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>icb: sign in</title>
<style>body{font:15px system-ui,sans-serif;max-width:28rem;margin:10vh auto;padding:0 16px}input,button{font:inherit;padding:6px 10px}input{width:100%;box-sizing:border-box;margin:8px 0}.err{color:#dc2626}</style>
<h1>icb</h1><p>Enter the access token printed by <code>icb serve</code> (or set with <code>-token</code> / <code>ICB_TOKEN</code>).</p>` + msg + `
<form method="post" action="/login"><input name="token" type="password" autocomplete="current-password" autofocus required><button>Sign in</button></form>`))
}

// SecurityHeaders sets headers every response gets. The CSP allows the
// UI's own scripts and the inline styles Mermaid and Shiki emit.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

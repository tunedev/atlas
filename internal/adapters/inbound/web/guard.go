package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
)

// sessionCookie is the cookie name the browser holds after exchanging the
// startup token, per §What is never exposed.
const sessionCookie = "atlas_session"

// sessionBodyMaxBytes bounds the /session request body; a token exchange is
// well under it.
const sessionBodyMaxBytes = 1 << 10

// rpcErrors writes an unauthenticated failure in whatever wire format the
// caller's protocol expects (Connect unary or streaming, gRPC, gRPC-Web),
// the same way the generated handler itself would.
var rpcErrors = connect.NewErrorWriter()

// guard wraps next with the checks that keep this server reachable only from
// the one authenticated local session, in order: security headers, host,
// origin, session exchange, RPC session. rpcPrefix is the path prefix the
// generated UIService handler serves.
func guard(host, token, rpcPrefix string, next http.Handler) http.Handler {
	origin := "http://" + host
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		if r.Host != host {
			http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") != origin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if r.Method == http.MethodPost && r.URL.Path == "/session" {
			exchangeSession(w, r, token)
			return
		}

		if strings.HasPrefix(r.URL.Path, rpcPrefix) && !hasSession(r, token) {
			err := connect.NewError(connect.CodeUnauthenticated, errors.New("web: no session"))
			_ = rpcErrors.Write(w, r, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// setSecurityHeaders sets the headers every response carries. No
// Access-Control-* header is ever set.
func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

// exchangeSession trades a startup token for a session cookie. The token
// comparison is constant-time so a wrong guess cannot be timed.
func exchangeSession(w http.ResponseWriter, r *http.Request, token string) {
	var body struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, sessionBodyMaxBytes)
	_ = json.NewDecoder(r.Body).Decode(&body) // a malformed or oversized body decodes to a zero token, which never matches

	if subtle.ConstantTimeCompare([]byte(body.Token), []byte(token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// hasSession reports whether r carries the session cookie set by
// exchangeSession, compared to token in constant time.
func hasSession(r *http.Request, token string) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1
}

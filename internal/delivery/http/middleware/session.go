package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service/auth"
)

// SessionCookie is the cookie name holding the session id.
const SessionCookie = "tpsa_session"

// CSRFField is the form field carrying the CSRF token.
const CSRFField = "csrf_token"

// CSRFHeader is the header HTMX requests may carry the token in instead.
const CSRFHeader = "X-CSRF-Token"

// Authenticator is the subset of the auth service this middleware needs.
type Authenticator interface {
	Authenticate(ctx context.Context, sessionID string) (*model.User, *model.Session, error)
}

// Auth resolves the session cookie and attaches the user to the request
// context. It does not reject anonymous requests; RequireAuth does that, so
// that public routes can share this resolution step.
func Auth(a Authenticator, secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
				user, _, err := a.Authenticate(ctx, c.Value)
				switch {
				case err == nil:
					ctx = WithUser(ctx, user)
					ctx = WithCSRFToken(ctx, auth.CSRFToken(c.Value, secret))
				case errors.Is(err, helper.ErrNotFound):
					// Expired, unknown, or still pending MFA. Clear the cookie
					// so the browser stops sending a session that will never
					// work again.
					ClearSessionCookie(w, r)
				default:
					// A database problem: leave the request anonymous rather
					// than failing it here, and let the handler report.
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuth rejects anonymous requests.
//
// A browser navigation is redirected to the login page carrying where it was
// going, so signing in lands the user where they meant to be. An HTMX request
// gets HX-Redirect instead, because swapping a login page into a fragment of
// the current page is worse than useless.
func RequireAuth(loginPath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if UserFrom(r.Context()) != nil {
				next.ServeHTTP(w, r)
				return
			}
			target := loginPath
			if next := r.URL.RequestURI(); r.Method == http.MethodGet && next != "/" {
				target = loginPath + "?next=" + urlQueryEscape(next)
			}
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", target)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
		})
	}
}

// CSRF rejects state-changing requests that do not carry the session's token.
//
// The token is derived from the session id, so an anonymous request has no
// token and nothing to check - the login form is protected by the fact that
// there is no session to ride on. Safe methods pass through untouched.
func CSRF(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}

			expected := CSRFTokenFrom(r.Context())
			if expected == "" {
				// Unauthenticated request: no session to forge against.
				next.ServeHTTP(w, r)
				return
			}

			submitted := r.Header.Get(CSRFHeader)
			if submitted == "" {
				// ParseForm is safe to call twice; the handler's own call
				// reuses the parsed values.
				if err := r.ParseForm(); err == nil {
					submitted = r.PostFormValue(CSRFField)
				}
			}
			if !auth.ValidCSRF(submitted, expected) {
				log.Warn("CSRF token rejected",
					"path", r.URL.Path, "method", r.Method,
					"request_id", RequestIDFrom(r.Context()))
				http.Error(w, "This page expired. Reload it and try again.", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetSessionCookie writes the session cookie.
//
// Secure is set only when the request arrived over TLS: hardcoding it would
// break a plain-HTTP deployment on an internal network, which is how this tool
// is expected to run at first, and the browser would silently drop the cookie.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isTLS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionIDFrom reads the raw session id from the request cookie.
func SessionIDFrom(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// isTLS reports whether the request reached the app over HTTPS, honouring the
// forwarded header a reverse proxy sets.
func isTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ClientIP returns the caller's address for the audit log.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if i := strings.IndexByte(fwd, ','); i > 0 {
			return strings.TrimSpace(fwd[:i])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// urlQueryEscape escapes a return path for use in the login redirect.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

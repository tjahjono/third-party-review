package middleware

import (
	"context"
	"net/http"

	"third-party-review/internal/domain"
)

type userKeyType struct{}
type csrfKeyType struct{}

var (
	userKey userKeyType
	csrfKey csrfKeyType
)

// WithUser attaches the authenticated user to the request context.
func WithUser(ctx context.Context, u *domain.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFrom returns the authenticated user, or nil when the request is
// unauthenticated.
func UserFrom(ctx context.Context) *domain.User {
	u, _ := ctx.Value(userKey).(*domain.User)
	return u
}

// UserIDFrom returns the authenticated user's id, or nil. Sign-off records who
// signed; a nil id means the attribution is unknown, which is what an
// unauthenticated deployment produces.
func UserIDFrom(ctx context.Context) *int64 {
	u := UserFrom(ctx)
	if u == nil {
		return nil
	}
	id := u.ID
	return &id
}

// WithCSRFToken attaches the per-session CSRF token to the request context so
// templates can embed it.
func WithCSRFToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}

// CSRFTokenFrom returns the request's CSRF token.
func CSRFTokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(csrfKey).(string)
	return t
}

// RequireUser is a convenience for handlers that must not run anonymously.
func RequireUser(w http.ResponseWriter, r *http.Request) (*domain.User, bool) {
	u := UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil, false
	}
	return u, true
}

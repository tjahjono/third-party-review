// Package auth handles login, sessions and optional authenticator-app MFA for
// the single internal team that uses this tool. There is deliberately no role
// or permission model: everyone who can log in can do everything.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/repository"
	"third-party-review/pkg/totp"
)

// Issuer is the name shown in the user's authenticator app.
const Issuer = "TPSA Reviewer"

// mfaPendingTTL is how long the half-finished login between password and TOTP
// stays valid. Short, because it is a credential that has passed one factor.
const mfaPendingTTL = 5 * time.Minute

// recoveryCodeCount is how many single-use codes are issued at enrolment.
const recoveryCodeCount = 8

// bcryptCost is the work factor for password hashing. 12 is a reasonable 2020s
// default: roughly a quarter of a second per hash on commodity hardware, which
// is invisible at login and expensive in bulk.
const bcryptCost = 12

// ErrInvalidCredentials is returned for every failed login regardless of
// cause. Distinguishing "no such user" from "wrong password" tells an attacker
// which usernames exist.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrMFARequired signals that the password was correct but a TOTP code is
// still needed.
var ErrMFARequired = errors.New("mfa required")

// ErrInvalidMFACode is returned for a wrong or reused authenticator code.
var ErrInvalidMFACode = errors.New("invalid authenticator code")

// ErrAccountDeactivated is returned when a correct password belongs to an
// account that has been switched off from the user management screen.
var ErrAccountDeactivated = errors.New("this account has been deactivated")

type Service struct {
	users         repository.UserRepository
	sessions      repository.SessionRepository
	recoveryCodes repository.RecoveryCodeRepository
	sessionTTL    time.Duration
	log           *slog.Logger
}

func New(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	ttl := deps.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &Service{
		users:         deps.Users,
		sessions:      deps.Sessions,
		recoveryCodes: deps.RecoveryCodes,
		sessionTTL:    ttl,
		log:           deps.Log,
	}, nil
}

// MustNew is New for wiring that cannot meaningfully recover.
func MustNew(deps Deps) *Service {
	s, err := New(deps)
	if err != nil {
		panic(err)
	}
	return s
}

// defaultSessionTTL applies when none is configured.
const defaultSessionTTL = 12 * time.Hour

// Login verifies a username and password and opens a session. When the account
// has MFA enabled the session is created in the pending state and
// MFARequired is set.
func (s *Service) Login(ctx context.Context, username, password, userAgent, ip string) (*dto.LoginResult, error) {
	username = strings.TrimSpace(username)

	user, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, helper.ErrNotFound) {
			// Hash anyway. Returning early on an unknown username makes login
			// measurably faster for names that do not exist, which enumerates
			// the user list for anyone timing it.
			_, _ = bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		s.log.Warn("failed login", "username", username, "ip", ip)
		return nil, ErrInvalidCredentials
	}

	if !user.Active {
		s.log.Warn("login attempt on a deactivated account", "username", username, "ip", ip)
		return nil, ErrAccountDeactivated
	}

	ttl := s.sessionTTL
	if user.MFAEnabled {
		ttl = mfaPendingTTL
	}
	session, err := s.newSession(ctx, user.ID, user.MFAEnabled, ttl, userAgent, ip)
	if err != nil {
		return nil, err
	}

	if user.MFAEnabled {
		return &dto.LoginResult{Session: session, User: user, MFARequired: true}, nil
	}

	if err := s.users.RecordLogin(ctx, user.ID, time.Now().UTC()); err != nil {
		s.log.Warn("could not record login time", "user_id", user.ID, "error", err)
	}
	s.log.Info("login", "username", user.Username, "ip", ip, "mfa", false)
	return &dto.LoginResult{Session: session, User: user}, nil
}

// VerifyMFA completes a pending login with an authenticator code or a recovery
// code, promoting the session to full validity.
func (s *Service) VerifyMFA(ctx context.Context, sessionID, code string) (*model.User, error) {
	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if !session.MFAPending {
		// Already verified; nothing to do.
		return s.users.GetByID(ctx, session.UserID)
	}
	if time.Now().After(session.ExpiresAt) {
		_ = s.sessions.Delete(ctx, sessionID)
		return nil, ErrInvalidCredentials
	}

	user, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}

	ok, err := totp.Validate(user.MFASecret, code, time.Now(), totp.DefaultSkew)
	if err != nil {
		s.log.Error("stored MFA secret is unreadable", "user_id", user.ID, "error", err)
		return nil, ErrInvalidMFACode
	}
	if !ok {
		// Fall back to a recovery code, which is consumed on use.
		used, rErr := s.consumeRecoveryCode(ctx, user.ID, code)
		if rErr != nil {
			return nil, rErr
		}
		if !used {
			s.log.Warn("failed MFA attempt", "username", user.Username)
			return nil, ErrInvalidMFACode
		}
		s.log.Warn("MFA satisfied with a recovery code", "username", user.Username)
	}

	if err := s.sessions.Promote(ctx, sessionID, time.Now().Add(s.sessionTTL)); err != nil {
		return nil, err
	}
	if err := s.users.RecordLogin(ctx, user.ID, time.Now().UTC()); err != nil {
		s.log.Warn("could not record login time", "user_id", user.ID, "error", err)
	}
	s.log.Info("login", "username", user.Username, "mfa", true)
	return user, nil
}

// Authenticate resolves a session cookie to a user. It returns
// helper.ErrNotFound for any session that is missing, expired or still
// pending MFA.
func (s *Service) Authenticate(ctx context.Context, sessionID string) (*model.User, *model.Session, error) {
	if sessionID == "" {
		return nil, nil, helper.ErrNotFound
	}
	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if !helper.SessionActive(session, time.Now()) {
		if time.Now().After(session.ExpiresAt) {
			_ = s.sessions.Delete(ctx, sessionID)
		}
		return nil, session, helper.ErrNotFound
	}
	user, err := s.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, session, err
	}
	if !user.Active {
		// Deactivation deletes sessions as it happens (see SetUserActive), so
		// finding one here means it was issued in the gap or survived some
		// other way. Either way, honour the deactivation and clean it up now
		// rather than granting access.
		_ = s.sessions.Delete(ctx, session.ID)
		return nil, session, helper.ErrNotFound
	}
	return user, session, nil
}

// PendingSession returns a session that has passed the password step but not
// yet MFA, for rendering the code prompt.
func (s *Service) PendingSession(ctx context.Context, sessionID string) (*model.Session, error) {
	if sessionID == "" {
		return nil, helper.ErrNotFound
	}
	session, err := s.sessions.GetByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !session.MFAPending || time.Now().After(session.ExpiresAt) {
		return nil, helper.ErrNotFound
	}
	return session, nil
}

// Logout ends a session.
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return s.sessions.Delete(ctx, sessionID)
}

// PurgeExpiredSessions removes sessions past their expiry. Called periodically
// so the table does not grow without bound.
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int, error) {
	return s.sessions.DeleteExpired(ctx, time.Now())
}

// newSession creates a session row with a cryptographically random id.
func (s *Service) newSession(ctx context.Context, userID uuid.UUID, pending bool, ttl time.Duration, userAgent, ip string) (*model.Session, error) {
	id, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	session := &model.Session{
		ID:         id,
		UserID:     userID,
		MFAPending: pending,
		UserAgent:  userAgent,
		IP:         ip,
		ExpiresAt:  time.Now().Add(ttl),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

// randomToken returns a URL-safe random string with n bytes of entropy.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// CSRFToken derives a per-session CSRF token from the session id and the
// application secret.
//
// Deriving rather than storing means there is no second thing to expire or
// keep in sync with the session, and an attacker who cannot read the cookie
// cannot compute the token.
func CSRFToken(sessionID, secret string) string {
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("csrf:" + secret + ":" + sessionID))
	return hex.EncodeToString(sum[:16])
}

// ValidCSRF compares a submitted token against the expected one in constant
// time.
func ValidCSRF(submitted, expected string) bool {
	if expected == "" || submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1
}

package domain

import "time"

// User is an internal team member. There is a single role: everyone who can
// log in can do everything. Role/permission infrastructure is deliberately
// absent (see project non-goals).
type User struct {
	ID           int64
	Username     string
	DisplayName  string
	PasswordHash string

	// MFA is optional per user. When MFAEnabled is true a TOTP code from an
	// authenticator app is required after the password step. The columns exist
	// from the first migration so enrolment can be switched on without a
	// schema change.
	MFAEnabled     bool
	MFASecret      string
	MFAEnrolledAt  *time.Time
	MFARecoveryHas bool

	LastLoginAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Session is a server-side login session keyed by an opaque cookie token.
type Session struct {
	ID        string
	UserID    int64
	ExpiresAt time.Time
	CreatedAt time.Time
	UserAgent string
	IP        string
	// MFAPending marks a session that has passed the password step but still
	// needs a TOTP code before it grants access.
	MFAPending bool
}

// Active reports whether the session may be used to authorise a request.
func (s *Session) Active(now time.Time) bool {
	return s != nil && !s.MFAPending && now.Before(s.ExpiresAt)
}

// LoginResult describes what the caller should do after a password check.
type LoginResult struct {
	Session *Session
	User    *User
	// MFARequired means the session exists but is pending: it authorises
	// nothing until a TOTP code is verified.
	MFARequired bool
}

// MFAEnrolment is what a user needs to add the account to an authenticator
// app. The secret is held here only until the user proves they can generate a
// code from it; nothing is persisted before that.
type MFAEnrolment struct {
	Secret string
	// FormattedSecret is the same secret grouped for manual typing.
	FormattedSecret string
	// URI is the otpauth:// URI a QR code would encode.
	URI string
	// QRSVG is a self-contained QR code for the URI.
	QRSVG string
}

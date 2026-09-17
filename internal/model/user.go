package model

import (
	"time"

	"github.com/google/uuid"
)

// User is an internal team member. Table: users.
//
// There is a single role: everyone who can log in can do everything,
// including managing other accounts. Role/permission infrastructure is
// deliberately absent (project non-goal) - Active is a membership switch,
// not a permission tier.
type User struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	// PasswordHash never leaves the server; it is excluded from JSON so a
	// model cannot leak it by being handed to an encoder.
	PasswordHash string `json:"-"`

	// Active gates login. Deactivating an account (rather than deleting it)
	// keeps every row it authored - finalized feedback, past logins - intact
	// and attributable, while blocking any further sign-in immediately.
	Active bool `json:"active"`

	// MFA is optional per user. When MFAEnabled is true a TOTP code from an
	// authenticator app is required after the password step.
	MFAEnabled    bool       `json:"mfa_enabled"`
	MFASecret     string     `json:"-"`
	MFAEnrolledAt *time.Time `json:"mfa_enrolled_at,omitempty"`
	// MFARecoveryHas is not a column: it reports whether any unused recovery
	// code rows remain for this user.
	MFARecoveryHas bool `json:"mfa_recovery_available"`

	// Language is the user's preferred language for AI-drafted assessor
	// feedback, rationale and executive summaries. Every review job that user
	// starts snapshots this onto the job row (see ReviewJob.Language).
	Language Language `json:"language"`

	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Session is a server-side login session keyed by an opaque cookie token.
// Table: sessions.
//
// ID stays a string rather than a uuid: it is a high-entropy random token that
// travels in a cookie, and typing it as a uuid would invite treating a guessed
// or enumerated uuid as one.
type Session struct {
	ID        string    `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	// MFAPending marks a session that has passed the password step but still
	// needs a TOTP code before it grants access.
	MFAPending bool `json:"mfa_pending"`
}

// RecoveryCode is one unused MFA recovery code. Table: mfa_recovery_codes.
// Only the bcrypt hash is stored; the plaintext is shown once at enrolment.
type RecoveryCode struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	CodeHash  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

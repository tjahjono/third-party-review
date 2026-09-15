package dto

import "third-party-review/internal/model"

// LoginResult describes what the caller should do after a password check.
type LoginResult struct {
	Session *model.Session `json:"session,omitempty"`
	User    *model.User    `json:"user,omitempty"`
	// MFARequired means the session exists but is pending: it authorises
	// nothing until a TOTP code is verified.
	MFARequired bool `json:"mfa_required"`
}

// MFAEnrolment is what a user needs to add the account to an authenticator
// app. The secret is held here only until the user proves they can generate a
// code from it; nothing is persisted before that.
type MFAEnrolment struct {
	Secret string `json:"-"`
	// FormattedSecret is the same secret grouped for manual typing.
	FormattedSecret string `json:"-"`
	// URI is the otpauth:// URI a QR code would encode.
	URI string `json:"-"`
	// QRSVG is a self-contained QR code for the URI.
	QRSVG string `json:"-"`
}

package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/pkg/totp"
)

// MFA is optional per user. Nothing forces enrolment; a team that does not
// want a second factor never sees it, and a user who enrols gets it on their
// account alone.

// BeginMFAEnrolment generates a secret and the material needed to add it to an
// authenticator app. Nothing is written to the user record: the secret is only
// stored once the user proves they can generate a code from it, so a failed
// enrolment cannot lock anyone out.
func (s *Service) BeginMFAEnrolment(ctx context.Context, userID uuid.UUID) (*dto.MFAEnrolment, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.MFAEnabled {
		return nil, helper.ValidationError{
			Field:   "mfa",
			Message: "Two-factor authentication is already switched on for this account.",
		}
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		return nil, err
	}
	uri := totp.URI(secret, Issuer, user.Username)
	svg, err := QRCodeSVG(uri, 4)
	if err != nil {
		s.log.Warn("could not render the enrolment QR code; manual entry still works", "error", err)
	}
	return &dto.MFAEnrolment{
		Secret:          secret,
		FormattedSecret: totp.FormatSecret(secret),
		URI:             uri,
		QRSVG:           svg,
	}, nil
}

// CompleteMFAEnrolment stores the secret once the user has proved they can
// produce a code from it, and issues single-use recovery codes.
func (s *Service) CompleteMFAEnrolment(ctx context.Context, userID uuid.UUID, secret, code string) ([]string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.MFAEnabled {
		return nil, helper.ValidationError{Field: "mfa", Message: "Two-factor authentication is already switched on."}
	}
	if strings.TrimSpace(secret) == "" {
		return nil, helper.ValidationError{Field: "mfa", Message: "The enrolment expired. Start again."}
	}

	ok, err := totp.Validate(secret, code, time.Now(), totp.DefaultSkew)
	if err != nil {
		return nil, helper.ValidationError{Field: "code", Message: "That secret could not be read. Start the enrolment again."}
	}
	if !ok {
		return nil, helper.ValidationError{
			Field:   "code",
			Message: "That code didn't match. Check your phone's clock is correct and try the current code.",
		}
	}

	codes, hashes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	user.MFAEnabled = true
	user.MFASecret = secret
	user.MFAEnrolledAt = &now
	if err := s.users.Update(ctx, user); err != nil {
		return nil, err
	}
	if err := s.recoveryCodes.Replace(ctx, userID, hashes); err != nil {
		return nil, err
	}

	s.log.Info("MFA enabled", "username", user.Username)
	return codes, nil
}

// DisableMFA turns the second factor off. The current password is required:
// without it, anyone who walks up to an unlocked screen can remove the very
// control that was protecting the account.
func (s *Service) DisableMFA(ctx context.Context, userID uuid.UUID, password string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.MFAEnabled {
		return nil
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return helper.ValidationError{Field: "password", Message: "That password is not correct."}
	}

	user.MFAEnabled = false
	user.MFASecret = ""
	user.MFAEnrolledAt = nil
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	if err := s.recoveryCodes.Replace(ctx, userID, nil); err != nil {
		return err
	}
	s.log.Warn("MFA disabled", "username", user.Username)
	return nil
}

// AdminResetMFA force-disables a second factor on another account, without
// the current-password check DisableMFA requires. It exists for a teammate
// who is locked out - phone lost, no recovery codes on hand - and so cannot
// clear their own second factor.
func (s *Service) AdminResetMFA(ctx context.Context, userID uuid.UUID) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !user.MFAEnabled {
		return nil
	}

	user.MFAEnabled = false
	user.MFASecret = ""
	user.MFAEnrolledAt = nil
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	if err := s.recoveryCodes.Replace(ctx, userID, nil); err != nil {
		return err
	}
	s.log.Warn("MFA reset by another account", "username", user.Username)
	return nil
}

// RegenerateRecoveryCodes issues a fresh set, invalidating the old ones.
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, password string) ([]string, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !user.MFAEnabled {
		return nil, helper.ValidationError{Field: "mfa", Message: "Two-factor authentication is not switched on."}
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, helper.ValidationError{Field: "password", Message: "That password is not correct."}
	}
	codes, hashes, err := generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := s.recoveryCodes.Replace(ctx, userID, hashes); err != nil {
		return nil, err
	}
	return codes, nil
}

// consumeRecoveryCode checks a submitted code against the stored hashes and
// removes it on a match. Recovery codes are single use by construction.
func (s *Service) consumeRecoveryCode(ctx context.Context, userID uuid.UUID, code string) (bool, error) {
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return false, nil
	}
	hashes, err := s.recoveryCodes.ListHashes(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, h := range hashes {
		if bcrypt.CompareHashAndPassword([]byte(h), []byte(normalized)) == nil {
			if err := s.recoveryCodes.Consume(ctx, userID, h); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// recoveryAlphabet excludes characters that are easy to confuse when a code is
// read off a printout: 0/O, 1/I/L.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// generateRecoveryCodes returns the plaintext codes to show the user once, and
// their bcrypt hashes to store. The plaintext is never persisted.
func generateRecoveryCodes(n int) (plain []string, hashed []string, err error) {
	for i := 0; i < n; i++ {
		code, err := randomRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		h, err := bcrypt.GenerateFromPassword([]byte(code), bcryptCost)
		if err != nil {
			return nil, nil, fmt.Errorf("auth: hash recovery code: %w", err)
		}
		plain = append(plain, code)
		hashed = append(hashed, string(h))
	}
	return plain, hashed, nil
}

// randomRecoveryCode builds a ten-character code formatted as two groups of
// five, which is legible when copied by hand.
func randomRecoveryCode() (string, error) {
	const length = 10
	var b strings.Builder
	max := big.NewInt(int64(len(recoveryAlphabet)))
	for i := 0; i < length; i++ {
		if i == length/2 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("auth: generate recovery code: %w", err)
		}
		b.WriteByte(recoveryAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// normalizeRecoveryCode lowercases and strips the formatting a user may retype.
func normalizeRecoveryCode(code string) string {
	s := strings.ToLower(strings.TrimSpace(code))
	s = strings.NewReplacer(" ", "", "-", "").Replace(s)
	if len(s) != 10 {
		return ""
	}
	return s[:5] + "-" + s[5:]
}

// EnrolmentFor rebuilds the display material for a secret already issued, so a
// mistyped confirmation code can be retried against the same secret instead of
// forcing the user to re-scan a new one.
func EnrolmentFor(secret, username string) *dto.MFAEnrolment {
	uri := totp.URI(secret, Issuer, username)
	svg, _ := QRCodeSVG(uri, 4)
	return &dto.MFAEnrolment{
		Secret:          secret,
		FormattedSecret: totp.FormatSecret(secret),
		URI:             uri,
		QRSVG:           svg,
	}
}

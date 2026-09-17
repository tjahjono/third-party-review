package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// minPasswordLength is the floor. Length is the only password rule enforced:
// composition rules push people towards predictable substitutions, whereas
// length is the property that actually costs an attacker work.
const minPasswordLength = 12

// CreateUser adds a member of the internal team.
func (s *Service) CreateUser(ctx context.Context, username, displayName, password string) (*model.User, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)

	if err := validateUsername(username); err != nil {
		return nil, err
	}
	if err := ValidatePassword(password, username); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("auth: hash password: %w", err)
	}
	if displayName == "" {
		displayName = username
	}

	user := &model.User{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: string(hash),
	}
	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, helper.ErrAlreadyExists) {
			return nil, helper.ValidationError{Field: "username", Message: "That username is already taken."}
		}
		return nil, err
	}
	s.log.Info("user created", "username", username)
	return user, nil
}

// ChangePassword updates a user's password after verifying the current one.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)); err != nil {
		return helper.ValidationError{Field: "current_password", Message: "That password is not correct."}
	}
	if err := ValidatePassword(next, user.Username); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	user.PasswordHash = string(hash)
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.log.Info("password changed", "username", user.Username)
	return nil
}

// UpdateLanguage sets the language the AI writes future review drafts and
// summaries in for this user. It takes effect the next time they start a
// review - a run already in progress keeps the language it was queued with.
func (s *Service) UpdateLanguage(ctx context.Context, userID uuid.UUID, lang model.Language) error {
	valid := false
	for _, l := range model.AllLanguages {
		if l == lang {
			valid = true
			break
		}
	}
	if !valid {
		return helper.ValidationError{Field: "language", Message: "Choose one of the supported languages."}
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	user.Language = lang
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.log.Info("AI response language changed", "username", user.Username, "language", lang)
	return nil
}

// UserCount reports how many accounts exist, used to decide whether the
// first-run setup screen should be shown.
func (s *Service) UserCount(ctx context.Context) (int, error) {
	return s.users.Count(ctx)
}

// GetUser returns one account.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return s.users.GetByID(ctx, id)
}

// ListUsers returns every account for the user management screen. There is
// a single role - anyone signed in can see and manage every other account -
// so this carries no viewer-scoping.
func (s *Service) ListUsers(ctx context.Context) ([]*model.User, error) {
	return s.users.List(ctx)
}

// RenameUser updates a teammate's username and display name.
func (s *Service) RenameUser(ctx context.Context, userID uuid.UUID, username, displayName string) error {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if err := validateUsername(username); err != nil {
		return err
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if displayName == "" {
		displayName = username
	}
	user.Username = username
	user.DisplayName = displayName
	if err := s.users.Update(ctx, user); err != nil {
		if errors.Is(err, helper.ErrAlreadyExists) {
			return helper.ValidationError{Field: "username", Message: "That username is already taken."}
		}
		return err
	}
	s.log.Info("account renamed", "user_id", userID, "username", username)
	return nil
}

// AdminResetPassword sets a new password for another account, without the
// current-password check ChangePassword requires. It exists for a teammate
// who is locked out and cannot supply their own current password.
func (s *Service) AdminResetPassword(ctx context.Context, userID uuid.UUID, next string) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := ValidatePassword(next, user.Username); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcryptCost)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}
	user.PasswordHash = string(hash)
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	s.log.Warn("password reset by another account", "username", user.Username, "by", userID)
	return nil
}

// SetUserActive turns an account's ability to sign in on or off. Deactivating
// is refused for your own account (to avoid an accidental self-lockout with
// nobody left to undo it) and for the last remaining active account (which
// would lock out the whole team). Deactivating also ends every session the
// account currently holds, so the block is immediate rather than waiting for
// a cookie to expire.
func (s *Service) SetUserActive(ctx context.Context, userID uuid.UUID, active bool, actingUserID uuid.UUID) error {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.Active == active {
		return nil
	}
	if !active {
		if userID == actingUserID {
			return helper.ValidationError{Field: "active", Message: "You can't deactivate your own account. Have another teammate do it."}
		}
		n, err := s.users.ActiveCount(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return helper.ValidationError{Field: "active", Message: "At least one account must stay active."}
		}
	}

	user.Active = active
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}

	if !active {
		if _, err := s.sessions.DeleteByUser(ctx, userID); err != nil {
			s.log.Warn("could not clear sessions for a deactivated account", "user_id", userID, "error", err)
		}
		s.log.Warn("account deactivated", "username", user.Username, "by", actingUserID)
	} else {
		s.log.Info("account reactivated", "username", user.Username, "by", actingUserID)
	}
	return nil
}

// Bootstrap creates the first account from configuration if no users exist.
// It is a no-op once anyone has been created, so restarting the container
// cannot resurrect or reset an account.
func (s *Service) Bootstrap(ctx context.Context, username, password string) error {
	n, err := s.users.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if username == "" || password == "" {
		s.log.Warn("no users exist and no bootstrap credentials were configured; " +
			"the first visit will show the setup screen")
		return nil
	}
	if _, err := s.CreateUser(ctx, username, "", password); err != nil {
		return fmt.Errorf("bootstrap the first user: %w", err)
	}
	s.log.Info("bootstrapped the first user from configuration", "username", username)
	return nil
}

// validateUsername keeps usernames to a predictable shape.
func validateUsername(username string) error {
	if len(username) < 3 {
		return helper.ValidationError{Field: "username", Message: "Username must be at least 3 characters."}
	}
	if len(username) > 64 {
		return helper.ValidationError{Field: "username", Message: "Username must be 64 characters or fewer."}
	}
	for _, r := range username {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_' && r != '@' {
			return helper.ValidationError{
				Field:   "username",
				Message: "Username may contain letters, digits, and . - _ @ only.",
			}
		}
	}
	return nil
}

// ValidatePassword enforces the length floor and rejects the handful of
// choices that defeat it.
func ValidatePassword(password, username string) error {
	if len([]rune(password)) < minPasswordLength {
		return helper.ValidationError{
			Field: "password",
			Message: fmt.Sprintf(
				"Password must be at least %d characters. A short phrase of a few words is easier to remember and harder to guess than a short complex string.",
				minPasswordLength),
		}
	}
	if len(password) > 256 {
		// bcrypt silently truncates past 72 bytes; refuse rather than let a
		// user believe a 300-character password is all being used.
		return helper.ValidationError{Field: "password", Message: "Password must be 256 characters or fewer."}
	}
	lower := strings.ToLower(password)
	if username != "" && strings.Contains(lower, strings.ToLower(username)) {
		return helper.ValidationError{Field: "password", Message: "Password must not contain the username."}
	}
	for _, bad := range []string{"password", "changeme", "letmein", "123456789012"} {
		if strings.Contains(lower, bad) {
			return helper.ValidationError{Field: "password", Message: "That password is too easy to guess. Choose something else."}
		}
	}
	return nil
}

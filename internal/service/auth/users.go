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

// UserCount reports how many accounts exist, used to decide whether the
// first-run setup screen should be shown.
func (s *Service) UserCount(ctx context.Context) (int, error) {
	return s.users.Count(ctx)
}

// GetUser returns one account.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return s.users.GetByID(ctx, id)
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

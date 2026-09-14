package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"third-party-review/internal/domain"
	"third-party-review/internal/service/auth"
	"third-party-review/pkg/totp"
)

const testPassword = "correct-horse-battery-staple"

func mustUser(t *testing.T, env *testEnv, username string) *domain.User {
	t.Helper()
	u, err := env.Auth.CreateUser(context.Background(), username, "", testPassword)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u
}

func TestLoginAndSessionLifecycle(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	user := mustUser(t, env, "assessor")

	// The password must not be recoverable from what is stored.
	stored, err := env.Repos.Users.GetByUsername(ctx, "assessor")
	if err != nil {
		t.Fatalf("GetByUsername: %v", err)
	}
	if stored.PasswordHash == testPassword || len(stored.PasswordHash) < 50 {
		t.Fatal("the password does not appear to be hashed")
	}

	res, err := env.Auth.Login(ctx, "assessor", testPassword, "curl", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.MFARequired {
		t.Error("MFA should not be required for an account without it")
	}
	if len(res.Session.ID) < 32 {
		t.Errorf("session id is only %d characters; it must be unguessable", len(res.Session.ID))
	}

	got, session, err := env.Auth.Authenticate(ctx, res.Session.ID)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("authenticated as user %d, want %d", got.ID, user.ID)
	}
	if session.MFAPending {
		t.Error("a session without MFA should not be pending")
	}

	// Username matching is case-insensitive; the password is not.
	if _, err := env.Auth.Login(ctx, "ASSESSOR", testPassword, "", ""); err != nil {
		t.Errorf("login should be case-insensitive on the username: %v", err)
	}
	if _, err := env.Auth.Login(ctx, "assessor", "CORRECT-HORSE-BATTERY-STAPLE", "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("the password comparison must be case-sensitive")
	}

	if err := env.Auth.Logout(ctx, res.Session.ID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := env.Auth.Authenticate(ctx, res.Session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a logged-out session should not authenticate, got %v", err)
	}
}

// TestLoginFailuresAreIndistinguishable: an unknown username and a wrong
// password must produce the same error, or the login form enumerates accounts.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()
	mustUser(t, env, "assessor")

	_, errUnknown := env.Auth.Login(ctx, "nobody", testPassword, "", "")
	_, errWrongPass := env.Auth.Login(ctx, "assessor", "wrong-password-entirely", "", "")

	if !errors.Is(errUnknown, auth.ErrInvalidCredentials) || !errors.Is(errWrongPass, auth.ErrInvalidCredentials) {
		t.Fatalf("both failures should be ErrInvalidCredentials, got %v and %v", errUnknown, errWrongPass)
	}
	if errUnknown.Error() != errWrongPass.Error() {
		t.Errorf("the two failures differ (%q vs %q), which enumerates usernames",
			errUnknown.Error(), errWrongPass.Error())
	}
}

func TestExpiredSessionIsRejectedAndPurged(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()
	mustUser(t, env, "assessor")

	res, err := env.Auth.Login(ctx, "assessor", testPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if _, err := env.DB.Pool().Exec(ctx,
		`UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, res.Session.ID); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, _, err := env.Auth.Authenticate(ctx, res.Session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("an expired session should not authenticate, got %v", err)
	}

	// Expired rows are swept so the table stays bounded.
	if _, err := env.Auth.PurgeExpiredSessions(ctx); err != nil {
		t.Fatalf("PurgeExpiredSessions: %v", err)
	}
	var n int
	if err := env.DB.Pool().QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Errorf("%d expired sessions survived the purge", n)
	}
}

// TestMFAEnrolmentAndLogin is the whole optional-second-factor path.
func TestMFAEnrolmentAndLogin(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()
	user := mustUser(t, env, "assessor")

	// --- enrolment ----------------------------------------------------------
	enrolment, err := env.Auth.BeginMFAEnrolment(ctx, user.ID)
	if err != nil {
		t.Fatalf("BeginMFAEnrolment: %v", err)
	}
	if enrolment.Secret == "" || enrolment.URI == "" {
		t.Fatal("enrolment is missing a secret or URI")
	}

	// Nothing is stored until the user proves they can generate a code. A
	// failed enrolment must not be able to lock anyone out.
	beforeConfirm, _ := env.Repos.Users.GetByID(ctx, user.ID)
	if beforeConfirm.MFAEnabled || beforeConfirm.MFASecret != "" {
		t.Fatal("the secret was stored before the user confirmed a code")
	}

	if _, err := env.Auth.CompleteMFAEnrolment(ctx, user.ID, enrolment.Secret, "000000"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("a wrong confirmation code should be refused, got %v", err)
	}

	code, err := totp.Code(enrolment.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	recovery, err := env.Auth.CompleteMFAEnrolment(ctx, user.ID, enrolment.Secret, code)
	if err != nil {
		t.Fatalf("CompleteMFAEnrolment: %v", err)
	}
	if len(recovery) != 8 {
		t.Errorf("got %d recovery codes, want 8", len(recovery))
	}

	// Recovery codes are stored hashed, never in plaintext.
	hashes, err := env.Repos.Users.RecoveryCodes(ctx, user.ID)
	if err != nil {
		t.Fatalf("RecoveryCodes: %v", err)
	}
	for _, plain := range recovery {
		for _, h := range hashes {
			if h == plain {
				t.Fatal("a recovery code is stored in plaintext")
			}
		}
	}

	// --- login now needs the second factor ---------------------------------
	res, err := env.Auth.Login(ctx, "assessor", testPassword, "", "")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !res.MFARequired {
		t.Fatal("MFA should be required after enrolment")
	}
	// The half-finished session authorises nothing.
	if _, _, err := env.Auth.Authenticate(ctx, res.Session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Error("a session pending MFA must not authenticate")
	}

	if _, err := env.Auth.VerifyMFA(ctx, res.Session.ID, "000000"); !errors.Is(err, auth.ErrInvalidMFACode) {
		t.Errorf("a wrong code should be refused, got %v", err)
	}

	code, _ = totp.Code(enrolment.Secret, time.Now())
	if _, err := env.Auth.VerifyMFA(ctx, res.Session.ID, code); err != nil {
		t.Fatalf("VerifyMFA: %v", err)
	}
	if _, _, err := env.Auth.Authenticate(ctx, res.Session.ID); err != nil {
		t.Errorf("the session should authenticate after MFA: %v", err)
	}

	// --- recovery codes are single use --------------------------------------
	second, _ := env.Auth.Login(ctx, "assessor", testPassword, "", "")
	if _, err := env.Auth.VerifyMFA(ctx, second.Session.ID, recovery[0]); err != nil {
		t.Fatalf("a recovery code should satisfy MFA: %v", err)
	}
	third, _ := env.Auth.Login(ctx, "assessor", testPassword, "", "")
	if _, err := env.Auth.VerifyMFA(ctx, third.Session.ID, recovery[0]); !errors.Is(err, auth.ErrInvalidMFACode) {
		t.Error("a recovery code was accepted twice")
	}
	remaining, _ := env.Repos.Users.RecoveryCodes(ctx, user.ID)
	if len(remaining) != 7 {
		t.Errorf("%d recovery codes remain, want 7", len(remaining))
	}

	// --- disabling requires the password ------------------------------------
	if err := env.Auth.DisableMFA(ctx, user.ID, "wrong-password-here"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("disabling MFA without the correct password should be refused")
	}
	if err := env.Auth.DisableMFA(ctx, user.ID, testPassword); err != nil {
		t.Fatalf("DisableMFA: %v", err)
	}
	after, _ := env.Repos.Users.GetByID(ctx, user.ID)
	if after.MFAEnabled || after.MFASecret != "" {
		t.Error("disabling MFA left the secret behind")
	}
	leftovers, _ := env.Repos.Users.RecoveryCodes(ctx, user.ID)
	if len(leftovers) != 0 {
		t.Errorf("%d recovery codes survived disabling MFA", len(leftovers))
	}
}

// TestMFAPendingSessionExpires covers the short window on a half-finished login.
func TestMFAPendingSessionExpires(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()
	user := mustUser(t, env, "assessor")

	enrolment, _ := env.Auth.BeginMFAEnrolment(ctx, user.ID)
	code, _ := totp.Code(enrolment.Secret, time.Now())
	if _, err := env.Auth.CompleteMFAEnrolment(ctx, user.ID, enrolment.Secret, code); err != nil {
		t.Fatalf("enrol: %v", err)
	}

	res, _ := env.Auth.Login(ctx, "assessor", testPassword, "", "")
	// The pending session must be short-lived: it is a credential that has
	// already passed one factor.
	if d := time.Until(res.Session.ExpiresAt); d > 10*time.Minute {
		t.Errorf("a pending session lasts %v; it should be minutes, not hours", d)
	}

	if _, err := env.DB.Pool().Exec(ctx,
		`UPDATE sessions SET expires_at = now() - interval '1 minute' WHERE id = $1`, res.Session.ID); err != nil {
		t.Fatalf("age the session: %v", err)
	}
	fresh, _ := totp.Code(enrolment.Secret, time.Now())
	if _, err := env.Auth.VerifyMFA(ctx, res.Session.ID, fresh); err == nil {
		t.Error("an expired pending session should not accept a code")
	}
}

func TestChangePassword(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()
	user := mustUser(t, env, "assessor")

	if err := env.Auth.ChangePassword(ctx, user.ID, "wrong", "a-brand-new-passphrase"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("changing a password without the current one should be refused")
	}
	if err := env.Auth.ChangePassword(ctx, user.ID, testPassword, "short"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Error("a new password below the length floor should be refused")
	}
	if err := env.Auth.ChangePassword(ctx, user.ID, testPassword, "a-brand-new-passphrase"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, err := env.Auth.Login(ctx, "assessor", testPassword, "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Error("the old password still works after a change")
	}
	if _, err := env.Auth.Login(ctx, "assessor", "a-brand-new-passphrase", "", ""); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
}

func TestBootstrapCreatesOnlyTheFirstUser(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	if err := env.Auth.Bootstrap(ctx, "first", testPassword); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	n, _ := env.Repos.Users.Count(ctx)
	if n != 1 {
		t.Fatalf("got %d users after bootstrap, want 1", n)
	}

	// A restart must not create a second account or reset the first.
	if err := env.Auth.Bootstrap(ctx, "second", testPassword); err != nil {
		t.Fatalf("second Bootstrap: %v", err)
	}
	n, _ = env.Repos.Users.Count(ctx)
	if n != 1 {
		t.Errorf("bootstrap ran again and created %d users", n)
	}
	if _, err := env.Repos.Users.GetByUsername(ctx, "second"); !errors.Is(err, domain.ErrNotFound) {
		t.Error("bootstrap created an account even though users already existed")
	}
}

// TestSignOffRecordsWhoSigned: the record has to say who signed it.
func TestSignOffRecordsWhoSigned(t *testing.T) {
	env := newTestEnv(t)
	env.truncateAll(t)
	ctx := context.Background()

	user := mustUser(t, env, "assessor")
	questions := mustIngestAndReview(t, env)

	signed, err := env.Assess.FinalizeQuestion(ctx, questions[0].ID, "Signed by the assessor.", &user.ID)
	if err != nil {
		t.Fatalf("FinalizeQuestion: %v", err)
	}
	if signed.FinalizedBy == nil || *signed.FinalizedBy != user.ID {
		t.Errorf("FinalizedBy = %v, want %d", signed.FinalizedBy, user.ID)
	}
	if signed.FinalizedAt == nil {
		t.Error("FinalizedAt was not stamped")
	}
}

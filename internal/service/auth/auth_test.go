package auth

import (
	"strings"
	"testing"
)

func TestValidatePasswordLengthFloor(t *testing.T) {
	if err := ValidatePassword("short", ""); err == nil {
		t.Error("a short password should be rejected")
	}
	if err := ValidatePassword(strings.Repeat("a", 12), ""); err != nil {
		t.Errorf("a 12-character password should be accepted, got %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a", 300), ""); err == nil {
		// bcrypt silently ignores bytes past 72; accepting a 300-character
		// password would let a user believe all of it protects them.
		t.Error("an over-long password should be rejected rather than silently truncated")
	}
}

func TestValidatePasswordRejectsObviousChoices(t *testing.T) {
	for _, p := range []string{"passwordpassword", "changeme-changeme", "letmein-letmein-1"} {
		if err := ValidatePassword(p, ""); err == nil {
			t.Errorf("%q should be rejected", p)
		}
	}
	if err := ValidatePassword("assessor-assessor-1", "assessor"); err == nil {
		t.Error("a password containing the username should be rejected")
	}
}

func TestValidateUsername(t *testing.T) {
	for _, u := range []string{"ab", "", strings.Repeat("a", 65), "bad name", "bad/name"} {
		if err := validateUsername(u); err == nil {
			t.Errorf("username %q should be rejected", u)
		}
	}
	for _, u := range []string{"sam", "sam.reviewer", "sam-reviewer_1", "sam@example.com"} {
		if err := validateUsername(u); err != nil {
			t.Errorf("username %q should be accepted, got %v", u, err)
		}
	}
}

func TestCSRFTokenIsSessionBoundAndStable(t *testing.T) {
	const secret = "app-secret"
	a := CSRFToken("session-a", secret)
	b := CSRFToken("session-b", secret)

	if a == "" {
		t.Fatal("a session should produce a token")
	}
	if a == b {
		t.Error("two sessions must not share a CSRF token")
	}
	if a != CSRFToken("session-a", secret) {
		t.Error("the token must be stable for the life of a session")
	}
	if a == CSRFToken("session-a", "different-secret") {
		t.Error("the token must depend on the application secret")
	}
	if CSRFToken("", secret) != "" {
		t.Error("an anonymous request has no session and must have no token")
	}
}

func TestValidCSRF(t *testing.T) {
	token := CSRFToken("s", "secret")
	if !ValidCSRF(token, token) {
		t.Error("a matching token should validate")
	}
	for _, bad := range []string{"", "wrong", token + "x", token[:len(token)-1]} {
		if ValidCSRF(bad, token) {
			t.Errorf("token %q should not validate", bad)
		}
	}
	if ValidCSRF(token, "") {
		t.Error("nothing should validate against an empty expected token")
	}
}

func TestRecoveryCodesAreUniqueAndUnambiguous(t *testing.T) {
	plain, hashed, err := generateRecoveryCodes(16)
	if err != nil {
		t.Fatalf("generateRecoveryCodes: %v", err)
	}
	if len(plain) != 16 || len(hashed) != 16 {
		t.Fatalf("got %d codes and %d hashes, want 16 of each", len(plain), len(hashed))
	}

	seen := map[string]bool{}
	for _, c := range plain {
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true

		if len(c) != 11 || c[5] != '-' {
			t.Errorf("code %q is not in the expected 5-5 form", c)
		}
		// Characters that are easy to confuse on a printout are excluded, so a
		// code read back by hand is not ambiguous.
		if strings.ContainsAny(c, "0o1il") {
			t.Errorf("code %q contains a visually ambiguous character", c)
		}
		// The plaintext must never equal its stored form.
		for _, h := range hashed {
			if h == c {
				t.Fatal("a recovery code was stored in plaintext")
			}
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	cases := map[string]string{
		"abcde-fghij":   "abcde-fghij",
		"ABCDE-FGHIJ":   "abcde-fghij",
		"abcdefghij":    "abcde-fghij",
		"abcde fghij":   "abcde-fghij",
		" abcde-fghij ": "abcde-fghij",
		"tooshort":      "",
		"":              "",
	}
	for in, want := range cases {
		if got := normalizeRecoveryCode(in); got != want {
			t.Errorf("normalizeRecoveryCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQRCodeSVGIsSelfContained(t *testing.T) {
	svg, err := QRCodeSVG("otpauth://totp/TPSA:alice?secret=ABCDEFGH", 4)
	if err != nil {
		t.Fatalf("QRCodeSVG: %v", err)
	}
	for _, want := range []string{"<svg", "xmlns=", "</svg>", "<rect"} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG is missing %q", want)
		}
	}
	// Nothing is fetched at render time: the page's Content-Security-Policy
	// blocks remote images, so an enrolment QR that referenced one would
	// silently fail to draw. The xmlns declaration is a namespace identifier,
	// not a request, so it is excluded before checking.
	body := strings.Replace(svg, `xmlns="http://www.w3.org/2000/svg"`, "", 1)
	for _, forbidden := range []string{"http://", "https://", "<image", "xlink:href", "url("} {
		if strings.Contains(body, forbidden) {
			t.Errorf("SVG references something external: %q", forbidden)
		}
	}
}

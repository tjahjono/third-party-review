package totp

import (
	"testing"
	"time"
)

// RFC 6238 appendix B publishes reference vectors for the secret "12345678901234567890".
// Base32 of that ASCII string is GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ. The RFC's
// SHA-1 table is 8 digits; this implementation emits 6, so the expectation is
// the low 6 digits of each published value.
func TestRFC6238ReferenceVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string // low 6 digits of the RFC's 8-digit SHA-1 value
	}{
		{59, "287082"},          // 94287082
		{1111111109, "081804"},  // 07081804
		{1111111111, "050471"},  // 14050471
		{1234567890, "005924"},  // 89005924
		{2000000000, "279037"},  // 69279037
		{20000000000, "353130"}, // 65353130
	}
	for _, c := range cases {
		got, err := Code(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("Code at %d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("Code at %d = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestValidateAcceptsCurrentCode(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	now := time.Now()
	code, err := Code(secret, now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	ok, err := Validate(secret, code, now, DefaultSkew)
	if err != nil || !ok {
		t.Fatalf("Validate(current) = %v, %v; want true, nil", ok, err)
	}
}

func TestValidateToleratesClockSkew(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()

	// A phone thirty seconds behind or ahead must still work.
	for _, offset := range []time.Duration{-Period * time.Second, Period * time.Second} {
		code, _ := Code(secret, now.Add(offset))
		ok, err := Validate(secret, code, now, DefaultSkew)
		if err != nil || !ok {
			t.Errorf("code from %v offset rejected", offset)
		}
	}

	// Two periods out is outside the window and must be rejected.
	code, _ := Code(secret, now.Add(-3*Period*time.Second))
	ok, _ := Validate(secret, code, now, DefaultSkew)
	if ok {
		t.Error("a code three periods old was accepted; the window is too wide")
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()

	for name, code := range map[string]string{
		"empty":      "",
		"too short":  "12345",
		"too long":   "1234567",
		"non-digits": "abcdef",
		"wrong":      "000000",
	} {
		ok, err := Validate(secret, code, now, DefaultSkew)
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
		if ok && code != "000000" {
			t.Errorf("%s: code %q was accepted", name, code)
		}
	}

	if _, err := Validate("not base32!", "123456", now, 1); err == nil {
		t.Error("a malformed secret should error")
	}
}

func TestValidateAcceptsUserTypedSpacing(t *testing.T) {
	secret, _ := GenerateSecret()
	now := time.Now()
	code, _ := Code(secret, now)

	// Authenticator apps display "123 456"; users paste it as shown.
	spaced := code[:3] + " " + code[3:]
	ok, err := Validate(secret, spaced, now, DefaultSkew)
	if err != nil || !ok {
		t.Errorf("a code typed with a space was rejected")
	}

	// And the secret may be pasted back with its display grouping.
	ok, err = Validate(FormatSecret(secret), code, now, DefaultSkew)
	if err != nil || !ok {
		t.Errorf("a secret with display spacing was rejected: %v", err)
	}
}

func TestGenerateSecretIsUniqueAndDecodable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := GenerateSecret()
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		if seen[s] {
			t.Fatal("GenerateSecret returned a duplicate")
		}
		seen[s] = true
		if _, err := decode(s); err != nil {
			t.Fatalf("generated secret does not decode: %v", err)
		}
	}
}

func TestURI(t *testing.T) {
	uri := URI("ABCDEFGHIJKLMNOP", "TPSA Reviewer", "alice")
	for _, want := range []string{
		"otpauth://totp/",
		"secret=ABCDEFGHIJKLMNOP",
		"issuer=TPSA+Reviewer",
		"digits=6",
		"period=30",
	} {
		if !contains(uri, want) {
			t.Errorf("URI %q is missing %q", uri, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
